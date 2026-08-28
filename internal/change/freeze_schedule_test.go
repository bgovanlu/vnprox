// SPDX-License-Identifier: Apache-2.0

package change_test

// freeze_schedule_test.go is T-4006 acceptance criterion 2's own test: a
// changeset already scheduled to fire inside a freeze window declared
// AFTER scheduling is still caught at fire time, not only at the original
// schedule-time validate. It reuses schedule_test.go's pvemock-backed
// harness (newScheduleHarness) exactly, adding only a policy store —
// mirroring newScheduleServiceOverSameState's own "reconstruct a Service
// over the same durable state, plus one more store" pattern immediately
// above it in that file.

import (
	"context"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/store"
)

func TestSchedule_FreezeDeclaredAfterScheduling_StillBlocksAtFireTime(t *testing.T) {
	h, schedRepo, clk := newScheduleHarness(t, fixtureSingleNode)
	ctx := context.Background()

	cs := h.mustCreate(t, "alice@pam", "add vmbr7", []change.Op{bridgeCreateOp("pve1", "vmbr7", nil)})
	mustSchedule(t, h, cs.ID, change.ScheduleParams{
		WindowStart: scheduleEpoch + 10, WindowEnd: scheduleEpoch + 70, ConfirmTimeoutSec: 30,
	})

	// h.svc was built with no Policies store (newScheduleHarness's base
	// Config leaves it unset, like every other schedule test). Rebuild the
	// Service over the exact same durable store, this time with a policy
	// repo wired — the daemon-restart-shaped reconstruction
	// newScheduleServiceOverSameState already does for the identical
	// reason, just with one more store than that helper wires.
	policyRepo := store.NewPolicySetRepo(h.db)
	h.svc = newService(t, change.Config{
		Changesets: h.csRepo, Audit: h.auditRepo, WS: h.ws,
		Nodes: h.agent, Snapshots: h.snapRepo, Blobs: h.blobRepo, Refresher: h.refresher,
		TimerFunc: h.timers.New, Now: clk.now, Schedules: schedRepo, Policies: policyRepo,
	})

	// Declare the freeze window AFTER scheduling, covering the fire instant
	// via the zone-free time.now fact (an absolute one-off range needs no
	// timezone — see policy_eval.go's own doc comment on time.now).
	freeze := change.PolicySet{Version: change.PolicyFormatVersion, Rules: []change.PolicyRule{{
		ID: "freeze-declared-after-schedule", Description: "declared after the changeset was scheduled",
		Severity: change.PolicyDeny, Tags: []string{change.PolicyTagFreeze},
		Match: []change.PolicyCondition{
			{Field: "op", Op: change.PolicyOpMatches, Value: "*"},
			{Field: "time.now", Op: change.PolicyOpGte, Value: float64(scheduleEpoch)},
			{Field: "time.now", Op: change.PolicyOpLt, Value: float64(scheduleEpoch + 1000)},
		},
	}}}
	if _, err := h.svc.SetPolicySet(ctx, "admin@pam", freeze); err != nil {
		t.Fatalf("SetPolicySet: %v", err)
	}

	clk.t = time.Unix(scheduleEpoch+10, 0)
	h.svc.TickSchedules(ctx)

	row, err := h.svc.GetSchedule(ctx, cs.ID)
	if err != nil {
		t.Fatalf("GetSchedule: %v", err)
	}
	if row.Status != store.ScheduleStatusBlocked {
		t.Fatalf("schedule status = %s, want blocked (the freeze declared after scheduling must still catch it at fire time)", row.Status)
	}

	fired := h.get(t, cs.ID)
	if fired.Status == change.StatusAwaitingConfirm || fired.Status == change.StatusCommitted {
		t.Fatalf("changeset status = %s, want it never to have fired", fired.Status)
	}

	// changeset.schedule_fire_blocked / apply_failed is the audit trail an
	// operator reads to learn WHY this schedule never fired — the ops
	// themselves were fine; a policy freeze declared after the fact caught
	// it at the last possible moment, exactly where AC2 requires it.
	entries, err := store.NewAuditRepo(h.db).List(ctx, cs.ID, 100)
	if err != nil {
		t.Fatalf("audit List: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Action == "changeset.schedule_fire_blocked" {
			found = true
		}
	}
	if !found {
		t.Error("no changeset.schedule_fire_blocked audit entry was written")
	}
}
