// SPDX-License-Identifier: Apache-2.0

package change

// freeze_test.go is T-4006's acceptance-criteria coverage: inside/outside a
// declared freeze window, boundary instants, recurring-window rollover
// across weeks, timezone correctness (including a DST-straddling case), the
// Zone-required load-time guard, and the audited override path — plus one
// integration test (in freeze_schedule_test.go) proving a freeze declared
// after a changeset was scheduled still catches it at fire time (AC2).

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

// --- load-time validation: Zone is required for local-wall-clock facts ----

func TestPolicyRule_LocalTimeFactRequiresExplicitZone(t *testing.T) {
	for _, field := range []string{
		policyFieldTimeWeekday, policyFieldTimeMinuteOfDay, policyFieldTimeDate,
		policyFieldTimeDayOfMonth, policyFieldTimeMonth,
	} {
		t.Run(field, func(t *testing.T) {
			set := PolicySet{Version: PolicyFormatVersion, Rules: []PolicyRule{{
				ID: "r", Description: "d", Severity: PolicyDeny,
				Match: []PolicyCondition{cond(policyFieldOpType, PolicyOpMatches, "*"), cond(field, PolicyOpExists, nil)},
				// Zone deliberately omitted.
			}}}
			err := set.Validate("test")
			var loadErr *PolicyLoadError
			if err == nil {
				t.Fatalf("Validate() = nil, want a zone-required error for field %q", field)
			}
			if !asPolicyLoadError(err, &loadErr) || loadErr.Field == "" || !strings.Contains(loadErr.Field, "zone") {
				t.Fatalf("Validate() err = %v, want it to name the missing zone field", err)
			}
		})
	}
}

func TestPolicyRule_TimeNowNeedsNoZone(t *testing.T) {
	// time.now is the one zone-free fact (an absolute instant) — a rule
	// using only it must load fine with Zone left empty.
	set := PolicySet{Version: PolicyFormatVersion, Rules: []PolicyRule{{
		ID: "one-off-freeze", Description: "d", Severity: PolicyDeny,
		Match: []PolicyCondition{
			cond(policyFieldOpType, PolicyOpMatches, "*"),
			cond(policyFieldTimeNow, PolicyOpGte, float64(1_700_000_000)),
			cond(policyFieldTimeNow, PolicyOpLt, float64(1_700_100_000)),
		},
	}}}
	if err := set.Validate("test"); err != nil {
		t.Fatalf("Validate() = %v, want nil (time.now needs no zone)", err)
	}
}

func TestPolicyRule_UnknownZoneRejected(t *testing.T) {
	set := PolicySet{Version: PolicyFormatVersion, Rules: []PolicyRule{{
		ID: "r", Description: "d", Severity: PolicyDeny, Zone: "Not/ARealZone",
		Match: []PolicyCondition{cond(policyFieldOpType, PolicyOpMatches, "*"), cond(policyFieldTimeWeekday, PolicyOpEq, "fri")},
	}}}
	if err := set.Validate("test"); err == nil {
		t.Fatal("Validate() = nil, want an error for an unloadable IANA zone")
	}
}

func TestPolicyRule_UnknownWeekdayValueRejected(t *testing.T) {
	set := PolicySet{Version: PolicyFormatVersion, Rules: []PolicyRule{{
		ID: "r", Description: "d", Severity: PolicyDeny, Zone: "UTC",
		Match: []PolicyCondition{cond(policyFieldOpType, PolicyOpMatches, "*"), cond(policyFieldTimeWeekday, PolicyOpEq, "friday")},
	}}}
	if err := set.Validate("test"); err == nil {
		t.Fatal("Validate() = nil, want an error for a non-abbreviated weekday value")
	}
}

func asPolicyLoadError(err error, target **PolicyLoadError) bool {
	le, ok := err.(*PolicyLoadError)
	if !ok {
		return false
	}
	*target = le
	return true
}

// --- evaluation: inside/outside/boundary/timezone/rollover ---------------

func fridayAfternoonNY() PolicyRule {
	return PolicyRule{
		ID: "friday-afternoon-freeze", Description: "no changes during the Friday freeze",
		Severity: PolicyDeny, Tags: []string{PolicyTagFreeze}, Zone: "America/New_York",
		Match: []PolicyCondition{
			cond(policyFieldOpType, PolicyOpMatches, "*"),
			cond(policyFieldTimeWeekday, PolicyOpEq, "fri"),
			cond(policyFieldTimeMinuteOfDay, PolicyOpGte, 840), // 14:00
			cond(policyFieldTimeMinuteOfDay, PolicyOpLt, 1080), // 18:00
		},
	}
}

// freezeTestOps uses bridge.create (not bridge.update) deliberately: these
// Service-level tests run against an empty inventory snapshot (no
// InventorySource wired), and only a create op's target is allowed not to
// exist yet — an update/delete op would fail referential validation before
// ever reaching the policy class this file is testing.
func freezeTestOps() []Op {
	return []Op{mkOp(OpBridgeCreate, testRef(inventory.KindBridge, "pve1", "vmbr7"), &BridgeCreateParams{MTU: 1500})}
}

func evaluatesToViolation(rule PolicyRule, evalTime time.Time) bool {
	res := EvaluatePolicy(PolicyInput{Set: PolicySet{Rules: []PolicyRule{rule}}, EvalTime: evalTime}, freezeTestOps(), inventory.NewGraph().Snapshot())
	return len(res.Rules) == 1 && len(res.Rules[0].ViolatingOps) > 0
}

func TestFreezeWindow_InsideOutsideAndBoundaries(t *testing.T) {
	rule := fridayAfternoonNY()
	est := time.FixedZone("EST", -5*60*60) // America/New_York in January (no DST)

	cases := []struct {
		at   time.Time
		name string
		want bool
	}{
		{name: "Friday 15:00, well inside the window", at: time.Date(2026, time.January, 16, 15, 0, 0, 0, est), want: true},
		{name: "Friday 13:59, one minute before the window opens", at: time.Date(2026, time.January, 16, 13, 59, 0, 0, est), want: false},
		{name: "Friday 14:00, the window's opening instant (inclusive, gte)", at: time.Date(2026, time.January, 16, 14, 0, 0, 0, est), want: true},
		{name: "Friday 17:59, the last minute inside the window", at: time.Date(2026, time.January, 16, 17, 59, 0, 0, est), want: true},
		{name: "Friday 18:00, the window's closing instant (exclusive, lt)", at: time.Date(2026, time.January, 16, 18, 0, 0, 0, est), want: false},
		{name: "Monday 15:00, same time of day but the wrong weekday", at: time.Date(2026, time.January, 19, 15, 0, 0, 0, est), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := evaluatesToViolation(rule, tc.at); got != tc.want {
				t.Errorf("violation = %v, want %v (at %v)", got, tc.want, tc.at)
			}
		})
	}
}

// TestFreezeWindow_RecurringRollover proves the weekly window is genuinely
// recurring — it fires on more than one occurrence, not only the first
// Friday it happens to be evaluated against — since the fact computation is
// stateless (derived fresh from EvalTime every call, nothing counts
// occurrences), a bug here would look like "fires once then never again" or
// "never fires at all", not a subtle drift.
func TestFreezeWindow_RecurringWindowRollsOverEveryWeek(t *testing.T) {
	rule := fridayAfternoonNY()
	est := time.FixedZone("EST", -5*60*60)

	fridays := []time.Time{
		time.Date(2026, time.January, 16, 15, 0, 0, 0, est), // week 1
		time.Date(2026, time.January, 23, 15, 0, 0, 0, est), // week 2
		time.Date(2026, time.January, 30, 15, 0, 0, 0, est), // week 3
	}
	for _, at := range fridays {
		if !evaluatesToViolation(rule, at) {
			t.Errorf("Friday %v did not fire the recurring freeze window", at)
		}
	}
	// The Saturdays immediately following each Friday must NOT fire.
	for _, at := range fridays {
		sat := at.AddDate(0, 0, 1)
		if evaluatesToViolation(rule, sat) {
			t.Errorf("Saturday %v incorrectly fired a Friday-only freeze window", sat)
		}
	}
}

// TestFreezeWindow_TimezoneCorrectness is the card's own stated trap: the
// SAME absolute instant must be judged differently depending on which zone
// the rule declares, and the local-wall-clock reading must track DST rather
// than a fixed UTC offset (America/New_York is UTC-5 in January, UTC-4 in
// July).
func TestFreezeWindow_TimezoneCorrectness(t *testing.T) {
	nyRule := fridayAfternoonNY()
	utcRule := fridayAfternoonNY()
	utcRule.Zone = "UTC"

	// 2026-01-16T20:00:00Z is 15:00 EST in New York (winter, UTC-5) — inside
	// the New York rule's window, but 20:00 UTC is outside the UTC rule's
	// 14:00-18:00 window.
	instant := time.Date(2026, time.January, 16, 20, 0, 0, 0, time.UTC)
	if !evaluatesToViolation(nyRule, instant) {
		t.Error("America/New_York rule: want a violation at 15:00 local (winter, UTC-5)")
	}
	if evaluatesToViolation(utcRule, instant) {
		t.Error("UTC rule: want no violation at 20:00 UTC (same absolute instant, different zone)")
	}

	// 2026-07-17T19:00:00Z is 15:00 EDT in New York (summer, UTC-4) — DST
	// changed the offset, but the local wall-clock reading (and therefore
	// the freeze decision) is unaffected, exactly as findings.QuietHours
	// documents for its own daily window.
	summerInstant := time.Date(2026, time.July, 17, 19, 0, 0, 0, time.UTC)
	if !evaluatesToViolation(nyRule, summerInstant) {
		t.Error("America/New_York rule: want a violation at 15:00 local across the DST boundary (summer, UTC-4)")
	}
}

// --- override path (Service-level, T-4006's audited escape hatch) --------

func newFreezeTestService(t *testing.T, now *int64) (*Service, *store.DB) {
	t.Helper()
	db := openTestDB(t)
	svc, err := NewService(Config{
		Changesets:      store.NewChangesetRepo(db),
		Audit:           store.NewAuditRepo(db),
		Policies:        store.NewPolicySetRepo(db),
		FreezeOverrides: store.NewChangesetFreezeOverrideRepo(db),
		Schedules:       store.NewChangeScheduleRepo(db),
		ProtectedPath:   t.TempDir() + "/protected.json",
		Now:             func() time.Time { return time.Unix(*now, 0) },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, db
}

// freezeAlwaysDeny is a freeze-tagged rule that matches every op
// unconditionally (via time.now with a huge range) — deliberately not
// weekday-based, so these Service-level tests don't depend on which day the
// test happens to run.
func freezeAlwaysDeny() PolicyRule {
	return PolicyRule{
		ID: "always-frozen", Description: "T-4006 override-path fixture", Severity: PolicyDeny,
		Tags: []string{PolicyTagFreeze},
		Match: []PolicyCondition{
			cond(policyFieldOpType, PolicyOpMatches, "*"),
			cond(policyFieldTimeNow, PolicyOpGte, float64(0)),
		},
	}
}

func TestFreezeOverride_BlocksThenOverrideDowngradesToVisibleWarning(t *testing.T) {
	now := int64(1_700_000_000)
	svc, db := newFreezeTestService(t, &now)
	ctx := context.Background()

	if _, err := svc.SetPolicySet(ctx, "admin", PolicySet{Rules: []PolicyRule{freezeAlwaysDeny()}}); err != nil {
		t.Fatalf("SetPolicySet: %v", err)
	}

	cs, err := svc.Create(ctx, "alice", "touch vmbr0", freezeTestOps())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !hasError(cs.Findings) {
		t.Fatalf("Create findings = %+v, want a blocking policy.violation while the freeze is active", cs.Findings)
	}
	if !containsPolicyMessage(cs.Findings, "always-frozen") {
		t.Fatalf("findings = %+v, want them to name the freeze rule", cs.Findings)
	}

	// No reason -> refused, nothing recorded, nothing changes.
	if _, err = svc.InvokeFreezeOverride(ctx, cs.ID, "bob", "  "); err == nil {
		t.Fatal("InvokeFreezeOverride with a blank reason succeeded, want *ErrFreezeOverrideReasonRequired")
	}
	if _, ok := svc.freezeOverrideFor(ctx, cs.ID); ok {
		t.Fatal("a refused override still recorded one")
	}

	rec, err := svc.InvokeFreezeOverride(ctx, cs.ID, "bob", "emergency router replacement, on-call approved")
	if err != nil {
		t.Fatalf("InvokeFreezeOverride: %v", err)
	}
	if rec.InvokedBy != "bob" || rec.Reason != "emergency router replacement, on-call approved" {
		t.Fatalf("record = %+v", rec)
	}

	// The very next validate sees the override: still visible (a finding is
	// still produced, naming the override), but no longer blocking.
	validated, err := svc.Validate(ctx, cs.ID, "alice")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if hasError(validated.Findings) {
		t.Fatalf("findings after override = %+v, want none blocking", validated.Findings)
	}
	if validated.Status != StatusValidated {
		t.Fatalf("Status = %s, want validated", validated.Status)
	}
	if !containsPolicyMessage(validated.Findings, "overridden") {
		t.Fatalf("findings = %+v, want a visible [overridden: ...] annotation, not a silently vanished finding", validated.Findings)
	}
	if !containsPolicyMessage(validated.Findings, "bob") {
		t.Fatalf("findings = %+v, want the override to name who invoked it", validated.Findings)
	}

	// Audited under its own action.
	entries, err := store.NewAuditRepo(db).List(ctx, cs.ID, 100)
	if err != nil {
		t.Fatalf("audit List: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Action == "change.freeze_override" {
			found = true
			if e.Username != "bob" {
				t.Errorf("audit entry actor = %q, want bob", e.Username)
			}
			if !strings.Contains(e.DetailJSON.String, "emergency router replacement") {
				t.Errorf("audit detail %q must carry the written reason", e.DetailJSON.String)
			}
		}
	}
	if !found {
		t.Error("no change.freeze_override audit entry was written")
	}
}

// TestFreezeOverride_EditingOpsInvalidatesIt mirrors T-2604's break-glass
// ops-fingerprint pinning: an override taken for one draft must not
// silently authorize whatever it is edited into afterwards.
func TestFreezeOverride_EditingOpsInvalidatesIt(t *testing.T) {
	now := int64(1_700_000_000)
	svc, _ := newFreezeTestService(t, &now)
	ctx := context.Background()

	if _, err := svc.SetPolicySet(ctx, "admin", PolicySet{Rules: []PolicyRule{freezeAlwaysDeny()}}); err != nil {
		t.Fatalf("SetPolicySet: %v", err)
	}
	cs, err := svc.Create(ctx, "alice", "touch vmbr0", freezeTestOps())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err = svc.InvokeFreezeOverride(ctx, cs.ID, "bob", "on-call incident"); err != nil {
		t.Fatalf("InvokeFreezeOverride: %v", err)
	}

	// A different, still-referentially-valid create op (not a mutation of
	// the original target) — so any blocking finding below can only be the
	// freeze rule re-firing, never a referential error that would pass this
	// assertion for the wrong reason.
	edited := []Op{mkOp(OpBridgeCreate, testRef(inventory.KindBridge, "pve1", "vmbr8"), &BridgeCreateParams{MTU: 9000})}
	updated, err := svc.UpdateDraft(ctx, cs.ID, "alice", nil, edited)
	if err != nil {
		t.Fatalf("UpdateDraft: %v", err)
	}
	if !containsPolicyMessage(updated.Findings, "always-frozen") {
		t.Fatalf("findings after editing past an override = %+v, want the freeze rule blocking again (the override pinned to the OLD ops)", updated.Findings)
	}
	if containsPolicyMessage(updated.Findings, "overridden") {
		t.Fatalf("findings after editing past an override = %+v, want the [overridden: ...] note gone — the old override must not carry over", updated.Findings)
	}
}

func TestFreezeOverride_RequiresConfiguredStore(t *testing.T) {
	now := int64(1_700_000_000)
	db := openTestDB(t)
	svc, err := NewService(Config{
		Changesets: store.NewChangesetRepo(db), Audit: store.NewAuditRepo(db),
		Policies: store.NewPolicySetRepo(db),
		// FreezeOverrides deliberately left nil.
		Now: func() time.Time { return time.Unix(now, 0) },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	ctx := context.Background()
	cs, err := svc.Create(ctx, "alice", "touch vmbr0", freezeTestOps())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err = svc.InvokeFreezeOverride(ctx, cs.ID, "bob", "reason"); err == nil {
		t.Fatal("InvokeFreezeOverride with no store configured succeeded, want *ErrFreezeOverrideNotConfigured")
	}
}

func containsPolicyMessage(findings []Finding, substr string) bool {
	for _, f := range findings {
		if f.Code == codePolicyViolation && strings.Contains(f.Message, substr) {
			return true
		}
	}
	return false
}

// --- calendar extraction (best-effort renderer, not an evaluation path) --

func TestExtractFreezeWindows_RendersWhatItRecognizes(t *testing.T) {
	set := PolicySet{Rules: []PolicyRule{
		fridayAfternoonNY(),
		{ID: "not-a-freeze", Description: "an ordinary deny rule", Severity: PolicyDeny,
			Match: []PolicyCondition{cond(policyFieldTargetID, PolicyOpEq, "vmbr9")}},
	}}
	views := ExtractFreezeWindows(set)
	if len(views) != 1 {
		t.Fatalf("ExtractFreezeWindows returned %d views, want exactly 1 (only the freeze-tagged rule)", len(views))
	}
	v := views[0]
	if v.RuleID != "friday-afternoon-freeze" || !v.Recognized {
		t.Fatalf("view = %+v, want the Friday rule recognized", v)
	}
	if len(v.Weekdays) != 1 || v.Weekdays[0] != "fri" {
		t.Errorf("Weekdays = %v, want [fri]", v.Weekdays)
	}
	if v.MinuteOfDayStart == nil || *v.MinuteOfDayStart != 840 || v.MinuteOfDayEnd == nil || *v.MinuteOfDayEnd != 1080 {
		t.Errorf("MinuteOfDayStart/End = %v/%v, want 840/1080", v.MinuteOfDayStart, v.MinuteOfDayEnd)
	}
	if v.Zone != "America/New_York" {
		t.Errorf("Zone = %q, want America/New_York", v.Zone)
	}
}

func TestCalendar_CombinesFreezeWindowsAndPendingSchedules(t *testing.T) {
	now := int64(1_700_000_000)
	svc, _ := newFreezeTestService(t, &now)
	ctx := context.Background()

	if _, err := svc.SetPolicySet(ctx, "admin", PolicySet{Rules: []PolicyRule{fridayAfternoonNY()}}); err != nil {
		t.Fatalf("SetPolicySet: %v", err)
	}
	view, err := svc.Calendar(ctx)
	if err != nil {
		t.Fatalf("Calendar: %v", err)
	}
	if len(view.FreezeWindows) != 1 {
		t.Fatalf("FreezeWindows = %+v, want exactly 1", view.FreezeWindows)
	}
	if view.Schedules != nil {
		t.Fatalf("Schedules = %+v, want none (nothing scheduled)", view.Schedules)
	}
}
