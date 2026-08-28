// SPDX-License-Identifier: Apache-2.0

package digest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/findings"
)

func recipientRules() fixedRules {
	return fixedRules{rules: []findings.AlertRule{
		{ID: "ar-ops", Name: "ops", TargetKind: findings.TargetGeneric, TargetURL: "https://a.invalid", Enabled: true},
		{ID: "ar-oncall", Name: "on-call", TargetKind: findings.TargetNtfy, TargetURL: "https://b.invalid", Enabled: true},
		{ID: "ar-audit", Name: "audit", TargetKind: findings.TargetSlack, TargetURL: "https://c.invalid", Enabled: true},
	}}
}

func TestRecipientFilter_EmptyListIsTheOrdinaryFanOut(t *testing.T) {
	store := &fakeStore{}
	store.setSchedule(Schedule{Every: time.Hour, Enabled: true})

	got, err := RecipientFilter{Rules: recipientRules(), Store: store}.AlertRules(context.Background())
	if err != nil {
		t.Fatalf("AlertRules: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("an empty recipient list yielded %d rule(s), want all 3 — "+
			"empty must mean 'every rule', like every other filter in this codebase", len(got))
	}
}

func TestRecipientFilter_NarrowsToTheNamedRules(t *testing.T) {
	store := &fakeStore{}
	store.setSchedule(Schedule{Every: time.Hour, Enabled: true, RuleIDs: []string{"ar-audit", "ar-ops"}})

	got, err := RecipientFilter{Rules: recipientRules(), Store: store}.AlertRules(context.Background())
	if err != nil {
		t.Fatalf("AlertRules: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("recipient filter yielded %d rule(s), want 2", len(got))
	}
	for _, rule := range got {
		if rule.ID == "ar-oncall" {
			t.Errorf("the digest would reach %q, which the schedule excludes", rule.ID)
		}
	}
	// Source order is preserved, not the recipient list's order — the caller
	// fans out over rules, and reordering them would make the delivery log
	// harder to compare against the alerting one for no benefit.
	if got[0].ID != "ar-ops" || got[1].ID != "ar-audit" {
		t.Errorf("filtered rules = [%s %s], want [ar-ops ar-audit] (source order)", got[0].ID, got[1].ID)
	}
}

// TestRecipientFilter_AnUnreadableScheduleRefusesRatherThanWidens is the
// safety direction: failing open would deliver a digest to targets the
// operator explicitly excluded, and nothing would say so.
func TestRecipientFilter_AnUnreadableScheduleRefusesRatherThanWidens(t *testing.T) {
	boom := errors.New("boom")
	store := &fakeStore{schedErr: boom}

	got, err := RecipientFilter{Rules: recipientRules(), Store: store}.AlertRules(context.Background())
	if !errors.Is(err, boom) {
		t.Errorf("AlertRules with an unreadable schedule: err = %v, want it to wrap %v", err, boom)
	}
	if len(got) != 0 {
		t.Errorf("AlertRules returned %d rule(s) despite the error; it failed open", len(got))
	}
}

func TestRecipientFilter_NoScheduleRowLeavesTheFanOutAlone(t *testing.T) {
	got, err := RecipientFilter{Rules: recipientRules(), Store: &fakeStore{}}.AlertRules(context.Background())
	if err != nil {
		t.Fatalf("AlertRules: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("with no schedule row, AlertRules yielded %d rule(s), want all 3", len(got))
	}
}

func TestRecipientFilter_IsAnAlertRuleProvider(t *testing.T) {
	// Compile-time in recipients.go; restated here so the reason is visible:
	// this is what lets the digest's notifier be built from T-2407's own
	// constructor rather than from a second delivery path.
	var _ findings.AlertRuleProvider = RecipientFilter{}
}
