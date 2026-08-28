// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestChangesetRepo_RoundTrip(t *testing.T) {
	db := openTestDB(t)
	repo := NewChangesetRepo(db)
	ctx := context.Background()

	id := NewULID()
	c := Changeset{
		ID:        id,
		Title:     "add vlan 100",
		Author:    "root@pam",
		Status:    "draft",
		Origin:    "ui", // T-1701: Insert defaults an unset origin to 'ui', so round-trip reads it back
		OpsJSON:   `[{"op":"add_vlan","id":100}]`,
		CreatedAt: 100,
		UpdatedAt: 100,
	}
	if err := repo.Insert(ctx, c); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != c {
		t.Errorf("Get() = %+v, want %+v", got, c)
	}

	list, err := repo.List(ctx, "")
	if err != nil {
		t.Fatalf("List(all): %v", err)
	}
	if len(list) != 1 || list[0] != c {
		t.Errorf("List(all) = %+v, want [%+v]", list, c)
	}

	byStatus, err := repo.List(ctx, "draft")
	if err != nil {
		t.Fatalf("List(draft): %v", err)
	}
	if len(byStatus) != 1 {
		t.Errorf("List(draft) len = %d, want 1", len(byStatus))
	}

	noneByStatus, err := repo.List(ctx, "committed")
	if err != nil {
		t.Fatalf("List(committed): %v", err)
	}
	if len(noneByStatus) != 0 {
		t.Errorf("List(committed) len = %d, want 0", len(noneByStatus))
	}

	c.Status = "awaiting_confirm"
	c.FindingsJSON = sql.NullString{String: `[]`, Valid: true}
	c.PlanJSON = sql.NullString{String: `[{"step":1}]`, Valid: true}
	c.ApplyLogJSON = sql.NullString{String: `[{"step":1,"ok":true}]`, Valid: true}
	c.ConfirmDeadline = sql.NullInt64{Int64: 500, Valid: true}
	c.UpdatedAt = 200
	if updateErr := repo.Update(ctx, c); updateErr != nil {
		t.Fatalf("Update: %v", updateErr)
	}

	got, err = repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if got != c {
		t.Errorf("Get() after Update = %+v, want %+v", got, c)
	}
}

func TestChangesetRepo_GetNotFound(t *testing.T) {
	db := openTestDB(t)
	repo := NewChangesetRepo(db)
	if _, err := repo.Get(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(missing): got %v, want ErrNotFound", err)
	}
}

func TestChangesetRepo_UpdateNotFound(t *testing.T) {
	db := openTestDB(t)
	repo := NewChangesetRepo(db)
	err := repo.Update(context.Background(), Changeset{ID: "nope", Author: "x", Status: "draft", OpsJSON: "[]"})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Update(missing): got %v, want ErrNotFound", err)
	}
}

func TestChangesetRepo_IDIsULID(t *testing.T) {
	// changesets.id is documented as a ULID; sanity check NewULID produces
	// the expected 26-character Crockford base32 form and is monotonically
	// sortable when generated in sequence.
	a := NewULID()
	b := NewULID()
	if len(a) != 26 || len(b) != 26 {
		t.Errorf("ULID length = %d/%d, want 26", len(a), len(b))
	}
	if a >= b {
		t.Errorf("ULIDs generated in sequence should sort increasing: %q >= %q", a, b)
	}
}
