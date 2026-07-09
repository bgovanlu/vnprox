package change_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
)

// newServiceOverSameState builds a second Service sharing this harness's DB,
// host agent (the "disk"), broadcaster and refresher, but with fresh timers —
// modeling a daemon restart, where DB rows and files persist but in-memory
// timer state is gone.
func (h *applyHarness) newServiceOverSameState(t *testing.T, timers change.TimerFunc) *change.Service {
	t.Helper()
	return newService(t, change.Config{
		Changesets: h.csRepo, Audit: h.auditRepo, WS: h.ws,
		Nodes: h.agent, Snapshots: h.snapRepo, Refresher: h.refresher,
		TimerFunc: timers,
	})
}

// Acceptance criterion 2 (timer survives restart, deterministic): a brand-new
// Service instance with zero in-memory timer state reconstructs the rollback
// timer purely from the persisted confirm_deadline and rolls back on fire.
func TestApply_TimerSurvivesRestart_DBReArm(t *testing.T) {
	h := newHarness(t, fixtureSingleNode)
	ctx := context.Background()
	before := mustRead(t, h, "pve1")

	cs := h.mustCreate(t, "bob@pve", "add vmbr5", []change.Op{bridgeCreateOp("pve1", "vmbr5", nil)})
	if _, err := h.svc.Apply(ctx, cs.ID, "bob@pve", nil, 0); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !strings.Contains(h.agent.committedFile("pve1"), "vmbr5") {
		t.Fatal("vmbr5 not applied")
	}

	// "Daemon restart": a fresh Service over the same DB + disk, new timers.
	timers2 := &fakeTimers{}
	svc2 := h.newServiceOverSameState(t, timers2.New)
	if err := svc2.ArmPendingRollbacks(ctx); err != nil {
		t.Fatalf("ArmPendingRollbacks: %v", err)
	}
	if timers2.armedCount() != 1 {
		t.Fatalf("re-armed timers = %d, want 1", timers2.armedCount())
	}

	// The re-armed changeset holds the lock again: a fresh apply is refused.
	cs2 := h.mustCreate(t, "bob@pve", "other", []change.Op{bridgeCreateOp("pve1", "vmbr6", nil)})
	if _, err := svc2.Apply(ctx, cs2.ID, "bob@pve", nil, 0); err == nil {
		t.Fatal("expected apply to be locked while a re-armed changeset is awaiting confirm")
	}

	// Deadline elapses on the restarted daemon → rollback fires.
	timers2.fireLatest(t)

	rolled, err := svc2.Get(ctx, cs.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if rolled.Status != change.StatusRolledBack {
		t.Fatalf("status after restart deadline = %s, want rolled_back", rolled.Status)
	}
	if got := h.agent.committedFile("pve1"); got != before {
		t.Fatalf("file not restored after restart rollback:\n%s", got)
	}
}

// Acceptance criterion 2 (timer survives restart, real wall-clock timer): the
// stronger proof — a restarted Service using the real time.AfterFunc timer,
// with a persisted deadline already in the past, rolls back on schedule with
// no test seam firing it.
func TestApply_TimerSurvivesRestart_RealTimer(t *testing.T) {
	h := newHarness(t, fixtureSingleNode)
	ctx := context.Background()
	before := mustRead(t, h, "pve1")

	cs := h.mustCreate(t, "carol@pve", "add vmbr9", []change.Op{bridgeCreateOp("pve1", "vmbr9", nil)})
	if _, err := h.svc.Apply(ctx, cs.ID, "carol@pve", nil, 0); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Persist a deadline in the past, as a daemon that was down past the
	// window would find on restart.
	row, err := h.csRepo.Get(ctx, cs.ID)
	if err != nil {
		t.Fatalf("csRepo.Get: %v", err)
	}
	row.ConfirmDeadline = sql.NullInt64{Int64: time.Now().Add(-1 * time.Second).Unix(), Valid: true}
	if err := h.csRepo.Update(ctx, row); err != nil {
		t.Fatalf("csRepo.Update: %v", err)
	}

	// Restart with the REAL timer (nil TimerFunc → time.AfterFunc).
	svc2 := h.newServiceOverSameState(t, nil)
	if err := svc2.ArmPendingRollbacks(ctx); err != nil {
		t.Fatalf("ArmPendingRollbacks: %v", err)
	}

	// The real timer fires (d<=0 → immediate) on its own goroutine.
	waitForStatus(t, svc2, cs.ID, change.StatusRolledBack, 3*time.Second)
	if got := h.agent.committedFile("pve1"); got != before {
		t.Fatalf("file not restored after real-timer restart rollback:\n%s", got)
	}
}

// Recovery of an apply interrupted by a crash: a changeset left in "applying"
// is restored from its pre-snapshot and marked failed on the next startup.
func TestArmPendingRollbacks_RecoversInterruptedApply(t *testing.T) {
	h := newHarness(t, fixtureSingleNode)
	ctx := context.Background()
	before := mustRead(t, h, "pve1")

	// Drive a real apply, then rewind the persisted status to "applying" and
	// mutate the live file, simulating a crash mid-apply after the pre-snapshot
	// was taken.
	cs := h.mustCreate(t, "dave@pve", "add vmbrA", []change.Op{bridgeCreateOp("pve1", "vmbrA", nil)})
	if _, err := h.svc.Apply(ctx, cs.ID, "dave@pve", nil, 0); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// file now has vmbrA; force status back to applying in the DB
	row, _ := h.csRepo.Get(ctx, cs.ID)
	row.Status = string(change.StatusApplying)
	row.ConfirmDeadline = sql.NullInt64{}
	if err := h.csRepo.Update(ctx, row); err != nil {
		t.Fatalf("Update: %v", err)
	}

	svc2 := h.newServiceOverSameState(t, (&fakeTimers{}).New)
	if err := svc2.ArmPendingRollbacks(ctx); err != nil {
		t.Fatalf("ArmPendingRollbacks: %v", err)
	}
	recovered, _ := svc2.Get(ctx, cs.ID)
	if recovered.Status != change.StatusFailed {
		t.Fatalf("status after recovery = %s, want failed", recovered.Status)
	}
	if got := h.agent.committedFile("pve1"); got != before {
		t.Fatalf("file not restored during interrupted-apply recovery:\n%s", got)
	}
}

func waitForStatus(t *testing.T, svc *change.Service, id string, want change.Status, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cs, err := svc.Get(context.Background(), id)
		if err == nil && cs.Status == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	cs, _ := svc.Get(context.Background(), id)
	t.Fatalf("timed out waiting for status %s (last: %s)", want, cs.Status)
}
