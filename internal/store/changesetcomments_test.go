// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"testing"
)

func seedChangesetForComments(t *testing.T, db *DB, id string) {
	t.Helper()
	repo := NewChangesetRepo(db)
	if err := repo.Insert(context.Background(), Changeset{
		ID: id, Author: "alice", Status: "draft", OpsJSON: "[]", CreatedAt: 1, UpdatedAt: 1,
	}); err != nil {
		t.Fatalf("seeding changeset %s: %v", id, err)
	}
}

func TestChangesetCommentRepo_InsertGetListDelete(t *testing.T) {
	db := openTestDB(t)
	repo := NewChangesetCommentRepo(db)
	ctx := context.Background()
	seedChangesetForComments(t, db, "cs1")

	c1 := ChangesetComment{ID: "cm1", ChangesetID: "cs1", OpID: "op1", Author: "alice", Body: "double-check the MTU here", CreatedAt: 100}
	if err := repo.Insert(ctx, c1); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	c2 := ChangesetComment{ID: "cm2", ChangesetID: "cs1", OpID: "", Author: "bob", Body: "looks good overall", CreatedAt: 200}
	if err := repo.Insert(ctx, c2); err != nil {
		t.Fatalf("Insert changeset-level comment: %v", err)
	}

	got, err := repo.Get(ctx, "cm1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != c1 {
		t.Errorf("Get() = %+v, want %+v", got, c1)
	}

	list, err := repo.ListForChangeset(ctx, "cs1")
	if err != nil {
		t.Fatalf("ListForChangeset: %v", err)
	}
	if len(list) != 2 || list[0] != c1 || list[1] != c2 {
		t.Errorf("ListForChangeset() = %+v, want [%+v %+v] oldest-first", list, c1, c2)
	}

	if err := repo.Delete(ctx, "cm1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, "cm1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete: err = %v, want ErrNotFound", err)
	}
	// Deleting an already-absent comment is not an error.
	if err := repo.Delete(ctx, "cm1"); err != nil {
		t.Errorf("Delete (already gone): %v", err)
	}
}

func TestChangesetCommentRepo_GetMissing(t *testing.T) {
	db := openTestDB(t)
	repo := NewChangesetCommentRepo(db)
	if _, err := repo.Get(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(missing): err = %v, want ErrNotFound", err)
	}
}

func TestChangesetCommentRepo_DeleteForOps(t *testing.T) {
	db := openTestDB(t)
	repo := NewChangesetCommentRepo(db)
	ctx := context.Background()
	seedChangesetForComments(t, db, "cs1")
	seedChangesetForComments(t, db, "cs2")

	mustInsert := func(id, csID, opID string) {
		t.Helper()
		if err := repo.Insert(ctx, ChangesetComment{ID: id, ChangesetID: csID, OpID: opID, Author: "alice", Body: "x", CreatedAt: 1}); err != nil {
			t.Fatalf("Insert %s: %v", id, err)
		}
	}
	mustInsert("cm-op1", "cs1", "op1")
	mustInsert("cm-op2", "cs1", "op2")
	mustInsert("cm-op3", "cs1", "op3")
	mustInsert("cm-level", "cs1", "")       // changeset-level: never matched by any op id
	mustInsert("cm-other-cs", "cs2", "op1") // a different changeset's op1 must survive

	n, err := repo.DeleteForOps(ctx, "cs1", []string{"op1", "op2"})
	if err != nil {
		t.Fatalf("DeleteForOps: %v", err)
	}
	if n != 2 {
		t.Fatalf("DeleteForOps() = %d, want 2", n)
	}

	remaining, err := repo.ListForChangeset(ctx, "cs1")
	if err != nil {
		t.Fatalf("ListForChangeset: %v", err)
	}
	if len(remaining) != 2 {
		t.Fatalf("remaining on cs1 = %+v, want 2 rows (op3 and the changeset-level one)", remaining)
	}
	for _, c := range remaining {
		if c.OpID == "op1" || c.OpID == "op2" {
			t.Errorf("comment %+v should have been deleted", c)
		}
	}

	// The other changeset's identically-named op1 comment must be untouched —
	// DeleteForOps is scoped to changeset_id, not a bare op-id match.
	otherList, err := repo.ListForChangeset(ctx, "cs2")
	if err != nil {
		t.Fatalf("ListForChangeset(cs2): %v", err)
	}
	if len(otherList) != 1 || otherList[0].ID != "cm-other-cs" {
		t.Errorf("cs2's comments = %+v, want cm-other-cs untouched", otherList)
	}

	// An empty opIDs slice is a no-op, not an error.
	if n, err := repo.DeleteForOps(ctx, "cs1", nil); err != nil || n != 0 {
		t.Errorf("DeleteForOps(nil) = %d, %v, want 0, nil", n, err)
	}
}

func TestChangesetCommentRepo_CascadeDeleteOnChangesetDelete(t *testing.T) {
	// changeset_comments.changeset_id REFERENCES changesets(id) ON DELETE
	// CASCADE — a comment must never outlive the changeset row it belongs to.
	db := openTestDB(t)
	repo := NewChangesetCommentRepo(db)
	ctx := context.Background()
	seedChangesetForComments(t, db, "cs1")
	if err := repo.Insert(ctx, ChangesetComment{ID: "cm1", ChangesetID: "cs1", Author: "alice", Body: "x", CreatedAt: 1}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// There is no ChangesetRepo.Delete in this codebase (changesets are
	// never hard-deleted, only transitioned to discarded) — exercise the
	// cascade directly at the SQL layer instead, the same way a future hard
	// delete (or a test-only cleanup) would trigger it.
	if _, err := db.ExecContext(ctx, `DELETE FROM changesets WHERE id = ?`, "cs1"); err != nil {
		t.Fatalf("deleting changeset: %v", err)
	}

	if _, err := repo.Get(ctx, "cm1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after cascade delete: err = %v, want ErrNotFound", err)
	}
}

func TestChangesetCommentRepo_InsertRejectsUnknownChangeset(t *testing.T) {
	db := openTestDB(t)
	repo := NewChangesetCommentRepo(db)
	err := repo.Insert(context.Background(), ChangesetComment{ID: "cm1", ChangesetID: "no-such-cs", Author: "alice", Body: "x", CreatedAt: 1})
	if err == nil {
		t.Fatal("Insert with unknown changeset_id should fail the foreign key constraint")
	}
}
