package change_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

// Acceptance criterion 4: a second apply while one changeset is
// applying/awaiting_confirm returns 409 changeset_locked.
func TestApply_LockContention(t *testing.T) {
	h := newHarness(t, fixtureSingleNode)
	ctx := context.Background()

	a := h.mustCreate(t, "root@pam", "A", []change.Op{bridgeCreateOp("pve1", "vmbr1", nil)})
	if _, err := h.svc.Apply(ctx, a.ID, "root@pam", nil, 0); err != nil {
		t.Fatalf("apply A: %v", err)
	}

	b := h.mustCreate(t, "root@pam", "B", []change.Op{bridgeCreateOp("pve1", "vmbr2", nil)})
	_, err := h.svc.Apply(ctx, b.ID, "root@pam", nil, 0)
	var locked *change.ErrChangesetLocked
	if !errors.As(err, &locked) {
		t.Fatalf("apply B error = %v, want *ErrChangesetLocked", err)
	}
	if locked.HeldBy != a.ID {
		t.Fatalf("lock held by %s, want %s", locked.HeldBy, a.ID)
	}

	// After A confirms, B can apply.
	if _, err := h.svc.Confirm(ctx, a.ID, "root@pam"); err != nil {
		t.Fatalf("confirm A: %v", err)
	}
	if _, err := h.svc.Apply(ctx, b.ID, "root@pam", nil, 0); err != nil {
		t.Fatalf("apply B after confirm: %v", err)
	}
}

// Acceptance criterion 5: manual rollback of a committed changeset creates a
// restoring draft whose diff is the exact inverse (the create becomes a
// delete that removes what was added), leaving the committed changeset intact.
func TestRollback_CommittedCreatesRestoringDraft(t *testing.T) {
	h := newHarness(t, fixtureSingleNode)
	ctx := context.Background()

	orig := h.mustCreate(t, "root@pam", "add vmbr3", []change.Op{bridgeCreateOp("pve1", "vmbr3", nil)})
	if _, err := h.svc.Apply(ctx, orig.ID, "root@pam", nil, 0); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := h.svc.Confirm(ctx, orig.ID, "root@pam"); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	draft, err := h.svc.Rollback(ctx, orig.ID, "root@pam", nil)
	if err != nil {
		t.Fatalf("rollback committed: %v", err)
	}
	if draft.ID == orig.ID {
		t.Fatal("restoring draft must be a new changeset, not the committed one")
	}
	if draft.Status != change.StatusDraft {
		t.Fatalf("restoring changeset status = %s, want draft", draft.Status)
	}
	if len(draft.Ops) != 1 || draft.Ops[0].Type != change.OpBridgeDelete {
		t.Fatalf("restoring ops = %+v, want a single bridge.delete", draft.Ops)
	}
	if draft.Ops[0].Target.ID != "vmbr3" {
		t.Fatalf("restoring op target = %s, want vmbr3", draft.Ops[0].Target.ID)
	}

	// The committed changeset is untouched.
	committed := h.get(t, orig.ID)
	if committed.Status != change.StatusCommitted {
		t.Fatalf("original status = %s, want committed", committed.Status)
	}

	// The restoring draft's diff removes vmbr3 (the exact inverse of the
	// original diff, which added it).
	diff, err := h.svc.Diff(ctx, draft.ID)
	if err != nil {
		t.Fatalf("diff restoring draft: %v", err)
	}
	if len(diff.Files) != 1 || !diff.Files[0].Changed {
		t.Fatalf("restoring diff has no file change: %+v", diff.Files)
	}
	if !strings.Contains(diff.Files[0].Unified, "vmbr3") || !removesLine(diff.Files[0].Unified, "vmbr3") {
		t.Fatalf("restoring diff does not remove vmbr3:\n%s", diff.Files[0].Unified)
	}
}

// removesLine reports whether the unified diff contains a removal ('-') line
// mentioning substr.
func removesLine(unified, substr string) bool {
	for _, line := range strings.Split(unified, "\n") {
		if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") && strings.Contains(line, substr) {
			return true
		}
	}
	return false
}

// Audit phase-2 F-10: manual rollback of a committed changeset is offered
// for the rollback window only (docs/features/change-management.md §4:
// "offered for 7 days"); beyond it, Rollback refuses with a typed
// *ErrRollbackWindowExpired instead of building a restoring draft whose
// pre-apply snapshot retention may already have pruned.
func TestRollback_CommittedWindowExpired(t *testing.T) {
	h := newHarness(t, fixtureSingleNode)
	ctx := context.Background()

	cs := h.mustCreate(t, "root@pam", "add vmbrW", []change.Op{bridgeCreateOp("pve1", "vmbrW", nil)})
	if _, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 0); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := h.svc.Confirm(ctx, cs.ID, "root@pam"); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	// Time-travel: age the commit past the 7-day window (updated_at is the
	// commit timestamp — Transition stamps it).
	aged := time.Now().Add(-8 * 24 * time.Hour).Unix()
	if _, err := h.db.Conn().ExecContext(ctx, "UPDATE changesets SET updated_at = ? WHERE id = ?", aged, cs.ID); err != nil {
		t.Fatalf("aging changeset: %v", err)
	}

	var expired *change.ErrRollbackWindowExpired
	if _, err := h.svc.Rollback(ctx, cs.ID, "root@pam", nil); !errors.As(err, &expired) {
		t.Fatalf("rollback of aged committed changeset err = %v, want *ErrRollbackWindowExpired", err)
	}
	if expired.WindowDays != change.DefaultRollbackWindowDays {
		t.Fatalf("windowDays = %d, want %d", expired.WindowDays, change.DefaultRollbackWindowDays)
	}

	// The committed changeset itself is untouched.
	if got := h.get(t, cs.ID); got.Status != change.StatusCommitted {
		t.Fatalf("status after refused rollback = %s, want committed", got.Status)
	}
}

// Manual rollback of an in-window (awaiting_confirm) changeset restores state
// and attributes the rollback to the acting user (not system:rollback).
func TestRollback_AwaitingConfirm_Manual(t *testing.T) {
	h := newHarness(t, fixtureSingleNode)
	ctx := context.Background()
	before := mustRead(t, h, "pve1")

	cs := h.mustCreate(t, "erin@pve", "add vmbr4", []change.Op{bridgeCreateOp("pve1", "vmbr4", nil)})
	if _, err := h.svc.Apply(ctx, cs.ID, "erin@pve", nil, 0); err != nil {
		t.Fatalf("apply: %v", err)
	}
	rolled, err := h.svc.Rollback(ctx, cs.ID, "erin@pve", nil)
	if err != nil {
		t.Fatalf("manual rollback: %v", err)
	}
	if rolled.Status != change.StatusRolledBack {
		t.Fatalf("status = %s, want rolled_back", rolled.Status)
	}
	if h.agent.committedFile("pve1") != before {
		t.Fatal("file not restored on manual rollback")
	}
	log := h.applyLog(t, cs.ID)
	if log.RolledBackBy != "erin@pve" {
		t.Fatalf("rolledBackBy = %q, want erin@pve", log.RolledBackBy)
	}
	if h.timers.armedCount() != 0 {
		t.Fatal("timer still armed after manual rollback")
	}
}

// --- error-path and edge-case unit tests (coverage) -----------------------

func TestApply_NotConfigured(t *testing.T) {
	db := openTestDB(t)
	svc := newService(t, change.Config{Changesets: store.NewChangesetRepo(db), Audit: store.NewAuditRepo(db)})
	cs, err := svc.Create(context.Background(), "root@pam", "x", []change.Op{bridgeCreateOp("pve1", "vmbr1", nil)})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var notConf *change.ErrApplyNotConfigured
	if _, err := svc.Apply(context.Background(), cs.ID, "root@pam", nil, 0); !errors.As(err, &notConf) {
		t.Fatalf("Apply err = %v, want *ErrApplyNotConfigured", err)
	}
	if _, err := svc.Confirm(context.Background(), cs.ID, "root@pam"); !errors.As(err, &notConf) {
		t.Fatalf("Confirm err = %v, want *ErrApplyNotConfigured", err)
	}
	if _, err := svc.Rollback(context.Background(), cs.ID, "root@pam", nil); !errors.As(err, &notConf) {
		t.Fatalf("Rollback err = %v, want *ErrApplyNotConfigured", err)
	}
	if _, err := svc.Diff(context.Background(), cs.ID); !errors.As(err, &notConf) {
		t.Fatalf("Diff err = %v, want *ErrApplyNotConfigured", err)
	}
}

func TestBuildPlan_UnsupportedOp(t *testing.T) {
	// guest.nic.update is a valid draft op but outside the T-205 executable
	// set, so the planner rejects it before any mutation.
	op := change.Op{
		Type:   change.OpGuestNicUpdate,
		Target: inventory.Ref{Kind: inventory.KindGuestNic, Node: "pve1", ID: "100/net0"},
		Params: &change.GuestNicUpdateParams{},
	}
	var unsupp *change.ErrUnsupportedOp
	if _, err := change.BuildPlan([]change.Op{op}); !errors.As(err, &unsupp) {
		t.Fatalf("BuildPlan err = %v, want *ErrUnsupportedOp", err)
	}
	if unsupp.OpType != change.OpGuestNicUpdate {
		t.Fatalf("unsupported op type = %s", unsupp.OpType)
	}
}

func TestApply_ValidationBlocked(t *testing.T) {
	h := newHarness(t, fixtureSingleNode)
	ctx := context.Background()
	// A bridge referencing a nonexistent port fails referential validation
	// against the (empty) test inventory.
	cs := h.mustCreate(t, "root@pam", "bad", []change.Op{bridgeCreateOp("pve1", "vmbrbad", []string{"nope0"})})
	var blocked *change.ErrValidationBlocked
	if _, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 0); !errors.As(err, &blocked) {
		t.Fatalf("Apply err = %v, want *ErrValidationBlocked", err)
	}
	if len(blocked.Findings) == 0 {
		t.Fatal("blocked apply carried no findings")
	}
}

func TestConfirm_NotAwaiting(t *testing.T) {
	h := newHarness(t, fixtureSingleNode)
	ctx := context.Background()
	cs := h.mustCreate(t, "root@pam", "x", []change.Op{bridgeCreateOp("pve1", "vmbr1", nil)})
	var nc *change.ErrNotConfirmable
	if _, err := h.svc.Confirm(ctx, cs.ID, "root@pam"); !errors.As(err, &nc) {
		t.Fatalf("Confirm on draft err = %v, want *ErrNotConfirmable", err)
	}
}

func TestRollback_NotEligible(t *testing.T) {
	h := newHarness(t, fixtureSingleNode)
	ctx := context.Background()
	cs := h.mustCreate(t, "root@pam", "x", []change.Op{bridgeCreateOp("pve1", "vmbr1", nil)})
	var nc *change.ErrNotConfirmable
	if _, err := h.svc.Rollback(ctx, cs.ID, "root@pam", nil); !errors.As(err, &nc) {
		t.Fatalf("Rollback on draft err = %v, want *ErrNotConfirmable", err)
	}
}

// T-206: rollback of a committed changeset containing a create *and* an
// in-changeset update of the same bridge now succeeds (T-205 deferred this
// case with a typed *ErrInverseUnsupported, since an op-level inverse can't
// recover the update's prior field values). The T-206 mechanism diffs the
// changeset's own pre-apply snapshot (which predates the bridge entirely)
// against the live state (which has it, at the updated MTU), so the
// restoring draft is a single bridge.delete — exactly reversing both ops at
// once, not just the last one.
func TestRollback_CommittedCreatePlusUpdate_RestoresViaSnapshotDiff(t *testing.T) {
	h := newHarness(t, fixtureSingleNode)
	ctx := context.Background()
	mtu := 9000
	ops := []change.Op{
		bridgeCreateOp("pve1", "vmbrU", nil),
		{
			Type:   change.OpBridgeUpdate,
			Target: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbrU"},
			Params: &change.BridgeUpdateParams{MTU: &mtu},
		},
	}
	cs := h.mustCreate(t, "root@pam", "create+update", ops)
	if _, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 0); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := h.svc.Confirm(ctx, cs.ID, "root@pam"); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	draft, err := h.svc.Rollback(ctx, cs.ID, "root@pam", nil)
	if err != nil {
		t.Fatalf("rollback committed create+update: %v", err)
	}
	if len(draft.Ops) != 1 || draft.Ops[0].Type != change.OpBridgeDelete || draft.Ops[0].Target.ID != "vmbrU" {
		t.Fatalf("restoring ops = %+v, want a single bridge.delete vmbrU", draft.Ops)
	}
}

func TestBuildPlan_OrderingAndSDNLast(t *testing.T) {
	ops := []change.Op{
		bridgeCreateOp("pve1", "a", nil),
		sdnApplyOp(),
		bridgeCreateOp("pve2", "b", nil),
		bridgeCreateOp("pve1", "c", nil),
	}
	plan, err := change.BuildPlan(ops)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	want := []struct {
		kind change.StepKind
		node string
	}{
		{change.StepStageFile, "pve1"},
		{change.StepReload, "pve1"},
		{change.StepStageFile, "pve2"},
		{change.StepReload, "pve2"},
		{change.StepSDNApply, ""},
	}
	if len(plan.Steps) != len(want) {
		t.Fatalf("steps = %+v", plan.Steps)
	}
	for i, w := range want {
		if plan.Steps[i].Kind != w.kind || plan.Steps[i].Node != w.node {
			t.Fatalf("step %d = {%s %s}, want {%s %s}", i, plan.Steps[i].Kind, plan.Steps[i].Node, w.kind, w.node)
		}
	}
	// pve1's stage step realizes both pve1 ops (indices 0 and 3).
	if len(plan.Steps[0].OpIdx) != 2 {
		t.Fatalf("pve1 stage OpIdx = %v, want 2 ops", plan.Steps[0].OpIdx)
	}
}

// An sdn.apply-only changeset exercises the cluster-scope step path (no
// per-node steps): the plan is a single sdn_apply step, executed via the PVE
// gateway, and the post-terminal refresh uses the empty (cluster) scope.
func TestApply_SDNApplyOnly(t *testing.T) {
	h := newHarness(t, fixtureThreeNode)
	ctx := context.Background()
	gw := &fakePVEGateway{client: h.client, pollNode: "pve1"}

	cs := h.mustCreate(t, "root@pam", "sdn", []change.Op{sdnApplyOp()})
	applied, err := h.svc.Apply(ctx, cs.ID, "root@pam", gw, 0)
	if err != nil {
		t.Fatalf("apply sdn-only: %v", err)
	}
	if applied.Status != change.StatusAwaitingConfirm {
		t.Fatalf("status = %s, want awaiting_confirm", applied.Status)
	}
	plan := h.plan(t, cs.ID)
	if len(plan.Steps) != 1 || plan.Steps[0].Kind != change.StepSDNApply {
		t.Fatalf("plan = %+v, want single sdn_apply", plan.Steps)
	}
	if _, err := h.svc.Confirm(ctx, cs.ID, "root@pam"); err != nil {
		t.Fatalf("confirm: %v", err)
	}
}

// sdn.apply with no PVE gateway (no user session) fails the step and the
// changeset goes to failed.
func TestApply_SDNApplyNoGateway(t *testing.T) {
	h := newHarness(t, fixtureThreeNode)
	ctx := context.Background()
	cs := h.mustCreate(t, "root@pam", "sdn", []change.Op{sdnApplyOp()})
	if _, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 0); err == nil {
		t.Fatal("expected sdn.apply with nil gateway to fail")
	}
	if got := h.get(t, cs.ID); got.Status != change.StatusFailed {
		t.Fatalf("status = %s, want failed", got.Status)
	}
}

// Auto-rollback where a node cannot be restored (its reload keeps failing)
// lands the changeset in failed, not rolled_back (the "couldn't fully roll
// back" case).
func TestAutoRollback_RestoreFailsGoesFailed(t *testing.T) {
	h := newHarness(t, fixtureSingleNode)
	ctx := context.Background()
	cs := h.mustCreate(t, "root@pam", "x", []change.Op{bridgeCreateOp("pve1", "vmbrR", nil)})
	if _, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 0); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Make the restoring reload fail, then fire the deadline.
	h.setReloadFail(t, "pve1", true)
	h.timers.fireLatest(t)

	got := h.get(t, cs.ID)
	if got.Status != change.StatusFailed {
		t.Fatalf("status = %s, want failed (rollback could not complete)", got.Status)
	}
}

func TestApply_NotFoundAndIllegalTransition(t *testing.T) {
	h := newHarness(t, fixtureSingleNode)
	ctx := context.Background()

	if _, err := h.svc.Apply(ctx, "does-not-exist", "root@pam", nil, 0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Apply not-found err = %v, want store.ErrNotFound", err)
	}
	if _, err := h.svc.Confirm(ctx, "does-not-exist", "root@pam"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Confirm not-found err = %v", err)
	}
	if _, err := h.svc.Rollback(ctx, "does-not-exist", "root@pam", nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Rollback not-found err = %v", err)
	}

	// Apply a committed changeset again → illegal transition.
	cs := h.mustCreate(t, "root@pam", "x", []change.Op{bridgeCreateOp("pve1", "vmbr1", nil)})
	if _, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 0); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := h.svc.Confirm(ctx, cs.ID, "root@pam"); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	var illegal *change.ErrIllegalTransition
	if _, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 0); !errors.As(err, &illegal) {
		t.Fatalf("re-apply committed err = %v, want *ErrIllegalTransition", err)
	}
}

// Re-arming on an already-armed changeset (e.g. ArmPendingRollbacks called on
// a live Service) stops the existing timer before arming the new one.
func TestArmPendingRollbacks_OnLiveService(t *testing.T) {
	h := newHarness(t, fixtureSingleNode)
	ctx := context.Background()
	cs := h.mustCreate(t, "root@pam", "x", []change.Op{bridgeCreateOp("pve1", "vmbr1", nil)})
	if _, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 0); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := h.svc.ArmPendingRollbacks(ctx); err != nil {
		t.Fatalf("ArmPendingRollbacks: %v", err)
	}
	// Exactly one timer remains armed (the old one was stopped, a new one armed).
	if h.timers.armedCount() != 1 {
		t.Fatalf("armed timers = %d, want 1", h.timers.armedCount())
	}
}

// A mid-apply failure whose staged-file discard also fails records the discard
// error in the rollback log but still lands the changeset in failed.
func TestApply_DiscardFailureDuringRollback(t *testing.T) {
	h := newHarness(t, fixtureThreeNode)
	ctx := context.Background()
	ops := []change.Op{
		bridgeCreateOp("pve1", "vmbrX", nil),
		bridgeCreateOp("pve2", "vmbrY", nil),
	}
	cs := h.mustCreate(t, "root@pam", "x", ops)
	// Fail pve2's reload; make pve2's discard fail too during rollback.
	h.setReloadFail(t, "pve2", true)
	h.agent.setFailDiscard("pve2", true)
	if _, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 0); err == nil {
		t.Fatal("expected apply to fail")
	}
	if got := h.get(t, cs.ID); got.Status != change.StatusFailed {
		t.Fatalf("status = %s, want failed", got.Status)
	}
	log := h.applyLog(t, cs.ID)
	sawFailedRollback := false
	for _, rb := range log.Rollback {
		if rb.Status == change.StepFailed {
			sawFailedRollback = true
		}
	}
	if !sawFailedRollback {
		t.Fatal("expected a failed rollback entry for the discard failure")
	}
}

func TestStopTimers(t *testing.T) {
	h := newHarness(t, fixtureSingleNode)
	ctx := context.Background()
	cs := h.mustCreate(t, "root@pam", "x", []change.Op{bridgeCreateOp("pve1", "vmbr1", nil)})
	if _, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 0); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if h.timers.armedCount() != 1 {
		t.Fatalf("armed = %d, want 1", h.timers.armedCount())
	}
	h.svc.StopTimers()
	if h.timers.armedCount() != 0 {
		t.Fatalf("armed after StopTimers = %d, want 0", h.timers.armedCount())
	}
	// The changeset remains awaiting_confirm (StopTimers changes no status).
	if got := h.get(t, cs.ID); got.Status != change.StatusAwaitingConfirm {
		t.Fatalf("status after StopTimers = %s, want awaiting_confirm", got.Status)
	}
}
