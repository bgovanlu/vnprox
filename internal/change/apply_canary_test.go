// SPDX-License-Identifier: Apache-2.0

package change_test

// apply_canary_test.go is T-2602's acceptance suite: a staged (canary) apply
// touches only the canary nodes, aborts by restoring only what it applied,
// gates automatic promotion on real evidence, survives a daemon restart, and
// never outlives the commit-confirm window that guards it.
//
// A NOTE ON THE ZERO-CALL ASSERTIONS. Several of these tests assert that a
// node's client recorded ZERO calls. An assertion like that is worthless on
// its own: it passes just as happily when the spy is broken, when the wrong
// node name is used, or when the changeset never had a second node to begin
// with. Every one of them here is therefore paired with a CONTROL LEG that
// fails loudly if the same spy, on the same nodes, would have counted zero
// anyway — so a zero is only ever reported as evidence once we have shown
// the counter can be non-zero.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/store"
)

// --- helpers --------------------------------------------------------------

// settableClock is a mutable clock the staged-apply suite shares, so the
// commit-confirm deadline and the hold deadline can be crossed by setting the
// time rather than by sleeping. (Distinct from localtimer_test.go's fakeClock,
// which is a non-locking double for direct LocalTimerAgent unit tests; this
// one is read from timer callbacks on other goroutines.)
type settableClock struct {
	at time.Time
	mu sync.Mutex
}

func newSettableClock() *settableClock {
	return &settableClock{at: time.Unix(1_700_000_000, 0)}
}

func (c *settableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *settableClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

func withClock(c *settableClock) func(*change.Config) {
	return func(cfg *change.Config) { cfg.Now = c.Now }
}

// fakeCanaryChecker is a change.CanaryHealthChecker recording every call, so
// the auto-gate tests can prove the gate actually consulted it rather than
// reaching its verdict some other way.
type fakeCanaryChecker struct {
	err     error
	calls   [][]string
	since   []int64
	verdict change.CanaryVerdict
	mu      sync.Mutex
}

func (c *fakeCanaryChecker) CheckCanary(_ context.Context, nodes []string, sinceUnix int64) (change.CanaryVerdict, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, append([]string(nil), nodes...))
	c.since = append(c.since, sinceUnix)
	return c.verdict, c.err
}

func (c *fakeCanaryChecker) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

func withCanaryChecker(c change.CanaryHealthChecker) func(*change.Config) {
	return func(cfg *change.Config) { cfg.Canary = c }
}

// canaryOps builds one bridge.create per node of the three-node fixture, so
// the plan has a stage/reload pair per node and there is something to stage a
// canary of.
func canaryOps() []change.Op {
	return []change.Op{
		bridgeCreateOp("pve1", "vmbr90", nil),
		bridgeCreateOp("pve2", "vmbr90", nil),
		bridgeCreateOp("pve3", "vmbr90", nil),
	}
}

const (
	canaryNode = "pve1"
)

var restNodes = []string{"pve2", "pve3"}

// canaryStrategy is the strategy under test: stage pve1, hold, wait for a
// manual continue.
func canaryStrategy(gate change.ApplyGate, holdSec int) change.ApplyStrategy {
	return change.ApplyStrategy{
		Mode: change.ApplyModeCanary, CanaryNodes: []string{canaryNode},
		HoldForSec: holdSec, Gate: gate,
	}
}

// stageRow reads the persisted staged-apply row directly from the store —
// deliberately not through the service, so "the store records exactly which
// nodes are in which state" is asserted against what is actually on disk.
func stageRow(t *testing.T, h *applyHarness, id string) (store.ChangesetApplyStage, bool) {
	t.Helper()
	row, err := store.NewChangesetStageRepo(h.db).Get(context.Background(), id)
	if errors.Is(err, store.ErrNotFound) {
		return store.ChangesetApplyStage{}, false
	}
	if err != nil {
		t.Fatalf("reading staged-apply row: %v", err)
	}
	return row, true
}

func changesetAudit(t *testing.T, h *applyHarness, id string) []store.AuditEntry {
	t.Helper()
	entries, err := h.auditRepo.List(context.Background(), id, 500)
	if err != nil {
		t.Fatalf("listing audit entries: %v", err)
	}
	return entries
}

// preApplyFile reads node's committed interfaces content through the agent
// and returns it — the restore baseline the abort/expiry tests compare
// against. It must be called BEFORE the apply: the fake agent seeds a node's
// committed content lazily on first read, so asking it "what was on this
// node" without having read it once returns the empty string, and an
// "unchanged after restore" assertion against that baseline would pass for
// the wrong reason.
func preApplyFile(t *testing.T, h *applyHarness, node string) string {
	t.Helper()
	content, err := h.agent.ReadInterfaces(context.Background(), node)
	if err != nil {
		t.Fatalf("reading %s's pre-apply interfaces file: %v", node, err)
	}
	if content == "" {
		t.Fatalf("%s's pre-apply interfaces file is empty; there would be nothing to prove a restore against", node)
	}
	return content
}

// --- AC1 ------------------------------------------------------------------

// TestCanaryApply_TouchesOnlyTheCanaryNodes is acceptance criterion 1.
//
// The control leg runs the SAME changeset with the default (all-at-once)
// strategy against the SAME spy and asserts pve2/pve3 were written to. If it
// ever stops doing so — a broken spy, a renamed node, a plan that no longer
// reaches those nodes — it fails with a message saying, in as many words,
// that the canary leg's zero-write assertion would prove nothing.
func TestCanaryApply_TouchesOnlyTheCanaryNodes(t *testing.T) {
	ctx := context.Background()

	t.Run("control: an all-at-once apply does write to the non-canary nodes", func(t *testing.T) {
		h := newHarness(t, fixtureThreeNode)
		cs := h.mustCreate(t, "brian", "control", canaryOps())
		if _, err := h.svc.Apply(ctx, cs.ID, "brian", nil, 0); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		for _, node := range restNodes {
			if got := h.agent.writeCallsFor(node); len(got) == 0 {
				t.Fatalf("control: an ordinary apply of this changeset recorded NO write calls on %s (%v) — "+
					"the spy therefore counts nothing on the non-canary nodes, and the zero-write assertion "+
					"in the canary leg below would prove nothing", node, h.agent.callsFor(node))
			}
		}
	})

	t.Run("canary: only the canary node is written to", func(t *testing.T) {
		h := newHarness(t, fixtureThreeNode)
		cs := h.mustCreate(t, "brian", "canary", canaryOps())

		got, err := h.svc.ApplyStaged(ctx, cs.ID, "brian", nil, 60*time.Second, canaryStrategy(change.ApplyGateManual, 30))
		if err != nil {
			t.Fatalf("ApplyStaged: %v", err)
		}

		// The canary node ran its whole stage->reload pair.
		wantCanary := []string{"StageInterfaces", "ReloadInterfaces"}
		if diff := fmt.Sprint(h.agent.writeCallsFor(canaryNode)); diff != fmt.Sprint(wantCanary) {
			t.Errorf("%s write calls = %v, want %v", canaryNode, h.agent.writeCallsFor(canaryNode), wantCanary)
		}

		// AC1: the remaining nodes' clients recorded zero calls. Their ONLY
		// permitted entry is the pre-apply snapshot's read, which happens for
		// the whole changeset before any stage runs and is the rollback
		// source for the nodes that do get applied later.
		for _, node := range restNodes {
			if writes := h.agent.writeCallsFor(node); len(writes) != 0 {
				t.Errorf("%s recorded %v during the canary stage, want none — the canary stage must touch only the canary nodes", node, writes)
			}
			for _, call := range h.agent.callsFor(node) {
				if call != "ReadInterfaces" {
					t.Errorf("%s recorded a %s call during the canary stage; only the pre-apply snapshot read is permitted", node, call)
				}
			}
		}

		// The sequence is paused, not finished: neither applied nor rolled back.
		if got.Status != change.StatusApplying {
			t.Errorf("status = %q, want %q (a paused staged apply is neither applied nor rolled back)", got.Status, change.StatusApplying)
		}
		row, ok := stageRow(t, h, cs.ID)
		if !ok {
			t.Fatal("no staged-apply row persisted; the pause must be recorded in the store, not in memory")
		}
		if row.State != store.StageCanaryHold {
			t.Errorf("stage state = %q, want %q", row.State, store.StageCanaryHold)
		}
		if row.AppliedNodesJSON != `["pve1"]` || row.PendingNodesJSON != `["pve2","pve3"]` {
			t.Errorf("stage row nodes = applied %s / pending %s, want [\"pve1\"] / [\"pve2\",\"pve3\"] — "+
				"the store must record exactly which nodes are in which state", row.AppliedNodesJSON, row.PendingNodesJSON)
		}
		if !hasAuditResult(changesetAudit(t, h, cs.ID), "changeset.apply", "canary_hold") {
			t.Error("no changeset.apply/canary_hold audit entry for the pause")
		}
	})
}

// TestCanaryApply_ContinuePromotesTheRest is the manual gate's happy path:
// the remaining nodes are applied only once the operator continues, and the
// sequence then enters the ordinary commit-confirm window with the deadline
// it started with.
func TestCanaryApply_ContinuePromotesTheRest(t *testing.T) {
	ctx := context.Background()
	clock := newSettableClock()
	h := newHarness(t, fixtureThreeNode, withClock(clock))
	cs := h.mustCreate(t, "brian", "canary", canaryOps())

	held, err := h.svc.ApplyStaged(ctx, cs.ID, "brian", nil, 120*time.Second, canaryStrategy(change.ApplyGateManual, 30))
	if err != nil {
		t.Fatalf("ApplyStaged: %v", err)
	}
	if held.ConfirmDeadline == nil {
		t.Fatal("a paused staged apply must publish the commit-confirm deadline it is already running against")
	}
	wantDeadline := *held.ConfirmDeadline

	clock.advance(20 * time.Second)
	promoted, err := h.svc.ContinueStagedApply(ctx, cs.ID, "brian", nil)
	if err != nil {
		t.Fatalf("ContinueStagedApply: %v", err)
	}
	if promoted.Status != change.StatusAwaitingConfirm {
		t.Fatalf("status after continue = %q, want %q", promoted.Status, change.StatusAwaitingConfirm)
	}
	if promoted.ConfirmDeadline == nil || *promoted.ConfirmDeadline != wantDeadline {
		t.Errorf("confirm deadline after continue = %v, want %d — the deadline covers the WHOLE staged sequence and must not be restarted by promoting",
			promoted.ConfirmDeadline, wantDeadline)
	}
	for _, node := range restNodes {
		want := []string{"StageInterfaces", "ReloadInterfaces"}
		if fmt.Sprint(h.agent.writeCallsFor(node)) != fmt.Sprint(want) {
			t.Errorf("%s write calls after continue = %v, want %v", node, h.agent.writeCallsFor(node), want)
		}
	}
	if _, ok := stageRow(t, h, cs.ID); ok {
		t.Error("the staged-apply row must be cleared once the sequence is promoted")
	}

	// One apply log for the whole sequence, with every step OK.
	log := h.applyLog(t, cs.ID)
	for _, st := range log.Steps {
		if st.Status != change.StepOK {
			t.Errorf("step %d (%s) status = %q, want %q — a promoted sequence must report ONE complete apply log",
				st.Index, st.Kind, st.Status, change.StepOK)
		}
	}
	if _, err := h.svc.ContinueStagedApply(ctx, cs.ID, "brian", nil); err == nil {
		t.Error("continuing an already-promoted changeset must be refused")
	}
}

// --- AC2 ------------------------------------------------------------------

// TestCanaryApply_AbortRestoresOnlyTheStagesThatRan is acceptance criterion 2.
//
// The control leg aborts an ALL-at-once apply through the same rollback
// route and asserts the non-canary nodes were contacted by it — proving the
// rollback path does reach those nodes when it should, so the canary leg's
// "contacted no others" assertion is a real constraint rather than a rollback
// that never contacts anything.
func TestCanaryApply_AbortRestoresOnlyTheStagesThatRan(t *testing.T) {
	ctx := context.Background()

	t.Run("control: rolling back an all-at-once apply does contact every node", func(t *testing.T) {
		h := newHarness(t, fixtureThreeNode)
		cs := h.mustCreate(t, "brian", "control", canaryOps())
		if _, err := h.svc.Apply(ctx, cs.ID, "brian", nil, 0); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		before := map[string]int{}
		for _, node := range restNodes {
			before[node] = len(h.agent.callsFor(node))
		}
		if _, err := h.svc.Rollback(ctx, cs.ID, "brian", nil); err != nil {
			t.Fatalf("Rollback: %v", err)
		}
		for _, node := range restNodes {
			if len(h.agent.callsFor(node))-before[node] == 0 {
				t.Fatalf("control: rolling back an ordinary apply contacted %s zero times — the rollback path "+
					"therefore contacts nothing on these nodes, and the canary leg's zero-contact assertion below would prove nothing", node)
			}
		}
	})

	t.Run("canary: abort restores the canary node and contacts no others", func(t *testing.T) {
		h := newHarness(t, fixtureThreeNode)
		cs := h.mustCreate(t, "brian", "canary", canaryOps())
		preCanary := preApplyFile(t, h, canaryNode)

		if _, err := h.svc.ApplyStaged(ctx, cs.ID, "brian", nil, 120*time.Second, canaryStrategy(change.ApplyGateManual, 30)); err != nil {
			t.Fatalf("ApplyStaged: %v", err)
		}
		if h.agent.committedFile(canaryNode) == preCanary {
			t.Fatal("the canary node's committed file is unchanged after the canary stage; there is nothing for the abort to restore")
		}

		// Snapshot the spy immediately before the abort so what follows is a
		// delta over the abort alone, not over the whole test.
		before := map[string]int{}
		for _, node := range append([]string{canaryNode}, restNodes...) {
			before[node] = len(h.agent.callsFor(node))
		}

		got, err := h.svc.Rollback(ctx, cs.ID, "brian", nil)
		if err != nil {
			t.Fatalf("Rollback (abort): %v", err)
		}
		if got.Status != change.StatusRolledBack {
			t.Fatalf("status after abort = %q, want %q", got.Status, change.StatusRolledBack)
		}
		if h.agent.committedFile(canaryNode) != preCanary {
			t.Error("the canary node was not restored to its pre-apply file")
		}
		if delta := len(h.agent.callsFor(canaryNode)) - before[canaryNode]; delta == 0 {
			t.Error("the abort contacted the canary node zero times, so it cannot have restored it")
		}
		for _, node := range restNodes {
			if delta := len(h.agent.callsFor(node)) - before[node]; delta != 0 {
				t.Errorf("the abort made %d call(s) on %s (%v) — a node that was never touched must never be restored",
					delta, node, h.agent.callsFor(node)[before[node]:])
			}
		}
		if _, ok := stageRow(t, h, cs.ID); ok {
			t.Error("the staged-apply row must be cleared once the sequence is aborted")
		}
		if !hasAuditResult(changesetAudit(t, h, cs.ID), "changeset.abort", "rolled_back") {
			t.Error("no changeset.abort audit entry naming the outcome of the abort")
		}
	})
}

// TestCanaryApply_CanaryStageFailureRollsBackOnlyTheCanary proves the same
// containment for the OTHER way a canary stage can end: the canary node's own
// step failing. The untouched nodes must not even have their (never-written)
// staged file discarded.
func TestCanaryApply_CanaryStageFailureRollsBackOnlyTheCanary(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, fixtureThreeNode)
	cs := h.mustCreate(t, "brian", "canary", canaryOps())
	h.agent.setFailStage(canaryNode, true)

	_, err := h.svc.ApplyStaged(ctx, cs.ID, "brian", nil, 120*time.Second, canaryStrategy(change.ApplyGateManual, 30))
	if err == nil {
		t.Fatal("ApplyStaged: want an error when the canary stage fails")
	}
	if got := h.get(t, cs.ID); got.Status != change.StatusFailed {
		t.Errorf("status = %q, want %q", got.Status, change.StatusFailed)
	}
	for _, node := range restNodes {
		if writes := h.agent.writeCallsFor(node); len(writes) != 0 {
			t.Errorf("%s recorded %v after a canary-stage failure, want none — including no DiscardStaged of a file that was never staged there", node, writes)
		}
	}
	if _, ok := stageRow(t, h, cs.ID); ok {
		t.Error("a failed canary stage must leave no staged-apply row behind")
	}
}

// --- AC3 ------------------------------------------------------------------

// TestCanaryApply_AutoGate is acceptance criterion 3, both directions: a
// clean hold promotes, and an `error` finding attributable to a canary node
// during the hold does not.
func TestCanaryApply_AutoGate(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name          string
		wantStatus    change.Status
		wantAudit     string
		verdict       change.CanaryVerdict
		wantRestWrite bool
	}{
		{
			name:          "clean hold promotes",
			verdict:       change.CanaryVerdict{Healthy: true},
			wantStatus:    change.StatusAwaitingConfirm,
			wantRestWrite: true,
			wantAudit:     "canary_gate_passed",
		},
		{
			name: "an error finding on a canary node does not promote",
			verdict: change.CanaryVerdict{
				Healthy:  true, // healthy, but a new error finding landed: either alone must block
				Findings: []string{"drift|pve1|mtu_consistency"},
				Reason:   "a new error finding appeared on the canary node during the hold",
			},
			wantStatus:    change.StatusRolledBack,
			wantRestWrite: false,
			wantAudit:     "canary_gate_failed",
		},
		{
			name:          "an unhealthy canary does not promote",
			verdict:       change.CanaryVerdict{Healthy: false, Reason: "pve1 did not come back after the reload"},
			wantStatus:    change.StatusRolledBack,
			wantRestWrite: false,
			wantAudit:     "canary_gate_failed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checker := &fakeCanaryChecker{verdict: tc.verdict}
			h := newHarness(t, fixtureThreeNode, withCanaryChecker(checker))
			cs := h.mustCreate(t, "brian", "canary", canaryOps())

			if _, err := h.svc.ApplyStaged(ctx, cs.ID, "brian", nil, 120*time.Second, canaryStrategy(change.ApplyGateAuto, 30)); err != nil {
				t.Fatalf("ApplyStaged: %v", err)
			}
			if checker.callCount() != 0 {
				t.Fatalf("the canary checker was consulted %d time(s) before the hold even elapsed", checker.callCount())
			}

			h.timers.fireLatest(t) // the hold deadline

			if checker.callCount() != 1 {
				t.Fatalf("the canary checker was consulted %d time(s) at the hold deadline, want exactly 1 — "+
					"an auto gate that reaches a verdict without asking is not a gate", checker.callCount())
			}
			if got := checker.calls[0]; len(got) != 1 || got[0] != canaryNode {
				t.Errorf("the checker was asked about %v, want only the canary node [%s]", got, canaryNode)
			}

			if got := h.get(t, cs.ID); got.Status != tc.wantStatus {
				t.Fatalf("status after the hold = %q, want %q", got.Status, tc.wantStatus)
			}
			for _, node := range restNodes {
				wrote := len(h.agent.writeCallsFor(node)) > 0
				if wrote != tc.wantRestWrite {
					t.Errorf("%s written = %v, want %v (calls: %v)", node, wrote, tc.wantRestWrite, h.agent.callsFor(node))
				}
			}
			if !hasAuditResult(changesetAudit(t, h, cs.ID), "changeset.apply", tc.wantAudit) {
				t.Errorf("no changeset.apply/%s audit entry", tc.wantAudit)
			}
			if _, ok := stageRow(t, h, cs.ID); ok {
				t.Error("the staged-apply row must be cleared once the gate has decided")
			}
		})
	}
}

// TestCanaryApply_AutoGateFailsClosedOnAnUnreadableChecker proves the auto
// gate treats "we could not assess the canary" as "do not promote". An
// unassessable canary is not a proof of safety.
func TestCanaryApply_AutoGateFailsClosedOnAnUnreadableChecker(t *testing.T) {
	ctx := context.Background()
	checker := &fakeCanaryChecker{err: errors.New("findings engine unavailable")}
	h := newHarness(t, fixtureThreeNode, withCanaryChecker(checker))
	cs := h.mustCreate(t, "brian", "canary", canaryOps())

	if _, err := h.svc.ApplyStaged(ctx, cs.ID, "brian", nil, 120*time.Second, canaryStrategy(change.ApplyGateAuto, 30)); err != nil {
		t.Fatalf("ApplyStaged: %v", err)
	}
	h.timers.fireLatest(t)

	if got := h.get(t, cs.ID); got.Status != change.StatusRolledBack {
		t.Errorf("status = %q, want %q — an unassessable canary must not promote", got.Status, change.StatusRolledBack)
	}
	for _, node := range restNodes {
		if writes := h.agent.writeCallsFor(node); len(writes) != 0 {
			t.Errorf("%s was written to (%v) despite an unassessable canary", node, writes)
		}
	}
}

// TestCanaryApply_ManualGateAbortsWhenNobodyContinues proves the manual gate
// is a bounded wait, not an open-ended one.
func TestCanaryApply_ManualGateAbortsWhenNobodyContinues(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, fixtureThreeNode)
	cs := h.mustCreate(t, "brian", "canary", canaryOps())

	if _, err := h.svc.ApplyStaged(ctx, cs.ID, "brian", nil, 120*time.Second, canaryStrategy(change.ApplyGateManual, 30)); err != nil {
		t.Fatalf("ApplyStaged: %v", err)
	}
	h.timers.fireLatest(t)

	if got := h.get(t, cs.ID); got.Status != change.StatusRolledBack {
		t.Errorf("status = %q, want %q", got.Status, change.StatusRolledBack)
	}
	for _, node := range restNodes {
		if writes := h.agent.writeCallsFor(node); len(writes) != 0 {
			t.Errorf("%s was written to (%v) even though the hold was never continued", node, writes)
		}
	}
}

// --- AC4 ------------------------------------------------------------------

// TestCanaryApply_RecoversAcrossADaemonRestart is acceptance criterion 4: the
// paused state is recovered from the STORE, and the sequence either resumes
// or rolls back per the recorded strategy. It is never left in a state
// nothing can describe.
func TestCanaryApply_RecoversAcrossADaemonRestart(t *testing.T) {
	ctx := context.Background()

	t.Run("a hold still inside its deadline resumes", func(t *testing.T) {
		clock := newSettableClock()
		h := newHarness(t, fixtureThreeNode, withClock(clock))
		cs := h.mustCreate(t, "brian", "canary", canaryOps())
		if _, err := h.svc.ApplyStaged(ctx, cs.ID, "brian", nil, 120*time.Second, canaryStrategy(change.ApplyGateManual, 60)); err != nil {
			t.Fatalf("ApplyStaged: %v", err)
		}

		clock.advance(10 * time.Second) // still well inside both deadlines
		h.restart(t)

		got := h.get(t, cs.ID)
		if got.Status != change.StatusApplying {
			t.Fatalf("status after restart = %q, want %q — a live hold must survive the restart", got.Status, change.StatusApplying)
		}
		row, ok := stageRow(t, h, cs.ID)
		if !ok || row.State != store.StageCanaryHold {
			t.Fatal("the staged-apply row must survive the restart intact")
		}
		if !hasAuditResult(changesetAudit(t, h, cs.ID), "changeset.recover", "canary_hold_resumed") {
			t.Error("no changeset.recover/canary_hold_resumed audit entry")
		}
		for _, node := range restNodes {
			if writes := h.agent.writeCallsFor(node); len(writes) != 0 {
				t.Errorf("%s was written to (%v) by the restart itself", node, writes)
			}
		}

		// The recovered service — a different *Service value — can drive the
		// sequence to completion, which is what "resumable" has to mean.
		promoted, err := h.svc.ContinueStagedApply(ctx, cs.ID, "brian", nil)
		if err != nil {
			t.Fatalf("ContinueStagedApply after restart: %v", err)
		}
		if promoted.Status != change.StatusAwaitingConfirm {
			t.Errorf("status after continuing a recovered hold = %q, want %q", promoted.Status, change.StatusAwaitingConfirm)
		}
	})

	t.Run("a hold whose window elapsed while the daemon was down rolls back", func(t *testing.T) {
		clock := newSettableClock()
		h := newHarness(t, fixtureThreeNode, withClock(clock))
		cs := h.mustCreate(t, "brian", "canary", canaryOps())
		preCanary := preApplyFile(t, h, canaryNode)
		if _, err := h.svc.ApplyStaged(ctx, cs.ID, "brian", nil, 120*time.Second, canaryStrategy(change.ApplyGateManual, 60)); err != nil {
			t.Fatalf("ApplyStaged: %v", err)
		}

		clock.advance(10 * time.Minute) // past both the hold and the confirm window
		h.restart(t)

		got := h.get(t, cs.ID)
		if got.Status != change.StatusRolledBack {
			t.Fatalf("status after restart = %q, want %q — an expired hold must be resolved, not left pending", got.Status, change.StatusRolledBack)
		}
		if h.agent.committedFile(canaryNode) != preCanary {
			t.Error("the canary node was not restored by the restart recovery")
		}
		for _, node := range restNodes {
			if writes := h.agent.writeCallsFor(node); len(writes) != 0 {
				t.Errorf("%s was written to (%v) during recovery of a hold that never reached it", node, writes)
			}
		}
		if _, ok := stageRow(t, h, cs.ID); ok {
			t.Error("the staged-apply row must be cleared once recovery has resolved the hold")
		}
	})
}

// --- AC5 ------------------------------------------------------------------

// TestCanaryApply_ConfirmDeadlineCoversTheWholeSequence is acceptance
// criterion 5: the commit-confirm deadline is measured from the start of the
// whole sequence, and a hold that outlasts the window rolls back everything
// applied so far.
func TestCanaryApply_ConfirmDeadlineCoversTheWholeSequence(t *testing.T) {
	ctx := context.Background()
	clock := newSettableClock()
	// gate: auto, so the ONLY thing that can end this hold un-promoted is the
	// window running out — a manual gate would abort on its own timing and
	// the test would pass for the wrong reason.
	checker := &fakeCanaryChecker{verdict: change.CanaryVerdict{Healthy: true}}
	h := newHarness(t, fixtureThreeNode, withClock(clock), withCanaryChecker(checker))
	cs := h.mustCreate(t, "brian", "canary", canaryOps())
	preCanary := preApplyFile(t, h, canaryNode)

	start := clock.Now().Unix()
	held, err := h.svc.ApplyStaged(ctx, cs.ID, "brian", nil, 60*time.Second, canaryStrategy(change.ApplyGateAuto, 30))
	if err != nil {
		t.Fatalf("ApplyStaged: %v", err)
	}
	if held.ConfirmDeadline == nil || *held.ConfirmDeadline != start+60 {
		t.Fatalf("confirm deadline = %v, want %d — it must be measured from the start of the whole sequence",
			held.ConfirmDeadline, start+60)
	}

	// The window runs out while the sequence is still paused.
	clock.advance(90 * time.Second)
	h.timers.fireLatest(t)

	if checker.callCount() != 0 {
		t.Errorf("the auto gate consulted the canary checker %d time(s) after the window had already expired; "+
			"an expired window is not a promotion decision to make", checker.callCount())
	}
	got := h.get(t, cs.ID)
	if got.Status != change.StatusRolledBack {
		t.Fatalf("status = %q, want %q — a hold that outlasts the window must roll back everything applied so far", got.Status, change.StatusRolledBack)
	}
	if h.agent.committedFile(canaryNode) != preCanary {
		t.Error("the canary node was not restored when the window expired")
	}
	for _, node := range restNodes {
		if writes := h.agent.writeCallsFor(node); len(writes) != 0 {
			t.Errorf("%s was written to (%v) by the expiry path", node, writes)
		}
	}
}

// TestCanaryApply_HoldDeadlineNeverOutlivesTheWindow pins the arithmetic AC5
// rests on: the recorded hold deadline is always at or before the recorded
// commit-confirm deadline, for every accepted hold length.
func TestCanaryApply_HoldDeadlineNeverOutlivesTheWindow(t *testing.T) {
	ctx := context.Background()
	for _, holdSec := range []int{10, 30, 119} {
		t.Run(fmt.Sprintf("hold=%ds", holdSec), func(t *testing.T) {
			clock := newSettableClock()
			h := newHarness(t, fixtureThreeNode, withClock(clock))
			cs := h.mustCreate(t, "brian", "canary", canaryOps())
			if _, err := h.svc.ApplyStaged(ctx, cs.ID, "brian", nil, 120*time.Second, canaryStrategy(change.ApplyGateManual, holdSec)); err != nil {
				t.Fatalf("ApplyStaged: %v", err)
			}
			row, ok := stageRow(t, h, cs.ID)
			if !ok {
				t.Fatal("no staged-apply row")
			}
			if row.HoldDeadline > row.ConfirmDeadline {
				t.Errorf("hold deadline %d is after the confirm deadline %d — a hold may never keep the cluster open past the window",
					row.HoldDeadline, row.ConfirmDeadline)
			}
			if want := clock.Now().Unix() + int64(holdSec); row.HoldDeadline != want {
				t.Errorf("hold deadline = %d, want %d", row.HoldDeadline, want)
			}
		})
	}
}

// --- AC6 ------------------------------------------------------------------

// TestApplyStaged_ModeAllIsTheOrdinaryApply is acceptance criterion 6's
// in-package half: an explicit `mode: all` is byte-for-byte the ordinary
// apply — same status, same plan, same apply log, and NO staged-apply row.
// The load-bearing half is the pre-existing apply suite (apply_e2e_test.go,
// apply_failure_test.go, apply_internal_test.go and the rest), which runs
// unchanged against this branch: every one of those tests goes through
// Service.Apply, which is now ApplyStaged with the zero strategy.
func TestApplyStaged_ModeAllIsTheOrdinaryApply(t *testing.T) {
	ctx := context.Background()

	run := func(t *testing.T, strategy change.ApplyStrategy) (change.Changeset, change.ApplyLog, []string) {
		t.Helper()
		h := newHarness(t, fixtureThreeNode)
		cs := h.mustCreate(t, "brian", "same", canaryOps())
		var err error
		if strategy.Mode == "" {
			_, err = h.svc.Apply(ctx, cs.ID, "brian", nil, 120*time.Second)
		} else {
			_, err = h.svc.ApplyStaged(ctx, cs.ID, "brian", nil, 120*time.Second, strategy)
		}
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if _, ok := stageRow(t, h, cs.ID); ok {
			t.Error("an all-at-once apply must never write a staged-apply row")
		}
		var calls []string
		for _, node := range []string{"pve1", "pve2", "pve3"} {
			calls = append(calls, node+":"+fmt.Sprint(h.agent.writeCallsFor(node)))
		}
		return h.get(t, cs.ID), h.applyLog(t, cs.ID), calls
	}

	implicit, implicitLog, implicitCalls := run(t, change.ApplyStrategy{})
	explicit, explicitLog, explicitCalls := run(t, change.ApplyStrategy{Mode: change.ApplyModeAll})

	if implicit.Status != explicit.Status || implicit.Status != change.StatusAwaitingConfirm {
		t.Errorf("status: implicit %q vs explicit %q, want %q", implicit.Status, explicit.Status, change.StatusAwaitingConfirm)
	}
	if fmt.Sprint(implicitCalls) != fmt.Sprint(explicitCalls) {
		t.Errorf("node calls differ:\n implicit %v\n explicit %v", implicitCalls, explicitCalls)
	}
	// The apply logs differ only in timestamps, so compare their step shape.
	shape := func(l change.ApplyLog) string {
		var out string
		for _, st := range l.Steps {
			out += fmt.Sprintf("%d/%s/%s/%s;", st.Index, st.Kind, st.Node, st.Status)
		}
		return out
	}
	if shape(implicitLog) != shape(explicitLog) {
		t.Errorf("apply log shape differs:\n implicit %s\n explicit %s", shape(implicitLog), shape(explicitLog))
	}
	if len(implicitLog.Steps) != 6 {
		t.Errorf("expected 6 steps (stage+reload per node across three nodes), got %d", len(implicitLog.Steps))
	}
}

// --- AC7 and the rest of the strategy gate --------------------------------

// TestCanaryApply_StrategyValidation is acceptance criterion 7 plus every
// other way a strategy can be refused. Each case additionally asserts the
// changeset was left exactly where it was — no lock held, no mutation, no
// status change — because a refused strategy must cost nothing.
func TestCanaryApply_StrategyValidation(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name       string
		wantReason string
		ops        []change.Op
		strategy   change.ApplyStrategy
		noChecker  bool
	}{
		{
			name:       "a canary node the changeset does not affect",
			strategy:   change.ApplyStrategy{Mode: change.ApplyModeCanary, CanaryNodes: []string{"pve1", "pve9"}, HoldForSec: 30},
			wantReason: "does not affect",
		},
		{
			name:       "an empty canary list",
			strategy:   change.ApplyStrategy{Mode: change.ApplyModeCanary, HoldForSec: 30},
			wantReason: "non-empty canaryNodes",
		},
		{
			name:       "a canary list covering every affected node",
			strategy:   change.ApplyStrategy{Mode: change.ApplyModeCanary, CanaryNodes: []string{"pve1", "pve2", "pve3"}, HoldForSec: 30},
			wantReason: "no second stage",
		},
		{
			name:       "an unknown mode",
			strategy:   change.ApplyStrategy{Mode: "rolling"},
			wantReason: "unknown mode",
		},
		{
			name:       "an unknown gate",
			strategy:   change.ApplyStrategy{Mode: change.ApplyModeCanary, CanaryNodes: []string{"pve1"}, HoldForSec: 30, Gate: "eventually"},
			wantReason: "unknown gate",
		},
		{
			name:       "a hold shorter than the floor",
			strategy:   change.ApplyStrategy{Mode: change.ApplyModeCanary, CanaryNodes: []string{"pve1"}, HoldForSec: 1},
			wantReason: "holdForSec must be between",
		},
		{
			name:       "a hold that fills the confirm window",
			strategy:   change.ApplyStrategy{Mode: change.ApplyModeCanary, CanaryNodes: []string{"pve1"}, HoldForSec: 120},
			wantReason: "must be shorter than the commit-confirm window",
		},
		{
			name:       "mode all carrying canary fields",
			strategy:   change.ApplyStrategy{Mode: change.ApplyModeAll, CanaryNodes: []string{"pve1"}},
			wantReason: "takes no canaryNodes",
		},
		{
			name:       "gate auto with no health checker wired",
			strategy:   change.ApplyStrategy{Mode: change.ApplyModeCanary, CanaryNodes: []string{"pve1"}, HoldForSec: 30, Gate: change.ApplyGateAuto},
			noChecker:  true,
			wantReason: "canary health checker",
		},
		{
			name:     "a single-node changeset has nothing to stage",
			strategy: change.ApplyStrategy{Mode: change.ApplyModeCanary, CanaryNodes: []string{"pve1"}, HoldForSec: 30},
			ops: []change.Op{
				bridgeCreateOp("pve1", "vmbr90", nil),
			},
			wantReason: "at least two affected nodes",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := []func(*change.Config){}
			if !tc.noChecker {
				opts = append(opts, withCanaryChecker(&fakeCanaryChecker{verdict: change.CanaryVerdict{Healthy: true}}))
			}
			h := newHarness(t, fixtureThreeNode, opts...)
			ops := tc.ops
			if ops == nil {
				ops = canaryOps()
			}
			cs := h.mustCreate(t, "brian", "bad strategy", ops)

			_, err := h.svc.ApplyStaged(ctx, cs.ID, "brian", nil, 120*time.Second, tc.strategy)
			var bad *change.ErrInvalidApplyStrategy
			if !errors.As(err, &bad) {
				t.Fatalf("ApplyStaged = %v, want *change.ErrInvalidApplyStrategy", err)
			}
			if !contains(bad.Error(), tc.wantReason) {
				t.Errorf("error %q does not mention %q", bad.Error(), tc.wantReason)
			}

			// A refused strategy must leave the changeset untouched: still a
			// draft, no node written to, and — proven by applying it for real
			// afterwards — no apply lock stranded.
			if got := h.get(t, cs.ID); got.Status != change.StatusDraft {
				t.Errorf("status after a refused strategy = %q, want %q", got.Status, change.StatusDraft)
			}
			for _, node := range []string{"pve1", "pve2", "pve3"} {
				if writes := h.agent.writeCallsFor(node); len(writes) != 0 {
					t.Errorf("%s was written to (%v) by a refused strategy", node, writes)
				}
			}
			if _, err := h.svc.Apply(ctx, cs.ID, "brian", nil, 120*time.Second); err != nil {
				t.Errorf("an ordinary apply after a refused strategy failed (%v) — the refusal must not strand the apply lock", err)
			}
		})
	}
}

// TestCanaryApply_RefusedWithNoStageStore proves a deployment that cannot
// RECORD a pause refuses to create one, rather than silently downgrading to
// an all-at-once apply.
func TestCanaryApply_RefusedWithNoStageStore(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, fixtureThreeNode, func(cfg *change.Config) { cfg.Stages = nil })
	cs := h.mustCreate(t, "brian", "canary", canaryOps())

	_, err := h.svc.ApplyStaged(ctx, cs.ID, "brian", nil, 120*time.Second, canaryStrategy(change.ApplyGateManual, 30))
	var bad *change.ErrInvalidApplyStrategy
	if !errors.As(err, &bad) {
		t.Fatalf("ApplyStaged = %v, want *change.ErrInvalidApplyStrategy", err)
	}
	if !contains(bad.Error(), "staged apply is not configured") {
		t.Errorf("error %q does not explain that staged apply is unconfigured", bad.Error())
	}
	for _, node := range []string{"pve1", "pve2", "pve3"} {
		if writes := h.agent.writeCallsFor(node); len(writes) != 0 {
			t.Errorf("%s was written to (%v) despite the refusal", node, writes)
		}
	}
}

// TestCanaryApply_ContinueRefusedWhenNotPaused pins the resumability
// precondition: continuing anything that is not paused between stages is a
// refusal, not a second apply.
func TestCanaryApply_ContinueRefusedWhenNotPaused(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, fixtureThreeNode)
	cs := h.mustCreate(t, "brian", "ordinary", canaryOps())

	if _, err := h.svc.ContinueStagedApply(ctx, cs.ID, "brian", nil); err == nil {
		t.Error("continuing a draft must be refused")
	}
	if _, err := h.svc.Apply(ctx, cs.ID, "brian", nil, 120*time.Second); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	_, err := h.svc.ContinueStagedApply(ctx, cs.ID, "brian", nil)
	var notResumable *change.ErrNotResumable
	if !errors.As(err, &notResumable) {
		t.Errorf("continuing an awaiting_confirm changeset = %v, want *change.ErrNotResumable", err)
	}
}

// TestCanaryApply_StagedStateIsReadable proves the read model T-2603 will
// consult reports the same thing the store holds.
func TestCanaryApply_StagedStateIsReadable(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, fixtureThreeNode)
	cs := h.mustCreate(t, "brian", "canary", canaryOps())

	if _, _, err := h.svc.StagedApplyState(ctx, cs.ID); err != nil {
		t.Fatalf("StagedApplyState on a draft: %v", err)
	}
	if _, paused, _ := h.svc.StagedApplyState(ctx, cs.ID); paused {
		t.Error("a draft must not report a staged pause")
	}

	if _, err := h.svc.ApplyStaged(ctx, cs.ID, "brian", nil, 120*time.Second, canaryStrategy(change.ApplyGateManual, 30)); err != nil {
		t.Fatalf("ApplyStaged: %v", err)
	}
	state, paused, err := h.svc.StagedApplyState(ctx, cs.ID)
	if err != nil || !paused {
		t.Fatalf("StagedApplyState = (%+v, %v, %v), want a paused state", state, paused, err)
	}
	if state.State != store.StageCanaryHold {
		t.Errorf("state = %q, want %q", state.State, store.StageCanaryHold)
	}
	if fmt.Sprint(state.AppliedNodes) != "[pve1]" || fmt.Sprint(state.PendingNodes) != "[pve2 pve3]" {
		t.Errorf("applied=%v pending=%v, want [pve1] / [pve2 pve3]", state.AppliedNodes, state.PendingNodes)
	}
	if state.Strategy.Mode != change.ApplyModeCanary || state.Strategy.Gate != change.ApplyGateManual {
		t.Errorf("recorded strategy = %+v, want the canary/manual strategy that was requested", state.Strategy)
	}
	if state.HoldDeadline > state.ConfirmDeadline {
		t.Errorf("hold deadline %d is after the confirm deadline %d", state.HoldDeadline, state.ConfirmDeadline)
	}

	// The read model must agree with what is actually on disk.
	row, ok := stageRow(t, h, cs.ID)
	if !ok {
		t.Fatal("no staged-apply row")
	}
	var onDisk change.ApplyStrategy
	if err := json.Unmarshal([]byte(row.StrategyJSON), &onDisk); err != nil {
		t.Fatalf("decoding the stored strategy: %v", err)
	}
	if onDisk.Mode != state.Strategy.Mode || fmt.Sprint(onDisk.CanaryNodes) != fmt.Sprint(state.Strategy.CanaryNodes) {
		t.Errorf("stored strategy %+v disagrees with the read model %+v", onDisk, state.Strategy)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
