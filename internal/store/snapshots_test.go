package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestSnapshotRepo_RoundTrip(t *testing.T) {
	db := openTestDB(t)
	changesets := NewChangesetRepo(db)
	repo := NewSnapshotRepo(db)
	ctx := context.Background()

	csID := NewULID()
	if err := changesets.Insert(ctx, Changeset{
		ID: csID, Author: "root@pam", Status: "draft", OpsJSON: "[]", CreatedAt: 1, UpdatedAt: 1,
	}); err != nil {
		t.Fatalf("seeding changeset: %v", err)
	}

	s := Snapshot{
		ID:          "snap-1",
		ChangesetID: sql.NullString{String: csID, Valid: true},
		TakenAt:     100,
		Kind:        "pre",
		FilesJSON:   `[{"node":"pve1","path":"/etc/network/interfaces","sha256":"abc","content_zstd":"..."}]`,
	}
	if err := repo.Insert(ctx, s); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.Get(ctx, s.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != s {
		t.Errorf("Get() = %+v, want %+v", got, s)
	}

	manual := Snapshot{ID: "snap-2", TakenAt: 200, Kind: "manual", FilesJSON: `[]`}
	if insertErr := repo.Insert(ctx, manual); insertErr != nil {
		t.Fatalf("Insert manual (NULL changeset_id): %v", insertErr)
	}

	all, err := repo.List(ctx, "")
	if err != nil {
		t.Fatalf("List(all): %v", err)
	}
	if len(all) != 2 {
		t.Errorf("List(all) len = %d, want 2", len(all))
	}

	byChangeset, err := repo.List(ctx, csID)
	if err != nil {
		t.Fatalf("List(changesetID): %v", err)
	}
	if len(byChangeset) != 1 || byChangeset[0] != s {
		t.Errorf("List(changesetID) = %+v, want [%+v]", byChangeset, s)
	}
}

func TestSnapshotRepo_InsertRejectsUnknownChangeset(t *testing.T) {
	// snapshots.changeset_id REFERENCES changesets(id); with foreign_keys
	// pragma on this must be enforced.
	db := openTestDB(t)
	repo := NewSnapshotRepo(db)
	err := repo.Insert(context.Background(), Snapshot{
		ID:          "snap-bad",
		ChangesetID: sql.NullString{String: "does-not-exist", Valid: true},
		TakenAt:     1,
		Kind:        "pre",
		FilesJSON:   `[]`,
	})
	if err == nil {
		t.Fatal("Insert with unknown changeset_id: got nil error, want a foreign key violation")
	}
}

func TestSnapshotRepo_GetNotFound(t *testing.T) {
	db := openTestDB(t)
	repo := NewSnapshotRepo(db)
	if _, err := repo.Get(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(missing): got %v, want ErrNotFound", err)
	}
}
