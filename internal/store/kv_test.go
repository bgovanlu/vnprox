package store

import (
	"context"
	"errors"
	"testing"
)

func TestKVRepo_RoundTrip(t *testing.T) {
	db := openTestDB(t)
	repo := NewKVRepo(db)
	ctx := context.Background()

	if err := repo.Insert(ctx, "greeting", "hello"); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.Get(ctx, "greeting")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "hello" {
		t.Errorf("Get() = %q, want %q", got, "hello")
	}

	if insertErr := repo.Insert(ctx, "other", "value"); insertErr != nil {
		t.Fatalf("Insert #2: %v", insertErr)
	}

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// schema_version is also present (set by migrate), so expect >= 2 keys.
	if len(all) < 2 || all["greeting"] != "hello" || all["other"] != "value" {
		t.Errorf("List() = %+v, want to contain greeting=hello, other=value", all)
	}

	if updateErr := repo.Update(ctx, "greeting", "goodbye"); updateErr != nil {
		t.Fatalf("Update: %v", updateErr)
	}
	got, err = repo.Get(ctx, "greeting")
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if got != "goodbye" {
		t.Errorf("Get() after Update = %q, want %q", got, "goodbye")
	}

	if err := repo.Delete(ctx, "greeting"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, "greeting"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete: got %v, want ErrNotFound", err)
	}
}

func TestKVRepo_Set_Upserts(t *testing.T) {
	db := openTestDB(t)
	repo := NewKVRepo(db)
	ctx := context.Background()

	if err := repo.Set(ctx, "k", "v1"); err != nil {
		t.Fatalf("Set (insert): %v", err)
	}
	if err := repo.Set(ctx, "k", "v2"); err != nil {
		t.Fatalf("Set (update): %v", err)
	}
	got, err := repo.Get(ctx, "k")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "v2" {
		t.Errorf("Get() = %q, want %q", got, "v2")
	}
}

func TestKVRepo_GetNotFound(t *testing.T) {
	db := openTestDB(t)
	repo := NewKVRepo(db)
	if _, err := repo.Get(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(missing): got %v, want ErrNotFound", err)
	}
}

func TestKVRepo_UpdateNotFound(t *testing.T) {
	db := openTestDB(t)
	repo := NewKVRepo(db)
	if err := repo.Update(context.Background(), "nope", "v"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update(missing): got %v, want ErrNotFound", err)
	}
}

func TestKVRepo_SchemaVersionIsPresentAfterOpen(t *testing.T) {
	db := openTestDB(t)
	repo := NewKVRepo(db)

	v, err := repo.Get(context.Background(), schemaVersionKey)
	if err != nil {
		t.Fatalf("Get(schema_version): %v", err)
	}
	if v != "9" {
		t.Errorf("schema_version = %q, want %q", v, "9")
	}
}
