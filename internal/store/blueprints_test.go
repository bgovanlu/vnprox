package store

import (
	"context"
	"errors"
	"testing"
)

func TestBlueprintRepo_RoundTrip(t *testing.T) {
	db := openTestDB(t)
	repo := NewBlueprintRepo(db)
	ctx := context.Background()

	b := Blueprint{ID: "b1", Name: "My bp", BlueprintJSON: `{"blueprintVersion":1}`, CreatedBy: "alice", CreatedAt: 100, UpdatedAt: 100}
	if err := repo.Put(ctx, b); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := repo.Get(ctx, b.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != b {
		t.Errorf("Get() = %+v, want %+v", got, b)
	}

	if _, err := repo.Get(ctx, "no-such-id"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(missing) err = %v, want ErrNotFound", err)
	}
}

func TestBlueprintRepo_Put_UpsertsAndUpdates(t *testing.T) {
	db := openTestDB(t)
	repo := NewBlueprintRepo(db)
	ctx := context.Background()

	b := Blueprint{ID: "b1", Name: "v1", BlueprintJSON: `{"v":1}`, CreatedBy: "alice", CreatedAt: 1, UpdatedAt: 1}
	if err := repo.Put(ctx, b); err != nil {
		t.Fatalf("Put (insert): %v", err)
	}
	b.Name = "v2"
	b.BlueprintJSON = `{"v":2}`
	b.UpdatedAt = 2
	if err := repo.Put(ctx, b); err != nil {
		t.Fatalf("Put (update): %v", err)
	}

	got, err := repo.Get(ctx, "b1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "v2" || got.BlueprintJSON != `{"v":2}` || got.UpdatedAt != 2 {
		t.Errorf("Get() after upsert = %+v, want the updated row", got)
	}
	// CreatedAt/CreatedBy are caller-managed (Put always writes whatever
	// the caller passes — Service.Save is what preserves the original
	// CreatedAt across an edit); this repo-level test just checks the
	// upsert mechanics.
}

func TestBlueprintRepo_List_OrderedByName(t *testing.T) {
	db := openTestDB(t)
	repo := NewBlueprintRepo(db)
	ctx := context.Background()

	for _, b := range []Blueprint{
		{ID: "b2", Name: "Zebra", BlueprintJSON: "{}", CreatedBy: "a", CreatedAt: 1, UpdatedAt: 1},
		{ID: "b1", Name: "Alpha", BlueprintJSON: "{}", CreatedBy: "a", CreatedAt: 1, UpdatedAt: 1},
	} {
		if err := repo.Put(ctx, b); err != nil {
			t.Fatalf("Put(%s): %v", b.ID, err)
		}
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 || list[0].Name != "Alpha" || list[1].Name != "Zebra" {
		t.Fatalf("List() = %+v, want [Alpha, Zebra]", list)
	}
}

func TestBlueprintRepo_Delete(t *testing.T) {
	db := openTestDB(t)
	repo := NewBlueprintRepo(db)
	ctx := context.Background()

	if err := repo.Put(ctx, Blueprint{ID: "b1", Name: "x", BlueprintJSON: "{}", CreatedBy: "a", CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := repo.Delete(ctx, "b1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, "b1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after Delete err = %v, want ErrNotFound", err)
	}
	// Deleting an already-absent row is not an error.
	if err := repo.Delete(ctx, "b1"); err != nil {
		t.Fatalf("Delete (already absent): %v", err)
	}
}
