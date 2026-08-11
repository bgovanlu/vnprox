package incident

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/store"
	"github.com/bgovanlu/vnprox/internal/topology"
)

func openWindow(t *testing.T, h *harness, from, to int64) string {
	t.Helper()
	inc, err := h.svc.Open(context.Background(), OpenRequest{Title: "w", StartedAt: from, EndedAt: to})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return inc.ID
}

func hasCaveat(tl *Timeline, substr string) bool {
	for _, c := range tl.Caveats {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

// TestTimeline_DiffRangeIsTheIncidentWindow: the diff beside the timeline
// describes the same range as the timeline, and an open incident asks for
// `now` rather than a timestamp — the sentinel T-2704 documents for "the live
// cluster, read right now".
func TestTimeline_DiffRangeIsTheIncidentWindow(t *testing.T) {
	ctx := context.Background()

	t.Run("closed window uses both timestamps", func(t *testing.T) {
		h := newHarness(t)
		h.setNow(5000)
		id := openWindow(t, h, 1000, 2000)
		if _, err := h.svc.Timeline(ctx, id); err != nil {
			t.Fatalf("Timeline: %v", err)
		}
		if h.diff.from != "1000" || h.diff.to != "2000" {
			t.Errorf("diff asked for (%q, %q), want (\"1000\", \"2000\")", h.diff.from, h.diff.to)
		}
	})

	t.Run("open window uses the now sentinel", func(t *testing.T) {
		h := newHarness(t)
		h.setNow(5000)
		inc, err := h.svc.Open(ctx, OpenRequest{Title: "still going", StartedAt: 1000})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		tl, err := h.svc.Timeline(ctx, inc.ID)
		if err != nil {
			t.Fatalf("Timeline: %v", err)
		}
		if h.diff.to != change.TopologyDiffNowToken {
			t.Errorf("diff asked for to=%q, want %q", h.diff.to, change.TopologyDiffNowToken)
		}
		if !tl.Window.Live || tl.Window.To != 5000 {
			t.Errorf("window = %+v, want live and ending at the clock (5000)", tl.Window)
		}
	})
}

// TestTimeline_SurfacesTheDiffRefusalRatherThanAnEmptyDiff is the property
// T-2704's author asked for explicitly: a range the diff cannot cover returns
// a typed error naming the snapshots that DO exist, and incident mode must
// surface that message. An empty diff would read as "nothing changed", which
// is a false statement an operator would act on.
func TestTimeline_SurfacesTheDiffRefusalRatherThanAnEmptyDiff(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name     string
		err      error
		wantCode string
		wantIn   string
	}{
		{
			name: "no snapshot covers the range",
			err: &change.ErrNoSnapshotForPoint{
				Side: "from", Requested: "1000", At: 1000,
				Nearest: []change.SnapshotPoint{{SnapshotID: "snap-9", Kind: "scheduled", TakenAt: 4000}},
			},
			wantCode: "no_snapshot_in_range",
			wantIn:   "snap-9",
		},
		{
			name:     "inverted range",
			err:      &change.ErrDiffRangeInverted{FromAt: 2000, ToAt: 1000},
			wantCode: "validation_failed",
			wantIn:   "",
		},
		{
			name:     "no snapshot store on this node",
			err:      &change.ErrApplyNotConfigured{},
			wantCode: "apply_unavailable",
			wantIn:   "",
		},
		{
			name:     "anything else",
			err:      errors.New("the disk fell off"),
			wantCode: "internal_error",
			wantIn:   "the disk fell off",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.diff.err = tc.err
			h.setNow(5000)
			id := openWindow(t, h, 1000, 2000)

			tl, err := h.svc.Timeline(ctx, id)
			if err != nil {
				t.Fatalf("Timeline: %v", err)
			}
			if tl.Diff != nil {
				t.Fatal("a diff was returned even though the change engine refused the range")
			}
			if tl.DiffErrorCode != tc.wantCode {
				t.Errorf("diffErrorCode = %q, want %q", tl.DiffErrorCode, tc.wantCode)
			}
			if tl.DiffError != tc.err.Error() {
				t.Errorf("diffError = %q, want the engine's own message %q", tl.DiffError, tc.err.Error())
			}
			if tc.wantIn != "" && !strings.Contains(tl.DiffError, tc.wantIn) {
				t.Errorf("diffError %q does not name %q — the message that tells the operator which "+
					"range they could ask for instead was lost", tl.DiffError, tc.wantIn)
			}
			if !hasCaveat(tl, "no point-in-time diff covers this window") {
				t.Errorf("caveats %v do not disclose the missing diff", tl.Caveats)
			}
		})
	}
}

// TestTimeline_CaveatsAreDerivedFromTheDiffsOwnCoverage gates the scope limit
// this feature inherits from T-2704 (the diff covers /etc/network/interfaces
// only; SDN entities are not diffed) and the "an absent entity on a one-sided
// node is not a deletion" rule its author warned about.
//
// Every caveat here is computed from Coverage, so this test would fail if the
// diff's scope changed and the disclosure did not — which is the whole reason
// the caveat is derived rather than written down.
func TestTimeline_CaveatsAreDerivedFromTheDiffsOwnCoverage(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.diff.result = &change.TopologyDiff{
		Added:   []topology.EntityDiff{{Ref: "iface:pve1:wg0", Kind: "iface", Change: topology.DiffAdded}},
		Removed: []topology.EntityDiff{}, Modified: []topology.EntityDiff{},
		Unattributed: 1,
		Coverage: change.DiffCoverage{
			Nodes:          []string{"pve1"},
			Paths:          []string{"/etc/network/interfaces"},
			OmittedPaths:   []string{"/etc/pve/sdn/zones.cfg"},
			UnmatchedNodes: []change.UnmatchedNode{{Node: "pve3", PresentIn: "to"}},
		},
	}
	h.setNow(5000)
	id := openWindow(t, h, 1000, 2000)

	tl, err := h.svc.Timeline(ctx, id)
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	for _, want := range []string{
		"compared /etc/network/interfaces only",
		"did not compare /etc/pve/sdn/zones.cfg",
		"pve3 (captured only in to)",
		"not evidence of a deletion",
		"explained by no changeset",
	} {
		if !hasCaveat(tl, want) {
			t.Errorf("caveats %v do not disclose %q", tl.Caveats, want)
		}
	}

	// Control: a diff with full coverage and no unattributed change makes
	// those caveats go away, so their presence above is a reading of
	// Coverage rather than a constant.
	h2 := newHarness(t)
	h2.setNow(5000)
	id2 := openWindow(t, h2, 1000, 2000)
	tl2, err := h2.svc.Timeline(ctx, id2)
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	for _, unwanted := range []string{"did not compare", "not evidence of a deletion", "explained by no changeset"} {
		if hasCaveat(tl2, unwanted) {
			t.Errorf("a fully-covered diff still produced the caveat %q: %v", unwanted, tl2.Caveats)
		}
	}
	if !hasCaveat(tl2, "compared /etc/network/interfaces only") {
		t.Errorf("the scope caveat is missing from a healthy diff: %v", tl2.Caveats)
	}
}

// TestTimeline_FlowTruncationIsReported gates the one cap the timeline
// applies. A silently short flow list would be worse than none, because a
// reader draws conclusions from it.
func TestTimeline_FlowTruncationIsReported(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.svc.cfg.FlowLimit = 3
	for i := int64(0); i < 5; i++ {
		h.seedFlow(1000+i, "10.0.0.1", "10.0.0.2")
	}
	h.setNow(5000)
	id := openWindow(t, h, 900, 2000)

	tl, err := h.svc.Timeline(ctx, id)
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	st := sourceStatus(t, tl, SourceFlow)
	if st.Status != StatusTruncated || st.Count != 3 {
		t.Errorf("flow source status = %+v, want truncated with 3 events", st)
	}
	if !hasCaveat(tl, "flow events are truncated") {
		t.Errorf("caveats %v do not disclose the flow cap", tl.Caveats)
	}

	// Control: under the cap, the same source reports ok and no caveat.
	h2 := newHarness(t)
	h2.svc.cfg.FlowLimit = 3
	h2.seedFlow(1000, "10.0.0.1", "10.0.0.2")
	h2.setNow(5000)
	id2 := openWindow(t, h2, 900, 2000)
	tl2, err := h2.svc.Timeline(ctx, id2)
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	if st := sourceStatus(t, tl2, SourceFlow); st.Status != StatusOK || st.Count != 1 {
		t.Errorf("under the cap, flow status = %+v, want ok with 1 event", st)
	}
	if hasCaveat(tl2, "truncated") {
		t.Errorf("a timeline under the cap still claims truncation: %v", tl2.Caveats)
	}
}

func sourceStatus(t *testing.T, tl *Timeline, src Source) SourceStatus {
	t.Helper()
	for _, st := range tl.Sources {
		if st.Source == src {
			return st
		}
	}
	t.Fatalf("no status reported for source %s (sources: %+v)", src, tl.Sources)
	return SourceStatus{}
}

// TestTimeline_ADeadSourceDegradesOnlyItself: one failing source must not
// fail the timeline, and "unavailable" must be distinguishable from "returned
// nothing".
func TestTimeline_ADeadSourceDegradesOnlyItself(t *testing.T) {
	ctx := context.Background()

	t.Run("an error", func(t *testing.T) {
		h := newHarness(t)
		h.seedInterleavedHistory()
		h.svc.cfg.Flows = failingFlows{}
		h.setNow(5000)
		id := openWindow(t, h, 900, 2000)

		tl, err := h.svc.Timeline(ctx, id)
		if err != nil {
			t.Fatalf("Timeline: %v", err)
		}
		if st := sourceStatus(t, tl, SourceFlow); st.Status != StatusError || !strings.Contains(st.Detail, "boom") {
			t.Errorf("flow status = %+v, want error naming the failure", st)
		}
		if len(tl.Events) != 8 {
			t.Errorf("a dead flow source left %d events; the other four sources' 8 events must survive", len(tl.Events))
		}
		if !hasCaveat(tl, "flow events may be incomplete") {
			t.Errorf("caveats %v do not disclose the failed source", tl.Caveats)
		}
	})

	t.Run("not wired at all", func(t *testing.T) {
		h := newHarness(t)
		h.seedInterleavedHistory()
		h.svc.cfg.Captures = nil
		h.setNow(5000)
		id := openWindow(t, h, 900, 2000)

		tl, err := h.svc.Timeline(ctx, id)
		if err != nil {
			t.Fatalf("Timeline: %v", err)
		}
		if st := sourceStatus(t, tl, SourceCapture); st.Status != StatusUnavailable {
			t.Errorf("capture status = %+v, want unavailable — which is a different statement from 'no captures happened'", st)
		}
		if !hasCaveat(tl, "capture events are missing from this timeline") {
			t.Errorf("caveats %v do not disclose the missing source", tl.Caveats)
		}
	})
}

type failingFlows struct{}

func (failingFlows) Query(_ context.Context, _ store.FlowFilter, _ string, _ int) ([]store.FlowSample, string, error) {
	return nil, "", errors.New("boom")
}

// TestTimeline_CaptureEventsOnlyWhenTheyHappenedInTheWindow: a capture that
// was already running before the window and is still running after it
// contributes no event, because nothing about it HAPPENED in the window.
func TestTimeline_CaptureEventsAreBoundedByTheWindow(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.seedCapture("cap-before", 100, 200, "stopped")    // entirely before
	h.seedCapture("cap-spanning", 100, 9000, "stopped") // straddles the whole window
	h.seedCapture("cap-inside", 1100, 1200, "stopped")
	h.seedCapture("cap-running", 1150, 0, "running")
	h.setNow(5000)
	id := openWindow(t, h, 1000, 2000)

	tl, err := h.svc.Timeline(ctx, id)
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	got := map[string]int{}
	for _, e := range tl.Events {
		if e.Source == SourceCapture {
			got[e.CaptureID]++
		}
	}
	want := map[string]int{"cap-inside": 2, "cap-running": 1}
	if len(got) != len(want) {
		t.Fatalf("capture events = %v, want %v", got, want)
	}
	for id, n := range want {
		if got[id] != n {
			t.Errorf("capture %s contributed %d events, want %d", id, got[id], n)
		}
	}
}

func TestService_LifecycleValidation(t *testing.T) {
	ctx := context.Background()

	t.Run("a title is required", func(t *testing.T) {
		h := newHarness(t)
		if _, err := h.svc.Open(ctx, OpenRequest{Title: "   "}); !errors.Is(err, ErrTitleRequired) {
			t.Errorf("Open(blank title): err = %v, want ErrTitleRequired", err)
		}
	})

	t.Run("an inverted window is refused", func(t *testing.T) {
		h := newHarness(t)
		_, err := h.svc.Open(ctx, OpenRequest{Title: "t", StartedAt: 2000, EndedAt: 1000})
		if !errors.Is(err, ErrWindowInverted) {
			t.Errorf("Open(inverted): err = %v, want ErrWindowInverted", err)
		}
	})

	t.Run("an empty annotation is refused", func(t *testing.T) {
		h := newHarness(t)
		id := openWindow(t, h, 1000, 2000)
		if _, err := h.svc.Annotate(ctx, id, AnnotateRequest{Body: "\n\t "}); !errors.Is(err, ErrAnnotationEmpty) {
			t.Errorf("Annotate(empty): err = %v, want ErrAnnotationEmpty", err)
		}
	})

	t.Run("double close and double open are refused", func(t *testing.T) {
		h := newHarness(t)
		h.setNow(1000)
		id := openWindow(t, h, 1000, 0)
		if _, err := h.svc.Close(ctx, id); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if _, err := h.svc.Close(ctx, id); !errors.Is(err, ErrAlreadyClosed) {
			t.Errorf("Close(twice): err = %v, want ErrAlreadyClosed", err)
		}
		if _, err := h.svc.Reopen(ctx, id); err != nil {
			t.Fatalf("Reopen: %v", err)
		}
		if _, err := h.svc.Reopen(ctx, id); !errors.Is(err, ErrAlreadyOpen) {
			t.Errorf("Reopen(twice): err = %v, want ErrAlreadyOpen", err)
		}
	})

	t.Run("an unknown incident is not found", func(t *testing.T) {
		h := newHarness(t)
		for name, err := range map[string]error{
			"Get":      mustErr(func() error { _, e := h.svc.Get(ctx, "nope"); return e }),
			"Timeline": mustErr(func() error { _, e := h.svc.Timeline(ctx, "nope"); return e }),
			"Close":    mustErr(func() error { _, e := h.svc.Close(ctx, "nope"); return e }),
			"Annotate": mustErr(func() error { _, e := h.svc.Annotate(ctx, "nope", AnnotateRequest{Body: "x"}); return e }),
		} {
			if !IsNotFound(err) {
				t.Errorf("%s(unknown id): err = %v, want a not-found", name, err)
			}
		}
	})

	t.Run("an annotation may be back-dated and survives a close", func(t *testing.T) {
		h := newHarness(t)
		h.setNow(1000)
		id := openWindow(t, h, 900, 0)
		if _, err := h.svc.Annotate(ctx, id, AnnotateRequest{At: 950, Body: "noticed at 950"}); err != nil {
			t.Fatalf("Annotate: %v", err)
		}
		h.setNow(1100)
		if _, err := h.svc.Close(ctx, id); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if _, err := h.svc.Annotate(ctx, id, AnnotateRequest{Body: "root cause, written up afterwards"}); err != nil {
			t.Fatalf("Annotate after close: %v", err)
		}
		inc, err := h.svc.Get(ctx, id)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if len(inc.Annotations) != 2 || inc.Annotations[0].At != 950 {
			t.Fatalf("annotations = %+v, want two with the back-dated one first", inc.Annotations)
		}
	})
}

func mustErr(f func() error) error { return f() }

// TestService_DefaultsAreApplied keeps New's defaulting honest — a zero
// FlowLimit must not mean "no flows".
func TestService_DefaultsAreApplied(t *testing.T) {
	svc := New(Config{Store: newMemIncidentStore()})
	if svc.cfg.FlowLimit != DefaultFlowLimit {
		t.Errorf("FlowLimit = %d, want the default %d", svc.cfg.FlowLimit, DefaultFlowLimit)
	}
	if svc.cfg.Now == nil || svc.cfg.Logger == nil {
		t.Error("New left Now or Logger nil")
	}
	if got := svc.cfg.Now(); time.Since(got) > time.Minute {
		t.Errorf("the default clock returned %v", got)
	}
}
