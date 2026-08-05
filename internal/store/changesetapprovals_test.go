package store

import (
	"context"
	"errors"
	"testing"
)

func seedChangesetForApproval(t *testing.T, db *DB, id string) {
	t.Helper()
	repo := NewChangesetRepo(db)
	if err := repo.Insert(context.Background(), Changeset{
		ID: id, Author: "alice", Status: "draft", OpsJSON: "[]", CreatedAt: 1, UpdatedAt: 1,
	}); err != nil {
		t.Fatalf("seeding changeset %s: %v", id, err)
	}
}

func TestChangesetApprovalRepo_GetMissingIsNotFound(t *testing.T) {
	db := openTestDB(t)
	repo := NewChangesetApprovalRepo(db)
	if _, err := repo.Get(context.Background(), "cs1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(never decided): err = %v, want ErrNotFound", err)
	}
}

func TestChangesetApprovalRepo_UpsertGetClear(t *testing.T) {
	db := openTestDB(t)
	repo := NewChangesetApprovalRepo(db)
	ctx := context.Background()
	seedChangesetForApproval(t, db, "cs1")

	a := ChangesetApproval{ChangesetID: "cs1", Status: "approved", DecidedBy: "bob", DecidedAt: 100}
	if err := repo.Upsert(ctx, a); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := repo.Get(ctx, "cs1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != a {
		t.Errorf("Get() = %+v, want %+v", got, a)
	}

	// A later decision replaces the earlier one — only the latest is kept.
	rejected := ChangesetApproval{ChangesetID: "cs1", Status: "rejected", DecidedBy: "carol", Reason: "needs another look", DecidedAt: 200}
	if upsertErr := repo.Upsert(ctx, rejected); upsertErr != nil {
		t.Fatalf("Upsert (replace): %v", upsertErr)
	}
	got, err = repo.Get(ctx, "cs1")
	if err != nil {
		t.Fatalf("Get after replace: %v", err)
	}
	if got != rejected {
		t.Errorf("Get() after replace = %+v, want %+v", got, rejected)
	}

	if err := repo.Clear(ctx, "cs1"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, err := repo.Get(ctx, "cs1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Clear: err = %v, want ErrNotFound", err)
	}
	// Clearing an already-absent decision is not an error.
	if err := repo.Clear(ctx, "cs1"); err != nil {
		t.Errorf("Clear (already gone): %v", err)
	}
}

func TestChangesetApprovalRepo_UpsertRejectsUnknownChangeset(t *testing.T) {
	db := openTestDB(t)
	repo := NewChangesetApprovalRepo(db)
	err := repo.Upsert(context.Background(), ChangesetApproval{ChangesetID: "no-such-cs", Status: "approved", DecidedBy: "bob", DecidedAt: 1})
	if err == nil {
		t.Fatal("Upsert with unknown changeset_id should fail the foreign key constraint")
	}
}

func TestChangesetApprovalRepo_CascadeDeleteOnChangesetDelete(t *testing.T) {
	db := openTestDB(t)
	repo := NewChangesetApprovalRepo(db)
	ctx := context.Background()
	seedChangesetForApproval(t, db, "cs1")
	if err := repo.Upsert(ctx, ChangesetApproval{ChangesetID: "cs1", Status: "approved", DecidedBy: "bob", DecidedAt: 1}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM changesets WHERE id = ?`, "cs1"); err != nil {
		t.Fatalf("deleting changeset: %v", err)
	}
	if _, err := repo.Get(ctx, "cs1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after cascade delete: err = %v, want ErrNotFound", err)
	}
}
