package store

import (
	"context"
	"errors"
	"testing"
)

// seedStageChangeset inserts the changesets row changeset_apply_stages' FK
// requires, so these tests exercise the real constrained table rather than a
// detached one.
func seedStageChangeset(t *testing.T, db *DB, id string) {
	t.Helper()
	repo := NewChangesetRepo(db)
	if err := repo.Insert(context.Background(), Changeset{
		ID: id, Title: "staged", Author: "brian", Status: "applying",
		OpsJSON: "[]", CreatedAt: 1700000000, UpdatedAt: 1700000000,
	}); err != nil {
		t.Fatalf("seeding changeset %s: %v", id, err)
	}
}

func TestChangesetStageRepo_RoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	seedStageChangeset(t, db, "cs-1")
	repo := NewChangesetStageRepo(db)

	if _, err := repo.Get(ctx, "cs-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get before any Upsert = %v, want ErrNotFound — a changeset with no staged pause must be reported as having none, not as an empty pause", err)
	}

	row := ChangesetApplyStage{
		ChangesetID: "cs-1", State: StageCanaryHold,
		StrategyJSON:     `{"mode":"canary","gate":"manual","canaryNodes":["pve1"],"holdForSec":60}`,
		AppliedNodesJSON: `["pve1"]`, PendingNodesJSON: `["pve2","pve3"]`,
		Author: "brian", HoldStartedAt: 1700000100, HoldDeadline: 1700000160, ConfirmDeadline: 1700000220,
	}
	if uerr := repo.Upsert(ctx, row); uerr != nil {
		t.Fatalf("Upsert: %v", uerr)
	}

	got, err := repo.Get(ctx, "cs-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != row {
		t.Errorf("Get = %+v, want %+v", got, row)
	}

	// The promotion transition is an in-place state flip on the same row —
	// there must never be two rows describing one changeset's pause.
	row.State = StagePromoting
	if uerr := repo.Upsert(ctx, row); uerr != nil {
		t.Fatalf("Upsert (promote): %v", uerr)
	}
	got, err = repo.Get(ctx, "cs-1")
	if err != nil {
		t.Fatalf("Get after promote: %v", err)
	}
	if got.State != StagePromoting {
		t.Errorf("State = %q, want %q", got.State, StagePromoting)
	}
	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("List returned %d rows, want 1 (Upsert must replace, never accumulate)", len(all))
	}

	if derr := repo.Delete(ctx, "cs-1"); derr != nil {
		t.Fatalf("Delete: %v", derr)
	}
	if _, err := repo.Get(ctx, "cs-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after Delete = %v, want ErrNotFound", err)
	}
	// Every terminal path deletes unconditionally, so deleting nothing must
	// not be an error.
	if derr := repo.Delete(ctx, "cs-1"); derr != nil {
		t.Errorf("Delete of an absent row = %v, want nil", derr)
	}
}

func TestChangesetStageRepo_ListOrdersOldestHoldFirst(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	repo := NewChangesetStageRepo(db)

	for _, tc := range []struct {
		id      string
		started int64
	}{
		{id: "cs-late", started: 1700000900},
		{id: "cs-early", started: 1700000100},
		{id: "cs-mid", started: 1700000500},
	} {
		seedStageChangeset(t, db, tc.id)
		if err := repo.Upsert(ctx, ChangesetApplyStage{
			ChangesetID: tc.id, State: StageCanaryHold, StrategyJSON: `{"mode":"canary"}`,
			AppliedNodesJSON: `[]`, PendingNodesJSON: `[]`, HoldStartedAt: tc.started,
		}); err != nil {
			t.Fatalf("Upsert %s: %v", tc.id, err)
		}
	}

	rows, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"cs-early", "cs-mid", "cs-late"}
	if len(rows) != len(want) {
		t.Fatalf("List returned %d rows, want %d", len(rows), len(want))
	}
	for i, id := range want {
		if rows[i].ChangesetID != id {
			t.Errorf("List[%d] = %q, want %q", i, rows[i].ChangesetID, id)
		}
	}
}
