package ha_test

// T-1707 v3.0 release: HA failover soak. Drives the two-daemon failover harness
// (harness_test.go / failover_test.go — a REAL change.Service on each daemon, an
// injected fake clock, no real sleeps) through soakCycles independent
// kill/promote cycles, alternating the two terminal outcomes (missing-ack →
// rollback, confirm → commit), and asserts the two safety invariants T-1704
// guarantees hold on EVERY cycle:
//
//   - zero double-apply:      the node file is mutated exactly once (by apply)
//                             and, on the rollback path, restored exactly once;
//                             a second timer fire is always a no-op.
//   - zero dropped-rollback:  a missing confirm ALWAYS rolls back (never leaves
//                             the change applied); a confirmed change is NEVER
//                             rolled back by a late timer fire.
//
// This is a deterministic soak (fake clock, N independent failover cycles), not
// a wall-clock endurance run — the same honest substitution T-607/T-1208 make
// for the 24h daemon soak. The cycle count is stated in planning/reports/T-1707.md.

import (
	"context"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/ha"
)

// soakCycles is the number of independent failover cycles the soak drives.
// Kept modest so the soak stays part of the ordinary `go test` / `make check`
// run (each cycle stands up two real change.Services over real SQLite stores);
// raise it for a longer local endurance pass.
const soakCycles = 40

func TestFailoverSoak_NoDoubleApply_NoDroppedRollback(t *testing.T) {
	var rollbacks, commits int

	for i := 0; i < soakCycles; i++ {
		clk, nodes, a, b := buildPair(t)
		ctx := context.Background()

		cs, err := a.svc.Create(ctx, "alice@pam", "soak vmbr9", []change.Op{bridgeCreate("pve1", "vmbr9")})
		if err != nil {
			t.Fatalf("cycle %d: Create: %v", i, err)
		}
		applied, err := a.svc.Apply(ctx, cs.ID, "alice@pam", nil, 30*time.Second)
		if err != nil {
			t.Fatalf("cycle %d: Apply: %v", i, err)
		}
		if applied.Status != change.StatusAwaitingConfirm {
			t.Fatalf("cycle %d: status after apply = %s, want awaiting_confirm", i, applied.Status)
		}
		appliedFile := nodes.committedOf("pve1")
		if appliedFile == origInterfaces {
			t.Fatalf("cycle %d: node file not mutated by apply (no-op apply)", i)
		}

		a.mgr.Tick(ctx) // replicate active -> standby (changeset + pre-snapshot + blob)
		killActive(a, b)

		clk.advance(20 * time.Second) // past observed lease expiry + fencing margin
		b.mgr.Tick(ctx)               // standby promotes, re-arms the SAME absolute deadline
		if b.mgr.Role() != ha.RoleActive {
			t.Fatalf("cycle %d: standby role = %s, want active (promoted)", i, b.mgr.Role())
		}

		if i%2 == 0 {
			// Missing-ack path: the re-armed commit-confirm timer must roll back
			// exactly once — never leaving the change applied (dropped rollback)
			// and never rolling back twice (double action).
			clk.advance(15 * time.Second) // > T+30
			b.timers.fireAll()

			if got := nodes.committedOf("pve1"); got != origInterfaces {
				t.Fatalf("cycle %d: node file = %q, want restored to pre-apply state (dropped rollback)", i, got)
			}
			rolled, err := b.repos.changeset.Get(ctx, cs.ID)
			if err != nil {
				t.Fatalf("cycle %d: Get after rollback: %v", i, err)
			}
			if rolled.Status != string(change.StatusRolledBack) {
				t.Fatalf("cycle %d: status = %s, want rolled_back (exactly one rollback)", i, rolled.Status)
			}
			// A second fire is a no-op: no double-rollback, no re-apply.
			b.timers.fireAll()
			again, _ := b.repos.changeset.Get(ctx, cs.ID)
			if again.Status != string(change.StatusRolledBack) {
				t.Fatalf("cycle %d: status after second fire = %s, want still rolled_back", i, again.Status)
			}
			if got := nodes.committedOf("pve1"); got != origInterfaces {
				t.Fatalf("cycle %d: node file changed on second fire = %q, want still pre-apply state", i, got)
			}
			rollbacks++
		} else {
			// Confirm path: a confirm on the promoted standby commits; a late
			// timer fire must NOT roll back a committed change.
			committed, err := b.svc.Confirm(ctx, cs.ID, "bob@pam")
			if err != nil {
				t.Fatalf("cycle %d: Confirm on promoted standby: %v", i, err)
			}
			if committed.Status != change.StatusCommitted {
				t.Fatalf("cycle %d: status after confirm = %s, want committed", i, committed.Status)
			}
			if n := b.timers.armedCount(); n != 0 {
				t.Fatalf("cycle %d: armed timer count after confirm = %d, want 0 (cancelled)", i, n)
			}
			clk.advance(15 * time.Second)
			b.timers.fireAll() // late fire — must be a no-op
			if got := nodes.committedOf("pve1"); got != appliedFile {
				t.Fatalf("cycle %d: node file = %q, want still the applied config %q (committed, not rolled back)", i, got, appliedFile)
			}
			commits++
		}
	}

	if rollbacks+commits != soakCycles {
		t.Fatalf("accounted %d cycles, want %d", rollbacks+commits, soakCycles)
	}
	t.Logf("HA failover soak: %d cycles, %d rollbacks + %d commits, zero double-apply, zero dropped-rollback",
		soakCycles, rollbacks, commits)
}
