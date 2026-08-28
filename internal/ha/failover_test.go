// SPDX-License-Identifier: Apache-2.0

package ha_test

import (
	"context"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/ha"
)

const origInterfaces = "auto lo\niface lo inet loopback\n"

// killActive simulates the active daemon's process dying: its in-process
// commit-confirm timers stop firing and the replication links go dark.
func killActive(a, b *haDaemon) {
	a.svc.StopTimers()
	a.link.partition(true)
	b.link.partition(true)
}

// TestFailover_ReArmsSameAbsoluteDeadline_RollsBackExactlyOnce is AC1's
// no-ack half: the active applies (deadline T+30), replication catches up, the
// active is killed, the standby promotes on lease expiry and re-arms the SAME
// absolute deadline T+30, and a missing ack rolls back at T+30 exactly once.
func TestFailover_ReArmsSameAbsoluteDeadline_RollsBackExactlyOnce(t *testing.T) {
	clk, nodes, a, b := buildPair(t)
	ctx := context.Background()

	cs, err := a.svc.Create(ctx, "alice@pam", "add vmbr9", []change.Op{bridgeCreate("pve1", "vmbr9")})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	applied, err := a.svc.Apply(ctx, cs.ID, "alice@pam", nil, 30*time.Second)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if applied.Status != change.StatusAwaitingConfirm {
		t.Fatalf("status after apply = %s, want awaiting_confirm", applied.Status)
	}
	if applied.ConfirmDeadline == nil || *applied.ConfirmDeadline != haEpoch+30 {
		t.Fatalf("confirm deadline = %v, want %d", applied.ConfirmDeadline, int64(haEpoch+30))
	}
	if nodes.committedOf("pve1") == origInterfaces {
		t.Fatalf("node file not mutated by apply")
	}

	a.mgr.Tick(ctx) // replicate active -> standby (changeset + pre-snapshot + blob)
	// The standby now holds the awaiting_confirm changeset with the SAME deadline.
	repl, err := b.repos.changeset.Get(ctx, cs.ID)
	if err != nil || !repl.ConfirmDeadline.Valid || repl.ConfirmDeadline.Int64 != haEpoch+30 {
		t.Fatalf("replicated changeset deadline = %+v, want %d", repl.ConfirmDeadline, int64(haEpoch+30))
	}

	killActive(a, b)

	clk.advance(20 * time.Second) // now ~ T+20: past observed lease expiry + margin, before T+30
	b.mgr.Tick(ctx)               // standby promotes -> ReArm re-arms at absolute T+30
	if b.mgr.Role() != ha.RoleActive {
		t.Fatalf("standby role = %s, want active (promoted)", b.mgr.Role())
	}
	if n := b.timers.armedCount(); n != 1 {
		t.Fatalf("standby re-armed timer count = %d, want 1 (same changeset, absolute deadline)", n)
	}

	clk.advance(15 * time.Second) // now ~ T+35 > T+30
	b.timers.fireAll()            // the re-armed commit-confirm timer fires

	if got := nodes.committedOf("pve1"); got != origInterfaces {
		t.Errorf("node file = %q, want restored to pre-apply state (rolled back)", got)
	}
	rolled, err := b.repos.changeset.Get(ctx, cs.ID)
	if err != nil {
		t.Fatalf("standby Get after rollback: %v", err)
	}
	if rolled.Status != string(change.StatusRolledBack) {
		t.Errorf("changeset status = %s, want rolled_back (exactly one rollback)", rolled.Status)
	}
	// Firing again is a no-op (exactly once): status stays rolled_back.
	b.timers.fireAll()
	again, _ := b.repos.changeset.Get(ctx, cs.ID)
	if again.Status != string(change.StatusRolledBack) {
		t.Errorf("changeset status after second fire = %s, want still rolled_back", again.Status)
	}
}

// TestFailover_ConfirmAfterPromotion_Commits is AC1's ack half: after the
// standby promotes and re-arms, a confirm before T+30 commits — the node change
// stays applied and the timer is cancelled.
func TestFailover_ConfirmAfterPromotion_Commits(t *testing.T) {
	clk, nodes, a, b := buildPair(t)
	ctx := context.Background()

	cs, err := a.svc.Create(ctx, "alice@pam", "add vmbr9", []change.Op{bridgeCreate("pve1", "vmbr9")})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err = a.svc.Apply(ctx, cs.ID, "alice@pam", nil, 30*time.Second); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	applied := nodes.committedOf("pve1")
	a.mgr.Tick(ctx)
	killActive(a, b)

	clk.advance(20 * time.Second)
	b.mgr.Tick(ctx) // promote + re-arm

	committed, err := b.svc.Confirm(ctx, cs.ID, "bob@pam")
	if err != nil {
		t.Fatalf("Confirm on promoted standby: %v", err)
	}
	if committed.Status != change.StatusCommitted {
		t.Errorf("status after confirm = %s, want committed", committed.Status)
	}
	if n := b.timers.armedCount(); n != 0 {
		t.Errorf("armed timer count after confirm = %d, want 0 (cancelled)", n)
	}
	// A late timer fire must not roll back a committed change.
	clk.advance(15 * time.Second)
	b.timers.fireAll()
	if got := nodes.committedOf("pve1"); got != applied {
		t.Errorf("node file = %q, want still the applied config %q (committed, not rolled back)", got, applied)
	}
}

// TestFailover_ScheduledWindowSurvivesFailover is AC4: a scheduled changeset's
// window survives a mid-window failover — the new active fires apply at the
// ORIGINAL absolute windowStart, never a time recomputed from promotion.
func TestFailover_ScheduledWindowSurvivesFailover(t *testing.T) {
	clk, _, a, b := buildPair(t)
	ctx := context.Background()

	cs, err := a.svc.Create(ctx, "alice@pam", "scheduled vmbr9", []change.Op{bridgeCreate("pve1", "vmbr9")})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Window opens well AFTER the standby will promote, so the test proves the
	// fire is keyed to the original absolute windowStart, not promotion time.
	const windowStart = haEpoch + 40
	if _, err = a.svc.Schedule(ctx, cs.ID, "alice@pam", change.ScheduleParams{
		WindowStart: windowStart, WindowEnd: haEpoch + 100, ConfirmTimeoutSec: 30,
	}); err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	a.mgr.Tick(ctx) // replicate the pending schedule + draft changeset to the standby
	sched, err := b.repos.sched.Get(ctx, cs.ID)
	if err != nil || sched.WindowStart != windowStart {
		t.Fatalf("replicated schedule = %+v, want windowStart %d", sched, int64(windowStart))
	}

	killActive(a, b)

	clk.advance(20 * time.Second) // T+20: before the window
	b.mgr.Tick(ctx)               // promote; ReArm's TickSchedules sees now < windowStart -> does NOT fire early
	if got, _ := b.repos.changeset.Get(ctx, cs.ID); got.Status == string(change.StatusAwaitingConfirm) {
		t.Fatalf("schedule fired before its original windowStart (status %s)", got.Status)
	}

	// Advance to the original absolute windowStart and tick the promoted
	// standby's scheduler: it fires now, at the original window.
	clk.advance(20 * time.Second) // T+40 == windowStart
	b.svc.TickSchedules(ctx)

	fired, err := b.repos.changeset.Get(ctx, cs.ID)
	if err != nil {
		t.Fatalf("Get after scheduled fire: %v", err)
	}
	if fired.Status != string(change.StatusAwaitingConfirm) {
		t.Errorf("status after scheduled fire = %s, want awaiting_confirm (fired at original window)", fired.Status)
	}
	row, err := b.svc.GetSchedule(ctx, cs.ID)
	if err != nil {
		t.Fatalf("GetSchedule: %v", err)
	}
	if row.Status != "fired" {
		t.Errorf("schedule status = %s, want fired", row.Status)
	}
	// The confirm deadline is measured from the original window (windowStart +
	// 30s), not from the promotion instant at T+20.
	if !fired.ConfirmDeadline.Valid || fired.ConfirmDeadline.Int64 != windowStart+30 {
		t.Errorf("confirm deadline = %+v, want %d (original window, not promotion-relative)", fired.ConfirmDeadline, int64(windowStart+30))
	}
}
