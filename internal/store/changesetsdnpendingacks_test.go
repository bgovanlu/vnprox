package store

import (
	"context"
	"errors"
	"testing"
)

func seedChangesetForSDNPendingAck(t *testing.T, db *DB, id string) {
	t.Helper()
	repo := NewChangesetRepo(db)
	if err := repo.Insert(context.Background(), Changeset{
		ID: id, Author: "alice", Status: "draft", OpsJSON: "[]", CreatedAt: 1, UpdatedAt: 1,
	}); err != nil {
		t.Fatalf("seeding changeset %s: %v", id, err)
	}
}

func TestChangesetSDNPendingAckRepo_GetMissingIsNotFound(t *testing.T) {
	db := openTestDB(t)
	repo := NewChangesetSDNPendingAckRepo(db)
	if _, err := repo.Get(context.Background(), "cs1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(never acknowledged): err = %v, want ErrNotFound", err)
	}
}

func TestChangesetSDNPendingAckRepo_UpsertGetClear(t *testing.T) {
	db := openTestDB(t)
	repo := NewChangesetSDNPendingAckRepo(db)
	ctx := context.Background()
	seedChangesetForSDNPendingAck(t, db, "cs1")

	a := ChangesetSDNPendingAck{
		ChangesetID: "cs1", AcknowledgedBy: "bob",
		EntriesJSON: `[{"kind":"zone","id":"foreignz","state":"new"}]`, AcknowledgedAt: 100,
	}
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

	// A later acknowledgement replaces the earlier one — only the latest is
	// kept (mirrors ChangesetApprovalRepo's own "not a history" contract).
	later := ChangesetSDNPendingAck{
		ChangesetID: "cs1", AcknowledgedBy: "carol",
		EntriesJSON:    `[{"kind":"zone","id":"foreignz","state":"new"},{"kind":"vnet","id":"vtest","state":"changed"}]`,
		AcknowledgedAt: 200,
	}
	if upsertErr := repo.Upsert(ctx, later); upsertErr != nil {
		t.Fatalf("Upsert (replace): %v", upsertErr)
	}
	got, err = repo.Get(ctx, "cs1")
	if err != nil {
		t.Fatalf("Get after replace: %v", err)
	}
	if got != later {
		t.Errorf("Get() after replace = %+v, want %+v", got, later)
	}

	if err := repo.Clear(ctx, "cs1"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, err := repo.Get(ctx, "cs1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Clear: err = %v, want ErrNotFound", err)
	}
	// Clearing an already-absent acknowledgement is not an error.
	if err := repo.Clear(ctx, "cs1"); err != nil {
		t.Errorf("Clear (already gone): %v", err)
	}
}

func TestChangesetSDNPendingAckRepo_UpsertRejectsUnknownChangeset(t *testing.T) {
	db := openTestDB(t)
	repo := NewChangesetSDNPendingAckRepo(db)
	err := repo.Upsert(context.Background(), ChangesetSDNPendingAck{
		ChangesetID: "no-such-cs", AcknowledgedBy: "bob", EntriesJSON: "[]", AcknowledgedAt: 1,
	})
	if err == nil {
		t.Fatal("Upsert with unknown changeset_id should fail the foreign key constraint")
	}
}

func TestChangesetSDNPendingAckRepo_CascadeDeleteOnChangesetDelete(t *testing.T) {
	db := openTestDB(t)
	repo := NewChangesetSDNPendingAckRepo(db)
	ctx := context.Background()
	seedChangesetForSDNPendingAck(t, db, "cs1")
	if err := repo.Upsert(ctx, ChangesetSDNPendingAck{
		ChangesetID: "cs1", AcknowledgedBy: "bob", EntriesJSON: "[]", AcknowledgedAt: 1,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM changesets WHERE id = ?`, "cs1"); err != nil {
		t.Fatalf("deleting changeset: %v", err)
	}
	if _, err := repo.Get(ctx, "cs1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after cascade delete: err = %v, want ErrNotFound", err)
	}
}
