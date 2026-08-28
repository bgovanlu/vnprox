// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"testing"
)

func TestGuestInteriorToggleRepo_DefaultOff(t *testing.T) {
	db := openTestDB(t)
	repo := NewGuestInteriorToggleRepo(db)
	ctx := context.Background()

	enabled, err := repo.Get(ctx, "guest:pve1:200")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if enabled {
		t.Errorf("Get() on a never-toggled guest = true, want false (off by default)")
	}
}

func TestGuestInteriorToggleRepo_SetAndGet(t *testing.T) {
	db := openTestDB(t)
	repo := NewGuestInteriorToggleRepo(db)
	ctx := context.Background()
	ref := "guest:pve1:200"

	if err := repo.Set(ctx, ref, true, "root@pam", 100); err != nil {
		t.Fatalf("Set(true): %v", err)
	}
	enabled, err := repo.Get(ctx, ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !enabled {
		t.Errorf("Get() after Set(true) = false, want true")
	}

	// Set is an upsert: toggling back off overwrites in place, not a
	// second row.
	if setErr := repo.Set(ctx, ref, false, "root@pam", 200); setErr != nil {
		t.Fatalf("Set(false): %v", setErr)
	}
	enabled, err = repo.Get(ctx, ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if enabled {
		t.Errorf("Get() after Set(false) = true, want false")
	}
}
