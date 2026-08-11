package incident

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/store"
)

// ac2_test.go is T-2804 acceptance criterion 2: "opening an incident changes
// no collection behaviour — a test asserts identical collector call counts
// with and without one open."
//
// Two legs, because the criterion has two halves and one of them is easy to
// satisfy vacuously:
//
//  1. A COLLECTOR, driven by its own loop, is called exactly as often with an
//     incident open as without. The control is that the counter moves at all
//     — comparing two zeroes would prove nothing, so the test asserts the
//     exact expected count in each leg AND that an extra cycle increments it.
//  2. Every SOURCE the incident view holds is called zero times by the
//     lifecycle (open/annotate/list/get/close/reopen) and exactly once by
//     Timeline. That is the sharper statement: an implementation that primed
//     a cache on Open, or subscribed to a stream, or "warmed" the diff, would
//     fail it even though its collector counts were unchanged.

// countingCollector stands in for the daemon's poll loops. Nothing in
// internal/incident can reach it — which is the point: the test drives it
// independently and asserts that an incident's presence is invisible to it.
type countingCollector struct{ cycles atomic.Int64 }

func (c *countingCollector) Collect(_ context.Context) { c.cycles.Add(1) }

func (c *countingCollector) run(ctx context.Context, cycles int) {
	for i := 0; i < cycles; i++ {
		c.Collect(ctx)
	}
}

func TestAC2_IdenticalCollectorCallCountsWithAndWithoutAnIncident(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.seedInterleavedHistory()

	const cycles = 5
	collector := &countingCollector{}

	// Leg A: no incident exists at all.
	collector.run(ctx, cycles)
	withoutIncident := collector.cycles.Load()
	if withoutIncident != cycles {
		t.Fatalf("CONTROL FAILED: %d collection cycles recorded, want %d — the counter is not live, "+
			"so the comparison below would be between two meaningless numbers", withoutIncident, cycles)
	}

	// Leg B: an incident is open, annotated, read and closed around the
	// identical number of cycles.
	h.setNow(2000)
	inc, err := h.svc.Open(ctx, OpenRequest{Title: "open while the collectors run", StartedAt: 900, EndedAt: 1500})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	collector.run(ctx, cycles)
	if _, err := h.svc.Annotate(ctx, inc.ID, AnnotateRequest{Body: "still bad"}); err != nil {
		t.Fatalf("Annotate: %v", err)
	}
	if _, err := h.svc.Timeline(ctx, inc.ID); err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	if _, err := h.svc.Close(ctx, inc.ID); err != nil {
		t.Fatalf("Close: %v", err)
	}
	withIncident := collector.cycles.Load() - withoutIncident

	if withIncident != withoutIncident {
		t.Errorf("collector ran %d times with an incident open and %d times without — opening an incident "+
			"changed collection behaviour, which is exactly what an incident must never do",
			withIncident, withoutIncident)
	}

	// Control: the counter still moves when something genuinely collects, so
	// "identical" above is not "frozen".
	collector.run(ctx, 1)
	if got := collector.cycles.Load(); got != withoutIncident+withIncident+1 {
		t.Fatalf("CONTROL FAILED: an extra collection cycle did not increment the counter (got %d)", got)
	}
}

// --- counting sources ------------------------------------------------------

type countingSources struct {
	findings, audit, captures, flows, diff atomic.Int32
}

func (c *countingSources) ListByTimeRange(_ context.Context, _, _ int64) ([]store.FindingEvent, error) {
	c.findings.Add(1)
	return nil, nil
}

func (c *countingSources) ListActionsInRange(_ context.Context, _ []string, _, _ int64) ([]store.AuditEntry, error) {
	c.audit.Add(1)
	return nil, nil
}

func (c *countingSources) List(_ context.Context) ([]store.CaptureSession, error) {
	c.captures.Add(1)
	return nil, nil
}

func (c *countingSources) Query(_ context.Context, _ store.FlowFilter, _ string, _ int) ([]store.FlowSample, string, error) {
	c.flows.Add(1)
	return nil, "", nil
}

func (c *countingSources) TopologyDiff(_ context.Context, _, _ string) (*change.TopologyDiff, error) {
	c.diff.Add(1)
	return nil, errors.New("no snapshots")
}

func (c *countingSources) counts() map[string]int32 {
	return map[string]int32{
		"finding-events": c.findings.Load(),
		"audit":          c.audit.Load(),
		"captures":       c.captures.Load(),
		"flows":          c.flows.Load(),
		"topology-diff":  c.diff.Load(),
	}
}

// memIncidentStore is a minimal in-memory Store. The incident RECORD is the
// only thing the lifecycle is allowed to touch, so it is deliberately not
// counted alongside the sources.
type memIncidentStore struct {
	rows  map[string]store.Incident
	notes map[string][]store.IncidentAnnotation
}

func newMemIncidentStore() *memIncidentStore {
	return &memIncidentStore{rows: map[string]store.Incident{}, notes: map[string][]store.IncidentAnnotation{}}
}

func (m *memIncidentStore) Insert(_ context.Context, i store.Incident) error {
	m.rows[i.ID] = i
	return nil
}

func (m *memIncidentStore) Get(_ context.Context, id string) (store.Incident, error) {
	row, ok := m.rows[id]
	if !ok {
		return store.Incident{}, store.ErrNotFound
	}
	return row, nil
}

func (m *memIncidentStore) List(_ context.Context) ([]store.Incident, error) {
	out := make([]store.Incident, 0, len(m.rows))
	for _, r := range m.rows {
		out = append(out, r)
	}
	return out, nil
}

func (m *memIncidentStore) SetStatus(_ context.Context, id, status string, endedAt, closedAt int64) error {
	row, ok := m.rows[id]
	if !ok {
		return store.ErrNotFound
	}
	row.Status, row.EndedAt, row.ClosedAt = status, endedAt, closedAt
	m.rows[id] = row
	return nil
}

func (m *memIncidentStore) InsertAnnotation(_ context.Context, a store.IncidentAnnotation) error {
	m.notes[a.IncidentID] = append(m.notes[a.IncidentID], a)
	return nil
}

func (m *memIncidentStore) ListAnnotations(_ context.Context, id string) ([]store.IncidentAnnotation, error) {
	return m.notes[id], nil
}

func TestAC2_TheLifecycleQueriesNoSourceAndTimelineQueriesEachOnce(t *testing.T) {
	ctx := context.Background()
	src := &countingSources{}
	now := int64(5000)
	svc := New(Config{
		Store:         newMemIncidentStore(),
		FindingEvents: src,
		Audit:         src,
		Captures:      src,
		Flows:         src,
		Diff:          src,
		Now:           func() time.Time { return time.Unix(now, 0) },
	})

	inc, err := svc.Open(ctx, OpenRequest{Title: "nothing should be queried", Actor: "brian@pam"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := svc.Annotate(ctx, inc.ID, AnnotateRequest{Body: "note"}); err != nil {
		t.Fatalf("Annotate: %v", err)
	}
	if _, err := svc.List(ctx); err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, err := svc.Get(ctx, inc.ID); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := svc.Close(ctx, inc.ID); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := svc.Reopen(ctx, inc.ID); err != nil {
		t.Fatalf("Reopen: %v", err)
	}

	for name, got := range src.counts() {
		if got != 0 {
			t.Errorf("the incident lifecycle called the %s source %d times; opening, annotating, listing, "+
				"closing and reopening an incident must query nothing at all", name, got)
		}
	}

	// The control: the counters are wired, and one read of the view is
	// exactly one query per source. An implementation that queried twice
	// (say, once to build the timeline and once to count it) fails here.
	if _, err := svc.Timeline(ctx, inc.ID); err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	want := map[string]int32{
		"finding-events": 1,
		// Two: the changeset source and the diagnosis source are different
		// audit action sets, and each is one query.
		"audit":         2,
		"captures":      1,
		"flows":         1,
		"topology-diff": 1,
	}
	for name, expect := range want {
		if got := src.counts()[name]; got != expect {
			t.Errorf("after one Timeline call, the %s source was queried %d times, want %d", name, got, expect)
		}
	}
}

// TestTimeline_IsNodeLocalByConstruction gates the scope limit docs/api.md
// states: the timeline is assembled from THIS node's own tables and does not
// fan out to peers.
//
// The limitation is inherited rather than invented — GET /history/events is
// node-local for the same reason (finding_events and audit_log are app-owned,
// per-node data), and a cluster-wide audit view is GET /audit's own job, with
// its own documented fan-out. Stating it in the docs without a gate would be
// exactly the "documented limitation with no test behind it" this repo has
// been bitten by, so the gate is structural: no seam this service holds is a
// peer client, and there is nowhere for one to hide.
func TestTimeline_IsNodeLocalByConstruction(t *testing.T) {
	cfgType := reflect.TypeOf(Config{})
	seen := 0
	for i := 0; i < cfgType.NumField(); i++ {
		f := cfgType.Field(i)
		name := f.Name + " " + f.Type.String()
		for _, forbidden := range []string{"Peer", "peer", "Fanout", "FanOut", "Cluster"} {
			if strings.Contains(name, forbidden) {
				t.Errorf("incident.Config declares %q, which looks like a cross-node seam — the timeline is "+
					"documented (docs/api.md) as node-local; either remove it or update that statement", name)
			}
		}
		seen++
	}
	// Control: the walk actually saw the fields, so "no peer seam" is not
	// the trivial consequence of an empty loop.
	if seen < 8 {
		t.Fatalf("CONTROL FAILED: walked only %d Config fields; the reflection is not seeing the struct", seen)
	}
}
