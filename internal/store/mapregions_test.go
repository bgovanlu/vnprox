package store

import (
	"context"
	"errors"
	"testing"
)

func TestMapRegionRepo_InsertGetList(t *testing.T) {
	db := openTestDB(t)
	repo := NewMapRegionRepo(db)
	ctx := context.Background()

	a := MapRegion{
		ID: "01A", Label: "vendor-managed, do not touch", Color: "amber", CreatedBy: "alice@pve",
		X: 10.5, Y: -20.25, W: 300, H: 180, CreatedAt: 100, UpdatedAt: 100,
	}
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

	b := MapRegion{ID: "01B", Label: "lab gear", CreatedBy: "bob@pve", W: 10, H: 10, CreatedAt: 200, UpdatedAt: 200}
	if insertErr := repo.Insert(ctx, b); insertErr != nil {
		t.Fatalf("Insert second: %v", insertErr)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 || list[0].ID != a.ID || list[1].ID != b.ID {
		t.Fatalf("List() = %+v, want [%s, %s] oldest first", list, a.ID, b.ID)
	}
}

func TestMapRegionRepo_GetNotFound(t *testing.T) {
	db := openTestDB(t)
	repo := NewMapRegionRepo(db)
	if _, err := repo.Get(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(missing): got %v, want ErrNotFound", err)
	}
}

func TestMapRegionRepo_Delete(t *testing.T) {
	db := openTestDB(t)
	repo := NewMapRegionRepo(db)
	ctx := context.Background()

	m := MapRegion{ID: "01C", Label: "region", CreatedBy: "alice@pve", W: 1, H: 1, CreatedAt: 100, UpdatedAt: 100}
	if err := repo.Insert(ctx, m); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := repo.Delete(ctx, m.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, m.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete: got %v, want ErrNotFound", err)
	}
	if err := repo.Delete(ctx, "never-existed"); err != nil {
		t.Errorf("Delete(already-absent): %v, want nil", err)
	}
}

// TestMapRegionRepo_ExpiryIsStoredNotEnforced: the repo stores expires_at
// and returns the row regardless — the read-time judgement belongs to
// internal/annotate's injected clock, not to SQL (T-2806 AC3). A List that
// filtered here would put a second clock in the system.
func TestMapRegionRepo_ExpiryIsStoredNotEnforced(t *testing.T) {
	db := openTestDB(t)
	repo := NewMapRegionRepo(db)
	ctx := context.Background()

	m := MapRegion{
		ID: "01D", Label: "expired long ago", CreatedBy: "alice@pve",
		W: 1, H: 1, CreatedAt: 100, UpdatedAt: 100, ExpiresAt: 101,
	}
	if err := repo.Insert(ctx, m); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ExpiresAt != 101 {
		t.Fatalf("List() = %+v, want the expired row returned with expires_at intact", list)
	}
}
