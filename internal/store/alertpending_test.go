package store

import (
	"context"
	"testing"
)

func newPendingTestRepo(t *testing.T) (*AlertPendingRepo, context.Context) {
	t.Helper()
	db := openTestDB(t)
	return NewAlertPendingRepo(db), context.Background()
}

func TestAlertPendingRepo_RoundTripsAndClears(t *testing.T) {
	repo, ctx := newPendingTestRepo(t)

	id, err := repo.Insert(ctx, AlertPending{
		RuleID: "rule-1", FindingID: "drift|pve1", FindingJSON: `{"id":"drift|pve1"}`,
		Kind: "new", At: 1700000000, FlushAt: 1700000600, Reason: "digest window 10m",
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if id == "" {
		t.Fatal("Insert returned an empty id; the caller cannot clear the row it just queued")
	}

	// Not yet due.
	due, err := repo.Due(ctx, 1700000599)
	if err != nil {
		t.Fatalf("Due: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("%d rows due one second early, want 0", len(due))
	}

	// Due exactly at flush_at — the boundary is inclusive, or a row whose
	// deadline lands precisely on a tick waits a whole extra interval.
	due, err = repo.Due(ctx, 1700000600)
	if err != nil {
		t.Fatalf("Due: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("%d rows due at flush_at, want 1", len(due))
	}
	got := due[0]
	if got.RuleID != "rule-1" || got.FindingJSON != `{"id":"drift|pve1"}` || got.Reason != "digest window 10m" {
		t.Errorf("row round-tripped as %+v", got)
	}

	if delErr := repo.DeleteByIDs(ctx, []string{id}); delErr != nil {
		t.Fatalf("DeleteByIDs: %v", delErr)
	}
	due, err = repo.Due(ctx, 1700009999)
	if err != nil {
		t.Fatalf("Due after delete: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("%d rows survived the delete", len(due))
	}
}

// TestAlertPendingRepo_EarliestFlushAtIsPerRule is what makes a digest window
// measure from its first event. If it returned another rule's deadline, one
// noisy rule would drag every other rule's digest with it.
func TestAlertPendingRepo_EarliestFlushAtIsPerRule(t *testing.T) {
	repo, ctx := newPendingTestRepo(t)

	if _, ok, err := repo.EarliestFlushAt(ctx, "rule-1"); err != nil || ok {
		t.Fatalf("EarliestFlushAt on an empty queue = (_, %v, %v), want (_, false, nil)", ok, err)
	}

	for _, row := range []AlertPending{
		{RuleID: "rule-1", FindingID: "a", FindingJSON: "{}", Kind: "new", At: 1, FlushAt: 900},
		{RuleID: "rule-1", FindingID: "b", FindingJSON: "{}", Kind: "new", At: 2, FlushAt: 500},
		{RuleID: "rule-2", FindingID: "c", FindingJSON: "{}", Kind: "new", At: 3, FlushAt: 100},
	} {
		if _, err := repo.Insert(ctx, row); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	at, ok, err := repo.EarliestFlushAt(ctx, "rule-1")
	if err != nil || !ok {
		t.Fatalf("EarliestFlushAt = (_, %v, %v)", ok, err)
	}
	if at != 500 {
		t.Errorf("EarliestFlushAt(rule-1) = %d, want 500 — rule-2's earlier deadline must not leak across rules", at)
	}
}

func TestAlertPendingRepo_DeleteByRuleClearsOnlyThatRule(t *testing.T) {
	repo, ctx := newPendingTestRepo(t)
	for _, row := range []AlertPending{
		{RuleID: "rule-1", FindingID: "a", FindingJSON: "{}", Kind: "new", At: 1, FlushAt: 10},
		{RuleID: "rule-2", FindingID: "b", FindingJSON: "{}", Kind: "new", At: 2, FlushAt: 10},
	} {
		if _, err := repo.Insert(ctx, row); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}
	if err := repo.DeleteByRule(ctx, "rule-1"); err != nil {
		t.Fatalf("DeleteByRule: %v", err)
	}
	due, err := repo.Due(ctx, 100)
	if err != nil {
		t.Fatalf("Due: %v", err)
	}
	if len(due) != 1 || due[0].RuleID != "rule-2" {
		t.Errorf("after deleting rule-1's queue, due = %+v; want only rule-2's row", due)
	}
}

// TestAlertRuleRepo_SchedulingColumnsRoundTrip covers the T-2407 columns
// added to an existing table, including the two that are NULL when unset.
func TestAlertRuleRepo_SchedulingColumnsRoundTrip(t *testing.T) {
	db := openTestDB(t)
	repo := NewAlertRuleRepo(db)
	ctx := context.Background()

	full := AlertRule{
		ID: "r1", Name: "night", Enabled: true,
		TargetKind: "generic", TargetURL: "https://example.test/hook",
		CreatedAt: 1, UpdatedAt: 1,
		QuietStart: "22:00", QuietEnd: "06:00", QuietTZ: "Europe/Bucharest",
		QuietBypassError: true, DigestWindowSec: 300,
	}
	if err := repo.Insert(ctx, full); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, err := repo.Get(ctx, "r1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.QuietStart != "22:00" || got.QuietEnd != "06:00" || got.QuietTZ != "Europe/Bucharest" {
		t.Errorf("quiet hours round-tripped as %q-%q in %q", got.QuietStart, got.QuietEnd, got.QuietTZ)
	}
	if !got.QuietBypassError || got.DigestWindowSec != 300 {
		t.Errorf("bypass/digest round-tripped as %v/%d, want true/300", got.QuietBypassError, got.DigestWindowSec)
	}

	// Unset scheduling: the three text columns are NULL and must scan as "",
	// not as an error.
	bare := AlertRule{
		ID: "r2", Name: "always", Enabled: true,
		TargetKind: "generic", TargetURL: "https://example.test/hook2",
		CreatedAt: 1, UpdatedAt: 1,
	}
	if insErr := repo.Insert(ctx, bare); insErr != nil {
		t.Fatalf("Insert bare: %v", insErr)
	}
	got, err = repo.Get(ctx, "r2")
	if err != nil {
		t.Fatalf("Get bare: %v", err)
	}
	if got.QuietStart != "" || got.QuietEnd != "" || got.QuietTZ != "" || got.DigestWindowSec != 0 {
		t.Errorf("an unscheduled rule read back as %+v; unset must stay unset", got)
	}

	// Update must carry the columns too — an update that silently dropped
	// them would clear an operator's quiet hours on any unrelated edit.
	full.Name = "renamed"
	full.UpdatedAt = 2
	if updErr := repo.Update(ctx, full); updErr != nil {
		t.Fatalf("Update: %v", updErr)
	}
	got, err = repo.Get(ctx, "r1")
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if got.QuietStart != "22:00" || got.DigestWindowSec != 300 {
		t.Errorf("update dropped the scheduling columns: %+v", got)
	}
}
