package store

import (
	"context"
	"errors"
	"testing"
)

func TestAnnotationRepo_InsertGetList(t *testing.T) {
	db := openTestDB(t)
	repo := NewAnnotationRepo(db)
	ctx := context.Background()

	a := Annotation{ID: "01A", Ref: "bridge:pve1:vmbr0", Content: "check this before maintenance", CreatedBy: "alice@pve", CreatedAt: 100, UpdatedAt: 100}
	if err := repo.Insert(ctx, a); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.Get(ctx, a.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != a {
		t.Errorf("Get() = %+v, want %+v", got, a)
	}

	// A second note on the same ref must not overwrite the first — unlike
	// layouts, annotations are additive (one row per note).
	b := Annotation{ID: "01B", Ref: "bridge:pve1:vmbr0", Content: "second note, same entity", CreatedBy: "bob@pve", CreatedAt: 200, UpdatedAt: 200}
	if insertErr := repo.Insert(ctx, b); insertErr != nil {
		t.Fatalf("Insert second: %v", insertErr)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List() len = %d, want 2", len(list))
	}
	if list[0].ID != a.ID || list[1].ID != b.ID {
		t.Errorf("List() order = [%s, %s], want [%s, %s] (created_at ascending)", list[0].ID, list[1].ID, a.ID, b.ID)
	}
}

// TestAnnotationRepo_ExpiryIsStoredNotEnforced is the store half of
// T-2806 AC3: expires_at round-trips, and an expired row is still returned
// by List/Get. Whether a note is expired is decided on the read, by
// internal/annotate's injected clock — never here, against SQLite's own
// notion of now.
func TestAnnotationRepo_ExpiryIsStoredNotEnforced(t *testing.T) {
	db := openTestDB(t)
	repo := NewAnnotationRepo(db)
	ctx := context.Background()

	expired := Annotation{
		ID: "01E", Ref: "bridge:pve1:vmbr0", Content: "expired long ago", CreatedBy: "alice@pve",
		CreatedAt: 100, UpdatedAt: 100, ExpiresAt: 101,
	}
	never := Annotation{
		ID: "01F", Ref: "bridge:pve1:vmbr0", Content: "no expiry", CreatedBy: "alice@pve",
		CreatedAt: 200, UpdatedAt: 200,
	}
	for _, a := range []Annotation{expired, never} {
		if err := repo.Insert(ctx, a); err != nil {
			t.Fatalf("Insert(%s): %v", a.ID, err)
		}
	}

	got, err := repo.Get(ctx, expired.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != expired {
		t.Errorf("Get() = %+v, want %+v", got, expired)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List() len = %d, want 2 — an expired note is still a row", len(list))
	}
	if list[1].ExpiresAt != 0 {
		t.Errorf("a note with no expiry stored expires_at = %d, want 0", list[1].ExpiresAt)
	}
}

func TestAnnotationRepo_GetNotFound(t *testing.T) {
	db := openTestDB(t)
	repo := NewAnnotationRepo(db)
	if _, err := repo.Get(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(missing): got %v, want ErrNotFound", err)
	}
}

func TestAnnotationRepo_Delete(t *testing.T) {
	db := openTestDB(t)
	repo := NewAnnotationRepo(db)
	ctx := context.Background()

	a := Annotation{ID: "01C", Ref: "bond:pve1:bond0", Content: "note", CreatedBy: "alice@pve", CreatedAt: 100, UpdatedAt: 100}
	if err := repo.Insert(ctx, a); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := repo.Delete(ctx, a.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete: got %v, want ErrNotFound", err)
	}
	// Deleting an already-absent annotation is not an error.
	if err := repo.Delete(ctx, "never-existed"); err != nil {
		t.Errorf("Delete(already-absent): %v, want nil", err)
	}
}
