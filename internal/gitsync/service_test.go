// SPDX-License-Identifier: Apache-2.0

package gitsync_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/gitsync"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/spec"
)

// newTestService wires a Service over the doubles with everything else
// defaulted. Every acceptance test below builds through this so the wiring
// under test is identical in all of them.
func newTestService(t *testing.T, src gitsync.Source, stager gitsync.ChangesetStager, g *inventory.Graph, mutate func(*gitsync.Config)) *gitsync.Service {
	t.Helper()
	logger, _ := captureLogger()
	cfg := gitsync.Config{
		Enabled:      true,
		Source:       src,
		Ref:          "main",
		Path:         "network/cluster.yaml",
		PollInterval: 20 * time.Millisecond,
		Changesets:   stager,
		Inventory:    g,
		Logger:       logger,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	return gitsync.New(cfg)
}

// planFor is the independent computation of "what the plan should be": the
// same spec.Parse + spec.Import the service runs, called directly by the
// test so a mismatch means the service diverged from the spec package rather
// than the test agreeing with itself.
func planFor(t *testing.T, doc []byte, g *inventory.Graph) []change.Op {
	t.Helper()
	parsed, err := spec.Parse(doc)
	if err != nil {
		t.Fatalf("spec.Parse: %v", err)
	}
	ops, _, err := spec.Import(parsed, g.Snapshot())
	if err != nil {
		t.Fatalf("spec.Import: %v", err)
	}
	return ops
}

func opKeys(ops []change.Op) []string {
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		out = append(out, fmt.Sprintf("%s %s", op.Type, op.Target.String()))
	}
	return out
}

// TestChangesetStagerHasNoApplyVerb is the structural half of the stage-only
// invariant: the change-engine seam this package holds cannot apply, because
// the type it is handed has no such method. Mirrors internal/mcp's
// TestChangesetStagerHasNoMutationVerb (T-1701) and internal/plugin's
// interface-surface test (T-1702).
func TestChangesetStagerHasNoApplyVerb(t *testing.T) {
	typ := reflect.TypeOf((*gitsync.ChangesetStager)(nil)).Elem()
	for _, forbidden := range []string{"Apply", "Confirm", "Rollback", "Discard", "Approve"} {
		for i := 0; i < typ.NumMethod(); i++ {
			if strings.Contains(typ.Method(i).Name, forbidden) {
				t.Fatalf("gitsync.ChangesetStager exposes %q — the sync seam must be stage-only", typ.Method(i).Name)
			}
		}
	}
	for _, want := range []string{"CreateWithOrigin", "UpdateDraft", "List"} {
		if _, ok := typ.MethodByName(want); !ok {
			t.Fatalf("gitsync.ChangesetStager is missing %q", want)
		}
	}
}

// TestAC1_DivergenceOpensExactlyOneDraftAndNeverApplies is acceptance
// criterion 1, including the control leg the card requires: the spy's apply
// counter is proven to move before "it did not move" is asserted as evidence.
func TestAC1_DivergenceOpensExactlyOneDraftAndNeverApplies(t *testing.T) {
	g := buildFixtureGraph(t, fixtureThreeNode)
	doc := divergentSpec(t, g, 1400)
	want := planFor(t, doc, g)
	if len(want) == 0 {
		t.Fatal("the divergent fixture spec plans to zero ops; the test would assert nothing")
	}

	src := &fakeSource{}
	src.set("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", doc)
	stager := newFakeStager()
	svc := newTestService(t, src, stager, g, nil)

	res, err := svc.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if !res.Created {
		t.Fatalf("Sync did not open a draft: %+v", res)
	}

	create, update, _, apply := stager.counts()
	if create != 1 || update != 0 {
		t.Errorf("create/update calls = %d/%d, want 1/0", create, update)
	}
	if got := stager.openSyncCount(); got != 1 {
		t.Errorf("open sync changesets = %d, want exactly 1", got)
	}

	c := stager.get(res.ChangesetID)
	if c.Status != change.StatusDraft {
		t.Errorf("changeset status = %q, want %q", c.Status, change.StatusDraft)
	}
	if c.Origin != change.OriginGitSync {
		t.Errorf("changeset origin = %q, want %q", c.Origin, change.OriginGitSync)
	}
	if got, wantKeys := opKeys(c.Ops), opKeys(want); !reflect.DeepEqual(got, wantKeys) {
		t.Errorf("staged ops do not match the plan.\n got: %v\nwant: %v", got, wantKeys)
	}

	// The assertion the card asks for...
	if apply != 0 {
		t.Fatalf("apply was called %d time(s) during a sync; sync must stage and stop", apply)
	}
	// ...and the control leg that makes it mean something. Without this, an
	// applyCalls counter that no code path could ever increment would make
	// the assertion above pass forever.
	if err := stager.Apply(context.Background(), c.ID); err != nil {
		t.Fatalf("control-leg Apply: %v", err)
	}
	if _, _, _, apply = stager.counts(); apply != 1 {
		t.Fatalf("control leg: applyCalls = %d after an explicit Apply, want 1 — the spy does not count, so the assertion above proves nothing", apply)
	}
}

// TestAC2_UnchangedRemoteProducesNoSecondDraftAndNoStoreWrite is acceptance
// criterion 2, in both the "there is a draft" and "there is nothing to do"
// directions — a no-op that still wrote would be just as wrong.
func TestAC2_UnchangedRemoteProducesNoSecondDraftAndNoStoreWrite(t *testing.T) {
	g := buildFixtureGraph(t, fixtureThreeNode)

	tests := []struct {
		name       string
		doc        []byte
		wantCreate int
	}{
		{name: "divergent spec: one draft, then nothing", doc: divergentSpec(t, g, 1400), wantCreate: 1},
		{name: "converged spec: never a draft at all", doc: specMatchingLive(t, g), wantCreate: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := &fakeSource{}
			src.set("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", tc.doc)
			stager := newFakeStager()
			svc := newTestService(t, src, stager, g, nil)

			if _, err := svc.Sync(context.Background()); err != nil {
				t.Fatalf("first Sync: %v", err)
			}
			create1, update1, _, _ := stager.counts()
			if create1 != tc.wantCreate {
				t.Fatalf("first Sync created %d changeset(s), want %d", create1, tc.wantCreate)
			}

			res, err := svc.Sync(context.Background())
			if err != nil {
				t.Fatalf("second Sync: %v", err)
			}
			if !res.Unchanged {
				t.Errorf("second Sync did not report the revision as unchanged: %+v", res)
			}
			create2, update2, _, _ := stager.counts()
			if create2 != create1 {
				t.Errorf("second Sync created %d more changeset(s); want none", create2-create1)
			}
			if update2 != update1 {
				t.Errorf("second Sync wrote %d update(s) to the store; want none", update2-update1)
			}
			if got := stager.openSyncCount(); got > 1 {
				t.Errorf("open sync changesets = %d, want at most 1", got)
			}
			// The remote WAS polled both times — "no store write" is not
			// achieved by not looking.
			if src.fetchCount() != 2 {
				t.Errorf("source fetched %d time(s), want 2", src.fetchCount())
			}
		})
	}
}

// TestAC3_ThreeDivergentPollsNeverExceedOneOpenSyncChangeset is acceptance
// criterion 3, asserted after every one of the three polls rather than only
// at the end.
func TestAC3_ThreeDivergentPollsNeverExceedOneOpenSyncChangeset(t *testing.T) {
	g := buildFixtureGraph(t, fixtureThreeNode)
	src := &fakeSource{}
	stager := newFakeStager()
	svc := newTestService(t, src, stager, g, nil)

	var lastDoc []byte
	var firstID string
	for i, mtu := range []int{1400, 1450, 9000} {
		doc := divergentSpec(t, g, mtu)
		lastDoc = doc
		src.set(fmt.Sprintf("%040d", i+1), doc)

		res, err := svc.Sync(context.Background())
		if err != nil {
			t.Fatalf("poll %d: %v", i+1, err)
		}
		if got := stager.openSyncCount(); got != 1 {
			t.Fatalf("after poll %d: %d open sync changesets, want exactly 1", i+1, got)
		}
		if i == 0 {
			if !res.Created {
				t.Fatalf("poll 1 did not open a draft: %+v", res)
			}
			firstID = res.ChangesetID
			continue
		}
		if !res.Updated {
			t.Fatalf("poll %d did not update the existing draft: %+v", i+1, res)
		}
		if res.ChangesetID != firstID {
			t.Fatalf("poll %d updated changeset %q, want the original %q", i+1, res.ChangesetID, firstID)
		}
	}

	create, update, _, apply := stager.counts()
	if create != 1 || update != 2 {
		t.Errorf("create/update calls = %d/%d across three divergent polls, want 1/2", create, update)
	}
	if apply != 0 {
		t.Errorf("apply called %d time(s); sync never applies", apply)
	}
	if got, want := opKeys(stager.get(firstID).Ops), opKeys(planFor(t, lastDoc, g)); !reflect.DeepEqual(got, want) {
		t.Errorf("the surviving draft does not carry the latest plan.\n got: %v\nwant: %v", got, want)
	}
}

// TestAC4_UnparseableSpecRaisesAFindingAndLeavesTheDraftAlone is acceptance
// criterion 4. The finding must name both the file and the parse error, and
// the open draft must come out byte-identical.
func TestAC4_UnparseableSpecRaisesAFindingAndLeavesTheDraftAlone(t *testing.T) {
	g := buildFixtureGraph(t, fixtureThreeNode)
	src := &fakeSource{}
	src.set("cccccccccccccccccccccccccccccccccccccccc", divergentSpec(t, g, 1400))
	stager := newFakeStager()
	svc := newTestService(t, src, stager, g, nil)

	res, err := svc.Sync(context.Background())
	if err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	before := stager.get(res.ChangesetID)
	create1, update1, _, _ := stager.counts()

	// Same repository, next commit: someone hand-edited the YAML and broke it.
	src.set("dddddddddddddddddddddddddddddddddddddddd", []byte("specVersion: 1\nnodes: [ this is not: valid: yaml\n"))
	_, err = svc.Sync(context.Background())
	if !errors.Is(err, gitsync.ErrSpecParse) {
		t.Fatalf("Sync error = %v, want one wrapping ErrSpecParse", err)
	}

	issues := svc.Issues()
	var found *gitsync.Issue
	for i := range issues {
		if issues[i].Check == gitsync.CheckSpecUnparseable {
			found = &issues[i]
		}
	}
	if found == nil {
		t.Fatalf("no %s finding was raised; issues = %+v", gitsync.CheckSpecUnparseable, issues)
	}
	if !strings.Contains(found.Detail, "network/cluster.yaml") {
		t.Errorf("finding does not name the file: %q", found.Detail)
	}
	if !strings.Contains(found.Detail, "parsing spec document") && !strings.Contains(found.Detail, "does not parse") {
		t.Errorf("finding does not carry the parse error: %q", found.Detail)
	}

	create2, update2, _, apply := stager.counts()
	if create2 != create1 {
		t.Errorf("an unparseable spec created %d changeset(s)", create2-create1)
	}
	if update2 != update1 {
		t.Errorf("an unparseable spec modified the open draft (%d update call(s))", update2-update1)
	}
	if apply != 0 {
		t.Errorf("apply called %d time(s)", apply)
	}
	after := stager.get(res.ChangesetID)
	if after.Status != change.StatusDraft {
		t.Errorf("the open draft's status changed to %q", after.Status)
	}
	if !reflect.DeepEqual(opKeys(before.Ops), opKeys(after.Ops)) {
		t.Errorf("the open draft's ops changed.\nbefore: %v\n after: %v", opKeys(before.Ops), opKeys(after.Ops))
	}
	if got := stager.openSyncCount(); got != 1 {
		t.Errorf("open sync changesets = %d after the bad poll, want 1", got)
	}
}

// TestAC7_UnreachableRemoteDegradesToAFindingAndRetries is acceptance
// criterion 7's first half: an unreachable remote produces a finding and
// keeps trying, and Run — the supervised actor — returns nil on cancellation
// rather than an error that would take every other subsystem down with it.
func TestAC7_UnreachableRemoteDegradesToAFindingAndRetries(t *testing.T) {
	g := buildFixtureGraph(t, fixtureSingleNode)
	src := &fakeSource{err: fmt.Errorf("%w: dial tcp: connection refused", gitsync.ErrUnreachable)}
	stager := newFakeStager()
	svc := newTestService(t, src, stager, g, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()

	// Wait for at least two attempts: one finding is a report, two is a retry.
	deadline := time.Now().Add(5 * time.Second)
	for src.fetchCount() < 2 && time.Now().After(deadline) == false {
		time.Sleep(5 * time.Millisecond)
	}
	if src.fetchCount() < 2 {
		cancel()
		<-done
		t.Fatalf("source was fetched %d time(s) in 5s; an unreachable remote must be retried", src.fetchCount())
	}

	var unreachable *gitsync.Issue
	for _, iss := range svc.Issues() {
		if iss.Check == gitsync.CheckUnreachable {
			iss := iss
			unreachable = &iss
		}
	}
	if unreachable == nil {
		t.Fatalf("no %s finding was raised", gitsync.CheckUnreachable)
	}
	if !strings.Contains(unreachable.Detail, "connection refused") {
		t.Errorf("finding does not carry the transport error: %q", unreachable.Detail)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v on cancellation; the runGroup contract requires nil, or the whole daemon goes down with the sync", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s of cancellation")
	}

	if create, update, _, apply := stager.counts(); create != 0 || update != 0 || apply != 0 {
		t.Errorf("an unreachable remote touched the change engine: create=%d update=%d apply=%d", create, update, apply)
	}
}

// TestAC7_AHangingRemoteNeverBlocksTheCaller is the "never blocks daemon
// startup or any other subsystem" half: a source that never answers must not
// stop Run from returning promptly on shutdown. If the fetch were done
// synchronously before Run's select, this test would hang.
func TestAC7_AHangingRemoteNeverBlocksTheCaller(t *testing.T) {
	g := buildFixtureGraph(t, fixtureSingleNode)
	src := &hangingSource{}
	stager := newFakeStager()
	svc := newTestService(t, src, stager, g, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	started := time.Now()
	go func() { done <- svc.Run(ctx) }()

	// Let the first (hanging) fetch get under way, then shut down.
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil", err)
		}
		if elapsed := time.Since(started); elapsed > 5*time.Second {
			t.Errorf("Run took %s to return after cancellation", elapsed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run never returned: a hanging remote blocked shutdown")
	}
}

// TestDisabledServiceContactsNothing is the "off by default" guarantee:
// nothing is fetched and nothing is staged until an operator configures it.
func TestDisabledServiceContactsNothing(t *testing.T) {
	g := buildFixtureGraph(t, fixtureSingleNode)
	src := &fakeSource{}
	src.set("eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", divergentSpec(t, g, 1400))
	stager := newFakeStager()
	svc := newTestService(t, src, stager, g, func(c *gitsync.Config) { c.Enabled = false })

	if _, err := svc.Sync(context.Background()); !errors.Is(err, gitsync.ErrNotConfigured) {
		t.Fatalf("Sync on a disabled service = %v, want ErrNotConfigured", err)
	}
	if src.fetchCount() != 0 {
		t.Errorf("a disabled service contacted the remote %d time(s)", src.fetchCount())
	}
	if create, update, list, apply := stager.counts(); create+update+list+apply != 0 {
		t.Errorf("a disabled service touched the change engine: %d/%d/%d/%d", create, update, list, apply)
	}
	st := svc.Status()
	if st.Enabled {
		t.Error("Status reports enabled for a disabled service")
	}
	if st.Remote != "" || st.Path != "" {
		t.Errorf("a disabled service's status leaks its would-be configuration: %+v", st)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run on a disabled service = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run on a disabled service did not return on cancellation")
	}
	if src.fetchCount() != 0 {
		t.Errorf("a disabled service's Run contacted the remote %d time(s)", src.fetchCount())
	}
}

// TestStatusExplainsWhyTheDraftExists covers the card's `gitsync status`
// bullet: last fetched sha, last plan, and the reason the draft is open.
func TestStatusExplainsWhyTheDraftExists(t *testing.T) {
	g := buildFixtureGraph(t, fixtureThreeNode)
	doc := divergentSpec(t, g, 1400)
	src := &fakeSource{}
	const sha = "0123456789abcdef0123456789abcdef01234567"
	src.set(sha, doc)
	stager := newFakeStager()
	svc := newTestService(t, src, stager, g, nil)

	res, err := svc.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	st := svc.Status()
	if st.LastFetchedSHA != sha {
		t.Errorf("Status.LastFetchedSHA = %q, want %q", st.LastFetchedSHA, sha)
	}
	if st.OpenChangesetID != res.ChangesetID {
		t.Errorf("Status.OpenChangesetID = %q, want %q", st.OpenChangesetID, res.ChangesetID)
	}
	if st.PlanOpCount != len(planFor(t, doc, g)) {
		t.Errorf("Status.PlanOpCount = %d, want %d", st.PlanOpCount, len(planFor(t, doc, g)))
	}
	if len(st.Plan) != st.PlanOpCount {
		t.Errorf("Status.Plan has %d line(s) for %d op(s)", len(st.Plan), st.PlanOpCount)
	}
	if !strings.Contains(st.OpenChangesetReason, "applied nothing") {
		t.Errorf("Status.OpenChangesetReason does not say vnprox applied nothing: %q", st.OpenChangesetReason)
	}
	if st.LastError != "" {
		t.Errorf("Status.LastError = %q on a clean sync", st.LastError)
	}
}

// hangingSource blocks until its context is cancelled.
type hangingSource struct{}

func (hangingSource) Describe() string { return "https://git.example.test/org/infra (hanging)" }

func (hangingSource) Fetch(ctx context.Context, _, _ string) (gitsync.Revision, error) {
	<-ctx.Done()
	return gitsync.Revision{}, fmt.Errorf("%w: %w", gitsync.ErrUnreachable, ctx.Err())
}
