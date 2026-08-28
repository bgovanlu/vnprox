// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"testing"
)

func TestHALeaseRepo_GetBeforeSet_NotFound(t *testing.T) {
	db := openTestDB(t)
	repo := NewHALeaseRepo(db)
	if _, err := repo.Get(context.Background()); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get before Set: got %v, want ErrNotFound", err)
	}
}

func TestHALeaseRepo_SetGet_RoundTrip(t *testing.T) {
	db := openTestDB(t)
	repo := NewHALeaseRepo(db)
	ctx := context.Background()

	want := HALease{Holder: "node-a", Term: 3, ExpiresAt: 1_800_000_030, AcquiredAt: 1_800_000_000, UpdatedAt: 1_800_000_010}
	if err := repo.Set(ctx, want); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != want {
		t.Errorf("Get() = %+v, want %+v", got, want)
	}
}

func TestHALeaseRepo_Set_IsSingletonUpsert(t *testing.T) {
	db := openTestDB(t)
	repo := NewHALeaseRepo(db)
	ctx := context.Background()

	if err := repo.Set(ctx, HALease{Holder: "node-a", Term: 1, ExpiresAt: 100, AcquiredAt: 90, UpdatedAt: 95}); err != nil {
		t.Fatalf("first Set: %v", err)
	}
	// A promotion writes a strictly-higher term; it replaces the row, never
	// inserts a second.
	if err := repo.Set(ctx, HALease{Holder: "node-b", Term: 2, ExpiresAt: 200, AcquiredAt: 190, UpdatedAt: 195}); err != nil {
		t.Fatalf("second Set: %v", err)
	}
	got, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Holder != "node-b" || got.Term != 2 {
		t.Errorf("Get() = %+v, want holder node-b term 2 (singleton replaced)", got)
	}

	var count int
	if err := db.Conn().QueryRowContext(ctx, `SELECT count(*) FROM ha_lease`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("ha_lease row count = %d, want 1 (singleton)", count)
	}
}
