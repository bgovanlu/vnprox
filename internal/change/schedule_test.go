// SPDX-License-Identifier: Apache-2.0

package change_test

// T-1103 acceptance-criteria coverage. Test <-> AC/safety-analysis-scenario
// cross-reference (see planning/reports/T-1103.md for the full mapping):
//
//   AC1 -> TestSchedule_FireThenAck_Commits, TestSchedule_FireThenNoAck_RollsBack
//   AC2 -> TestSchedule_MgmtPathForbidden_NoOverride
//   AC3 -> TestSchedule_RestartSafety_FiresFromPersistedRow
//   AC4 -> TestSchedule_MissedWindow (table test, both policies)
//   AC5 -> TestSchedule_CancelPreventsApply
//   AC6 -> TestSchedule_RevalidationAtFireTime_Aborts
//
// Safety-analysis scenarios 1-4 (daemon down mid-window; peer unreachable at
// deadline; clock skew; spec/state changed) are cross-referenced in
// schedule.go's own package doc comment and, again, in the report.

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

// scheduleEpoch is an arbitrary fixed "now" every schedule test's fakeClock
// starts at (mirrors localtimer_test.go's own fakeClock seed convention).
const scheduleEpoch = 1_800_000_000

// newScheduleHarness builds an apply-capable harness (single-node fixture,
// pvemock-backed real Apply/rollback) with a fakeClock wired as both
// Config.Now and (implicitly, via Config.Clock's default) the scheduler's
// own Clock, plus a dedicated ChangeScheduleRepo. protectedPath is a fresh
// per-test temp file (empty unless a test writes to it via
// change.SaveProtectedConfig) so mgmt-path tests can control it directly.
func newScheduleHarness(t *testing.T, fixture string, opts ...func(*change.Config)) (*applyHarness, *store.ChangeScheduleRepo, *fakeClock) {
	t.Helper()
	clk := &fakeClock{t: time.Unix(scheduleEpoch, 0)}
	schedDB := openTestDB(t)
	schedRepo := store.NewChangeScheduleRepo(schedDB)
	all := append([]func(*change.Config){
		func(cfg *change.Config) {
			cfg.Now = clk.now
			cfg.Schedules = schedRepo
		},
	}, opts...)
	h := newHarness(t, fixture, all...)
	return h, schedRepo, clk
}

// newScheduleServiceOverSameState mirrors applyHarness.
// newServiceOverSameState (apply_restart_test.go) but additionally reuses
// the same ChangeScheduleRepo and fakeClock — "reconstruct the scheduler
// from the same store" (AC3) means every durable dependency (changesets,
// audit, snapshots, blobs, schedules) is the exact same backing object; only
// in-memory-only state (the rollback-timer map, TimerFunc) is fresh, exactly
// like a real daemon restart.
func (h *applyHarness) newScheduleServiceOverSameState(t *testing.T, timers change.TimerFunc, schedRepo *store.ChangeScheduleRepo, clk *fakeClock) *change.Service {
	t.Helper()
	return newService(t, change.Config{
		Changesets: h.csRepo, Audit: h.auditRepo, WS: h.ws,
		Nodes: h.agent, Snapshots: h.snapRepo, Blobs: h.blobRepo, Refresher: h.refresher,
		TimerFunc: timers, Now: clk.now, Schedules: schedRepo,
	})
}

func mustSchedule(t *testing.T, h *applyHarness, changesetID string, params change.ScheduleParams) change.Schedule {
	t.Helper()
	sched, err := h.svc.Schedule(context.Background(), changesetID, "alice@pam", params)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if sched.CallbackToken == "" {
		t.Fatal("Schedule() returned an empty CallbackToken")
	}
	return sched
}

// TestSchedule_FireThenAck_Commits is AC1's first half: schedule against
// single-node (window T+10..T+70, confirm 30s) -> advancing to T+10 fires
// apply, confirm deadline lands at T+40 -> callback-token ack before T+40 ->
// committed.
func TestSchedule_FireThenAck_Commits(t *testing.T) {
	h, _, clk := newScheduleHarness(t, fixtureSingleNode)
	ctx := context.Background()

	cs := h.mustCreate(t, "alice@pam", "add vmbr7", []change.Op{bridgeCreateOp("pve1", "vmbr7", nil)})
	sched := mustSchedule(t, h, cs.ID, change.ScheduleParams{
		WindowStart: scheduleEpoch + 10, WindowEnd: scheduleEpoch + 70, ConfirmTimeoutSec: 30,
	})

	// Before windowStart: ticking does nothing.
	h.svc.TickSchedules(ctx)
	if got := h.get(t, cs.ID); got.Status != change.StatusDraft && got.Status != change.StatusValidated {
		t.Fatalf("status before windowStart = %s, want draft/validated (unfired)", got.Status)
	}

	clk.t = time.Unix(scheduleEpoch+10, 0)
	h.svc.TickSchedules(ctx)

	fired := h.get(t, cs.ID)
	if fired.Status != change.StatusAwaitingConfirm {
		t.Fatalf("status at windowStart = %s, want awaiting_confirm", fired.Status)
	}
	if fired.ConfirmDeadline == nil || *fired.ConfirmDeadline != scheduleEpoch+40 {
		t.Fatalf("ConfirmDeadline = %v, want %d", fired.ConfirmDeadline, int64(scheduleEpoch+40))
	}

	row, err := h.svc.GetSchedule(ctx, cs.ID)
	if err != nil {
		t.Fatalf("GetSchedule: %v", err)
	}
	if row.Status != store.ScheduleStatusFired {
		t.Errorf("schedule status = %s, want fired", row.Status)
	}

	// Ack before the deadline commits it.
	committed, err := h.svc.AckSchedule(ctx, cs.ID, sched.CallbackToken)
	if err != nil {
		t.Fatalf("AckSchedule: %v", err)
	}
	if committed.Status != change.StatusCommitted {
		t.Fatalf("status after ack = %s, want committed", committed.Status)
	}

	// Single-use: replaying the same token now fails (the changeset is no
	// longer awaiting_confirm).
	if _, err := h.svc.AckSchedule(ctx, cs.ID, sched.CallbackToken); err == nil {
		t.Fatal("AckSchedule replay after commit succeeded, want an error (single-use)")
	}
}

// TestSchedule_FireThenNoAck_RollsBack is AC1's second half: no ack -> rolled
// back at T+40, pre-state byte-identical.
func TestSchedule_FireThenNoAck_RollsBack(t *testing.T) {
	h, _, clk := newScheduleHarness(t, fixtureSingleNode)
	ctx := context.Background()
	before := mustRead(t, h, "pve1")

	cs := h.mustCreate(t, "alice@pam", "add vmbr7", []change.Op{bridgeCreateOp("pve1", "vmbr7", nil)})
	mustSchedule(t, h, cs.ID, change.ScheduleParams{
		WindowStart: scheduleEpoch + 10, WindowEnd: scheduleEpoch + 70, ConfirmTimeoutSec: 30,
	})

	clk.t = time.Unix(scheduleEpoch+10, 0)
	h.svc.TickSchedules(ctx)
	if got := h.get(t, cs.ID); got.Status != change.StatusAwaitingConfirm {
		t.Fatalf("status at windowStart = %s, want awaiting_confirm", got.Status)
	}

	// No ack before the deadline: the commit-confirm timer (unchanged T-304/
	// T-205 machinery) fires on its own, no scheduler involvement at all.
	h.timers.fireLatest(t)

	rolled := h.get(t, cs.ID)
	if rolled.Status != change.StatusRolledBack {
		t.Fatalf("status after deadline with no ack = %s, want rolled_back", rolled.Status)
	}
	if got := h.agent.committedFile("pve1"); got != before {
		t.Fatalf("pre-state not byte-identical after rollback:\n--- want ---\n%s\n--- got ---\n%s", before, got)
	}
}

// TestSchedule_MgmtPathForbidden_NoOverride is AC2: scheduling a
// touchesMgmtPath changeset against a management-carrying node is rejected
// 422-equivalent (*change.ErrMgmtPathUnattendedForbidden), the changeset is
// left completely unchanged, and — the task card's required "no override
// path exists anywhere" property — setting AllowDangerousOps (the one
// config flag that *does* downgrade the underlying T-203 safety-interlock
// finding for an ordinary interactive apply, docs/security.md) has no
// effect on this gate at all: ScheduleParams carries no field capable of
// bypassing it, and Schedule's mgmt-path check runs unconditionally before
// (and independently of) safetyOptions()'s own AllowDangerousOps-aware
// validation pass a few lines later.
func TestSchedule_MgmtPathForbidden_NoOverride(t *testing.T) {
	for _, allowDangerous := range []bool{false, true} {
		t.Run(map[bool]string{false: "allowDangerousOps=false", true: "allowDangerousOps=true"}[allowDangerous], func(t *testing.T) {
			protectedPath := filepath.Join(t.TempDir(), "protected.json")
			h, _, _ := newScheduleHarness(t, fixtureSingleNode, func(cfg *change.Config) {
				cfg.AllowDangerousOps = allowDangerous
				cfg.ProtectedPath = protectedPath
			})
			ctx := context.Background()

			vmbr0 := inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}
			if err := change.SaveProtectedConfig(protectedPath, change.ProtectedConfig{
				Nodes: map[string][]string{"pve1": {vmbr0.String()}},
			}); err != nil {
				t.Fatalf("SaveProtectedConfig: %v", err)
			}

			cs := h.mustCreate(t, "alice@pam", "touch mgmt bridge", []change.Op{{
				Type:   change.OpBridgePortAdd,
				Target: vmbr0,
				Params: &change.BridgePortAddParams{Port: "eno2"},
			}})

			_, err := h.svc.Schedule(ctx, cs.ID, "alice@pam", change.ScheduleParams{
				WindowStart: scheduleEpoch + 10, WindowEnd: scheduleEpoch + 70,
			})
			var forbidden *change.ErrMgmtPathUnattendedForbidden
			if !errors.As(err, &forbidden) {
				t.Fatalf("Schedule() err = %v, want *ErrMgmtPathUnattendedForbidden", err)
			}

			if _, getErr := h.svc.GetSchedule(ctx, cs.ID); !errors.Is(getErr, store.ErrNotFound) {
				t.Fatalf("GetSchedule after forbidden Schedule = %v, want store.ErrNotFound (no row created)", getErr)
			}
			if got := h.get(t, cs.ID); got.Status != change.StatusDraft {
				t.Errorf("changeset status = %s, want draft (unchanged)", got.Status)
			}
		})
	}
}

// TestSchedule_ValidationBlockedFindings: scheduling a changeset carrying an
// error-severity finding (schema: MTU below the valid range) is rejected
// with *change.ErrValidationBlocked.
func TestSchedule_ValidationBlockedFindings(t *testing.T) {
	h, _, _ := newScheduleHarness(t, fixtureSingleNode)
	ctx := context.Background()

	badMTU := 100
	cs := h.mustCreate(t, "alice@pam", "bad mtu", []change.Op{{
		Type:   change.OpBridgeCreate,
		Target: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr7"},
		Params: &change.BridgeCreateParams{MTU: badMTU},
	}})

	_, err := h.svc.Schedule(ctx, cs.ID, "alice@pam", change.ScheduleParams{
		WindowStart: scheduleEpoch + 10, WindowEnd: scheduleEpoch + 70,
	})
	var blocked *change.ErrValidationBlocked
	if !errors.As(err, &blocked) {
		t.Fatalf("Schedule() err = %v, want *ErrValidationBlocked", err)
	}
	if len(blocked.Findings) == 0 {
		t.Error("ErrValidationBlocked.Findings is empty, want at least the MTU schema error")
	}
}

// TestSchedule_BadWindow: windowStart >= windowEnd is rejected.
func TestSchedule_BadWindow(t *testing.T) {
	h, _, _ := newScheduleHarness(t, fixtureSingleNode)
	cs := h.mustCreate(t, "alice@pam", "add vmbr7", []change.Op{bridgeCreateOp("pve1", "vmbr7", nil)})

	_, err := h.svc.Schedule(context.Background(), cs.ID, "alice@pam", change.ScheduleParams{
		WindowStart: scheduleEpoch + 70, WindowEnd: scheduleEpoch + 70,
	})
	var badWindow *change.ErrInvalidScheduleWindow
	if !errors.As(err, &badWindow) {
		t.Fatalf("Schedule() err = %v, want *ErrInvalidScheduleWindow", err)
	}
}

// TestSchedule_BadMissedWindowPolicy: an unrecognized missedWindowPolicy is
// rejected.
func TestSchedule_BadMissedWindowPolicy(t *testing.T) {
	h, _, _ := newScheduleHarness(t, fixtureSingleNode)
	cs := h.mustCreate(t, "alice@pam", "add vmbr7", []change.Op{bridgeCreateOp("pve1", "vmbr7", nil)})

	_, err := h.svc.Schedule(context.Background(), cs.ID, "alice@pam", change.ScheduleParams{
		WindowStart: scheduleEpoch + 10, WindowEnd: scheduleEpoch + 70, MissedWindowPolicy: "explode",
	})
	var badPolicy *change.ErrInvalidMissedWindowPolicy
	if !errors.As(err, &badPolicy) {
		t.Fatalf("Schedule() err = %v, want *ErrInvalidMissedWindowPolicy", err)
	}
}

// TestSchedule_RestartSafety_FiresFromPersistedRow is AC3: schedule,
// reconstruct the scheduler from the same store (simulating restart),
// advance the clock past windowStart -> apply still fires from the
// persisted row.
func TestSchedule_RestartSafety_FiresFromPersistedRow(t *testing.T) {
	h, schedRepo, clk := newScheduleHarness(t, fixtureSingleNode)
	ctx := context.Background()

	cs := h.mustCreate(t, "alice@pam", "add vmbr7", []change.Op{bridgeCreateOp("pve1", "vmbr7", nil)})
	mustSchedule(t, h, cs.ID, change.ScheduleParams{
		WindowStart: scheduleEpoch + 10, WindowEnd: scheduleEpoch + 70, ConfirmTimeoutSec: 30,
	})

	// "Daemon restart": a fresh Service over the same DB/schedules store,
	// fresh in-memory timer state, same fake clock.
	timers2 := &fakeTimers{}
	svc2 := h.newScheduleServiceOverSameState(t, timers2.New, schedRepo, clk)

	clk.t = time.Unix(scheduleEpoch+10, 0)
	svc2.TickSchedules(ctx)

	cs2, err := svc2.Get(ctx, cs.ID)
	if err != nil {
		t.Fatalf("Get after restart+tick: %v", err)
	}
	if cs2.Status != change.StatusAwaitingConfirm {
		t.Fatalf("status after restart+tick = %s, want awaiting_confirm (fired from the persisted row)", cs2.Status)
	}
}

// TestSchedule_MissedWindow is AC4 (table test, both policies): advancing
// the fake clock past windowEnd before the scheduler ever ticks.
func TestSchedule_MissedWindow(t *testing.T) {
	t.Run("skip", func(t *testing.T) {
		h, _, clk := newScheduleHarness(t, fixtureSingleNode)
		ctx := context.Background()

		cs := h.mustCreate(t, "alice@pam", "add vmbr7", []change.Op{bridgeCreateOp("pve1", "vmbr7", nil)})
		mustSchedule(t, h, cs.ID, change.ScheduleParams{
			WindowStart: scheduleEpoch + 10, WindowEnd: scheduleEpoch + 70, MissedWindowPolicy: "skip",
		})

		clk.t = time.Unix(scheduleEpoch+200, 0) // well past windowEnd, no tick in between
		h.svc.TickSchedules(ctx)

		got := h.get(t, cs.ID)
		if got.Status != change.StatusDraft && got.Status != change.StatusValidated {
			t.Fatalf("status after a skipped missed window = %s, want unfired (draft/validated)", got.Status)
		}
		row, err := h.svc.GetSchedule(ctx, cs.ID)
		if err != nil {
			t.Fatalf("GetSchedule: %v", err)
		}
		if row.Status != store.ScheduleStatusMissed {
			t.Errorf("schedule status = %s, want missed", row.Status)
		}

		entries, err := h.auditRepo.List(ctx, cs.ID, 0)
		if err != nil {
			t.Fatalf("audit List: %v", err)
		}
		if !hasAudit(entries, "changeset.schedule_missed", "system:schedule") {
			t.Error("audit trail missing changeset.schedule_missed")
		}

		missed := h.svc.MissedSchedules(ctx)
		found := false
		for _, m := range missed {
			if m.ChangesetID == cs.ID {
				found = true
			}
		}
		if !found {
			t.Errorf("MissedSchedules() = %+v, want an entry for %s (the findings-engine seam)", missed, cs.ID)
		}
	})

	t.Run("applyImmediately", func(t *testing.T) {
		h, _, clk := newScheduleHarness(t, fixtureSingleNode)
		ctx := context.Background()

		cs := h.mustCreate(t, "alice@pam", "add vmbr8", []change.Op{bridgeCreateOp("pve1", "vmbr8", nil)})
		mustSchedule(t, h, cs.ID, change.ScheduleParams{
			WindowStart: scheduleEpoch + 10, WindowEnd: scheduleEpoch + 70,
			ConfirmTimeoutSec: 30, MissedWindowPolicy: "applyImmediately",
		})

		clk.t = time.Unix(scheduleEpoch+200, 0) // well past windowEnd, no tick in between
		h.svc.TickSchedules(ctx)

		got := h.get(t, cs.ID)
		if got.Status != change.StatusAwaitingConfirm {
			t.Fatalf("status after an applyImmediately missed window = %s, want awaiting_confirm (fired with a fresh window)", got.Status)
		}
		// Fresh window: the confirm deadline is now+confirmTimeoutSec, not
		// anywhere near the stale original window.
		if got.ConfirmDeadline == nil || *got.ConfirmDeadline != scheduleEpoch+230 {
			t.Errorf("ConfirmDeadline = %v, want %d (a freshly computed window)", got.ConfirmDeadline, int64(scheduleEpoch+230))
		}

		row, err := h.svc.GetSchedule(ctx, cs.ID)
		if err != nil {
			t.Fatalf("GetSchedule: %v", err)
		}
		if row.Status != store.ScheduleStatusFired {
			t.Errorf("schedule status = %s, want fired", row.Status)
		}
	})
}

// TestSchedule_CancelPreventsApply is AC5: DELETE .../schedule before
// windowStart prevents apply (audited changeset.schedule_cancel); the
// scheduler skips the original firing time.
func TestSchedule_CancelPreventsApply(t *testing.T) {
	h, _, clk := newScheduleHarness(t, fixtureSingleNode)
	ctx := context.Background()

	cs := h.mustCreate(t, "alice@pam", "add vmbr7", []change.Op{bridgeCreateOp("pve1", "vmbr7", nil)})
	mustSchedule(t, h, cs.ID, change.ScheduleParams{
		WindowStart: scheduleEpoch + 10, WindowEnd: scheduleEpoch + 70,
	})

	if err := h.svc.CancelSchedule(ctx, cs.ID, "alice@pam"); err != nil {
		t.Fatalf("CancelSchedule: %v", err)
	}

	clk.t = time.Unix(scheduleEpoch+10, 0)
	h.svc.TickSchedules(ctx)

	got := h.get(t, cs.ID)
	if got.Status != change.StatusDraft && got.Status != change.StatusValidated {
		t.Fatalf("status after ticking past windowStart on a cancelled schedule = %s, want unfired", got.Status)
	}

	entries, err := h.auditRepo.List(ctx, cs.ID, 0)
	if err != nil {
		t.Fatalf("audit List: %v", err)
	}
	if !hasAudit(entries, "changeset.schedule_cancel", "alice@pam") {
		t.Error("audit trail missing changeset.schedule_cancel")
	}

	// A second cancel (already cancelled) is rejected, not silently
	// accepted.
	if err := h.svc.CancelSchedule(ctx, cs.ID, "alice@pam"); !errors.Is(err, store.ErrIllegalState) {
		t.Errorf("second CancelSchedule err = %v, want store.ErrIllegalState", err)
	}
}

// TestSchedule_RevalidationAtFireTime_Aborts is AC6: mutate live state
// between schedule and windowStart to introduce a new blocking finding ->
// apply aborts at fire time, no steps executed, audited.
func TestSchedule_RevalidationAtFireTime_Aborts(t *testing.T) {
	g := inventory.NewGraph()
	h, _, clk := newScheduleHarness(t, fixtureSingleNode, func(cfg *change.Config) {
		cfg.Inventory = inventorySource{g}
	})
	ctx := context.Background()
	before := mustRead(t, h, "pve1")

	target := inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr9"}
	cs := h.mustCreate(t, "alice@pam", "add vmbr9", []change.Op{{
		Type: change.OpBridgeCreate, Target: target, Params: &change.BridgeCreateParams{},
	}})
	if hasErrorFinding(cs.Findings) {
		t.Fatalf("precondition: changeset has error findings at schedule time: %+v", cs.Findings)
	}
	mustSchedule(t, h, cs.ID, change.ScheduleParams{
		WindowStart: scheduleEpoch + 10, WindowEnd: scheduleEpoch + 70,
	})

	// State moves: vmbr9 now already exists live (a referential.already_
	// exists error the schedule-time snapshot didn't have).
	g.ApplyPoll(inventory.SourcePVECluster, inventory.Scope{}, []inventory.Entity{
		&inventory.Node{Ref: inventory.Ref{Kind: inventory.KindNode, Node: "pve1", ID: "pve1"}, Name: "pve1", Status: "online"},
		&inventory.Bridge{Ref: target, Name: "vmbr9", Virt: inventory.BridgeLinux},
	})

	clk.t = time.Unix(scheduleEpoch+10, 0)
	h.svc.TickSchedules(ctx)

	got := h.get(t, cs.ID)
	if got.Status == change.StatusAwaitingConfirm || got.Status == change.StatusCommitted {
		t.Fatalf("status after fire-time revalidation failure = %s, want an aborted (not applying) status", got.Status)
	}
	if got := h.agent.committedFile("pve1"); got != before {
		t.Fatalf("a step executed despite the revalidation failure:\n--- want (untouched) ---\n%s\n--- got ---\n%s", before, got)
	}

	row, err := h.svc.GetSchedule(ctx, cs.ID)
	if err != nil {
		t.Fatalf("GetSchedule: %v", err)
	}
	if row.Status != store.ScheduleStatusBlocked && row.Status != store.ScheduleStatusFailed {
		t.Errorf("schedule status = %s, want blocked/failed", row.Status)
	}

	entries, err := h.auditRepo.List(ctx, cs.ID, 0)
	if err != nil {
		t.Fatalf("audit List: %v", err)
	}
	if !hasAudit(entries, "changeset.schedule_fire_blocked", "system:schedule") {
		t.Error("audit trail missing changeset.schedule_fire_blocked")
	}
}

// TestSchedule_AckWithWrongTokenRejected: an invalid/garbage token never
// confirms.
func TestSchedule_AckWithWrongTokenRejected(t *testing.T) {
	h, _, clk := newScheduleHarness(t, fixtureSingleNode)
	ctx := context.Background()

	cs := h.mustCreate(t, "alice@pam", "add vmbr7", []change.Op{bridgeCreateOp("pve1", "vmbr7", nil)})
	mustSchedule(t, h, cs.ID, change.ScheduleParams{
		WindowStart: scheduleEpoch + 10, WindowEnd: scheduleEpoch + 70, ConfirmTimeoutSec: 30,
	})
	clk.t = time.Unix(scheduleEpoch+10, 0)
	h.svc.TickSchedules(ctx)

	if _, err := h.svc.AckSchedule(ctx, cs.ID, "not-the-right-token"); err == nil {
		t.Fatal("AckSchedule with a wrong token succeeded, want an error")
	}
	if got := h.get(t, cs.ID); got.Status != change.StatusAwaitingConfirm {
		t.Fatalf("status after a rejected ack attempt = %s, want still awaiting_confirm", got.Status)
	}
}

// TestSchedule_ClockInterfaceSeam demonstrates the injected Clock interface
// directly (Config.Clock, distinct from Config.Now/TimerFunc's own
// established fake-clock convention elsewhere in this package): a schedule
// due only per a custom Clock implementation fires exactly when that
// Clock's Now() crosses windowStart, independent of Config.Now.
func TestSchedule_ClockInterfaceSeam(t *testing.T) {
	seam := &manualClock{t: time.Unix(scheduleEpoch, 0)}
	schedDB := openTestDB(t)
	schedRepo := store.NewChangeScheduleRepo(schedDB)
	h := newHarness(t, fixtureSingleNode, func(cfg *change.Config) {
		cfg.Schedules = schedRepo
		cfg.Clock = seam
	})
	ctx := context.Background()

	cs := h.mustCreate(t, "alice@pam", "add vmbr7", []change.Op{bridgeCreateOp("pve1", "vmbr7", nil)})
	mustSchedule(t, h, cs.ID, change.ScheduleParams{
		WindowStart: scheduleEpoch + 10, WindowEnd: scheduleEpoch + 70,
	})

	h.svc.TickSchedules(ctx)
	if got := h.get(t, cs.ID); got.Status == change.StatusAwaitingConfirm {
		t.Fatal("fired before the seam Clock reached windowStart")
	}

	seam.t = time.Unix(scheduleEpoch+10, 0)
	h.svc.TickSchedules(ctx)
	if got := h.get(t, cs.ID); got.Status != change.StatusAwaitingConfirm {
		t.Fatalf("status once the seam Clock reached windowStart = %s, want awaiting_confirm", got.Status)
	}
}

// manualClock is a from-scratch change.Clock implementation (distinct from
// fakeClock, which backs Config.Now's separate func-based convention) —
// TestSchedule_ClockInterfaceSeam's point is that Config.Clock alone is
// sufficient to drive the scheduler.
type manualClock struct{ t time.Time }

func (c *manualClock) Now() time.Time { return c.t }

func hasErrorFinding(findings []change.Finding) bool {
	for _, f := range findings {
		if f.Severity == change.SeverityError {
			return true
		}
	}
	return false
}
