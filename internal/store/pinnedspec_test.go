// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestPinnedSpecRepo_GetNotFoundWhenUnset(t *testing.T) {
	db := openTestDB(t)
	repo := NewPinnedSpecRepo(db)
	if _, err := repo.Get(context.Background()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() err = %v, want ErrNotFound", err)
	}
}

func TestPinnedSpecRepo_SetAndGet(t *testing.T) {
	db := openTestDB(t)
	repo := NewPinnedSpecRepo(db)
	ctx := context.Background()

	ps := PinnedSpec{Content: "specVersion: 1\n", PinnedBy: "root@pam", PinnedAt: 100}
	if err := repo.Set(ctx, ps); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != ps {
		t.Errorf("Get() = %+v, want %+v", got, ps)
	}
}

func TestPinnedSpecRepo_SetOverwritesSingletonRow(t *testing.T) {
	db := openTestDB(t)
	repo := NewPinnedSpecRepo(db)
	ctx := context.Background()

	if err := repo.Set(ctx, PinnedSpec{Content: "specVersion: 1\n# first\n", PinnedBy: "alice@pam", PinnedAt: 100}); err != nil {
		t.Fatalf("Set (first): %v", err)
	}
	second := PinnedSpec{Content: "specVersion: 1\n# second\n", PinnedBy: "bob@pam", PinnedAt: 200}
	if err := repo.Set(ctx, second); err != nil {
		t.Fatalf("Set (second): %v", err)
	}
	got, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// Re-pinning replaces the row in place — there is exactly one pin ever,
	// never a history of past pins.
	if got != second {
		t.Errorf("Get() = %+v, want the second pin %+v", got, second)
	}
}

func TestPinnedSpecRepo_Clear(t *testing.T) {
	db := openTestDB(t)
	repo := NewPinnedSpecRepo(db)
	ctx := context.Background()

	if err := repo.Set(ctx, PinnedSpec{Content: "specVersion: 1\n", PinnedBy: "root@pam", PinnedAt: 100}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := repo.Clear(ctx); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, err := repo.Get(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() after Clear err = %v, want ErrNotFound", err)
	}
	// Clearing an already-unset pin is not an error.
	if err := repo.Clear(ctx); err != nil {
		t.Fatalf("Clear (already unset): %v", err)
	}
}

func TestPinnedSpecRepo_SurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vnprox.db")
	ctx := context.Background()

	db1, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ps := PinnedSpec{Content: "specVersion: 1\n", PinnedBy: "root@pam", PinnedAt: 100}
	if setErr := NewPinnedSpecRepo(db1).Set(ctx, ps); setErr != nil {
		t.Fatalf("Set: %v", setErr)
	}
	if closeErr := db1.Close(); closeErr != nil {
		t.Fatalf("Close: %v", closeErr)
	}

	db2, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close() })

	got, err := NewPinnedSpecRepo(db2).Get(ctx)
	if err != nil {
		t.Fatalf("Get after restart: %v", err)
	}
	if got != ps {
		t.Errorf("Get() after restart = %+v, want %+v", got, ps)
	}
}
