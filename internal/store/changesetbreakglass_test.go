// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"testing"
)

func TestChangesetBreakGlassRepo_GetMissingIsNotFound(t *testing.T) {
	db := openTestDB(t)
	repo := NewChangesetBreakGlassRepo(db)
	if _, err := repo.Get(context.Background(), "cs1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(never invoked): err = %v, want ErrNotFound", err)
	}
}

func TestChangesetBreakGlassRepo_UpsertGetList(t *testing.T) {
	db := openTestDB(t)
	repo := NewChangesetBreakGlassRepo(db)
	ctx := context.Background()
	seedChangesetForApproval(t, db, "cs1")
	seedChangesetForApproval(t, db, "cs2")

	first := ChangesetBreakGlass{
		ChangesetID: "cs1", Reason: "corosync down", InvokedBy: "alice",
		InvokedAt: 100, OpsFingerprint: "abc",
	}
	if err := repo.Upsert(ctx, first); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, getErr := repo.Get(ctx, "cs1")
	if getErr != nil {
		t.Fatalf("Get: %v", getErr)
	}
	if got != first {
		t.Fatalf("Get = %+v, want %+v", got, first)
	}

	// Re-invoking after an edit replaces the row, fingerprint included — a
	// second override is a second event, and its 24-hour floor restarts.
	second := ChangesetBreakGlass{
		ChangesetID: "cs1", Reason: "still down after the edit", InvokedBy: "bob",
		InvokedAt: 200, OpsFingerprint: "def",
	}
	if err := repo.Upsert(ctx, second); err != nil {
		t.Fatalf("re-Upsert: %v", err)
	}
	got, getErr = repo.Get(ctx, "cs1")
	if getErr != nil {
		t.Fatalf("Get after re-Upsert: %v", getErr)
	}
	if got != second {
		t.Fatalf("Get = %+v, want the replacement %+v", got, second)
	}

	if err := repo.Upsert(ctx, ChangesetBreakGlass{
		ChangesetID: "cs2", Reason: "other incident", InvokedBy: "carol", InvokedAt: 300,
	}); err != nil {
		t.Fatalf("Upsert(cs2): %v", err)
	}
	list, listErr := repo.List(ctx)
	if listErr != nil {
		t.Fatalf("List: %v", listErr)
	}
	if len(list) != 2 || list[0].ChangesetID != "cs2" {
		t.Fatalf("List = %+v, want two rows, newest first", list)
	}
}

// The break-glass record is evidence: nothing but the changeset's own
// deletion may remove it, because the finding it raises — and that finding's
// 24-hour acknowledgement floor — is computed from it.
func TestChangesetBreakGlassRepo_CascadesOnlyWithItsChangeset(t *testing.T) {
	db := openTestDB(t)
	repo := NewChangesetBreakGlassRepo(db)
	ctx := context.Background()
	seedChangesetForApproval(t, db, "cs1")
	if err := repo.Upsert(ctx, ChangesetBreakGlass{
		ChangesetID: "cs1", Reason: "incident 42", InvokedBy: "alice", InvokedAt: 100,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM changesets WHERE id = 'cs1'`); err != nil {
		t.Fatalf("deleting the changeset: %v", err)
	}
	if _, err := repo.Get(ctx, "cs1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after the changeset was deleted: err = %v, want ErrNotFound", err)
	}
}
