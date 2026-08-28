// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"testing"
)

func TestMaintenanceWindowRepo_GetMissingIsNotFound(t *testing.T) {
	db := openTestDB(t)
	repo := NewMaintenanceWindowRepo(db)
	if _, err := repo.Get(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(missing): err = %v, want ErrNotFound", err)
	}
}

func TestMaintenanceWindowRepo_CreateGetList(t *testing.T) {
	db := openTestDB(t)
	repo := NewMaintenanceWindowRepo(db)
	ctx := context.Background()

	w := MaintenanceWindow{
		ID: "01J000000000000000000001", Node: "pvecube", Reason: "firmware upgrade",
		CreatedBy: "alice", Zone: "America/New_York", Start: 1000, End: 2000, CreatedAt: 500,
	}
	if err := repo.Create(ctx, w); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := repo.Get(ctx, w.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != w {
		t.Fatalf("Get = %+v, want %+v", got, w)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0] != w {
		t.Fatalf("List = %+v, want [%+v]", list, w)
	}
}

func TestMaintenanceWindowRepo_ListOrdersByStart(t *testing.T) {
	db := openTestDB(t)
	repo := NewMaintenanceWindowRepo(db)
	ctx := context.Background()

	later := MaintenanceWindow{ID: "b", Node: "n1", CreatedBy: "x", Zone: "UTC", Start: 200, End: 300, CreatedAt: 1}
	earlier := MaintenanceWindow{ID: "a", Node: "n1", CreatedBy: "x", Zone: "UTC", Start: 100, End: 150, CreatedAt: 1}
	if err := repo.Create(ctx, later); err != nil {
		t.Fatalf("Create later: %v", err)
	}
	if err := repo.Create(ctx, earlier); err != nil {
		t.Fatalf("Create earlier: %v", err)
	}
	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 || list[0].ID != "a" || list[1].ID != "b" {
		t.Fatalf("List = %+v, want [a, b] ordered by start_epoch", list)
	}
}

func TestMaintenanceWindowRepo_ListEmptyIsNotNil(t *testing.T) {
	db := openTestDB(t)
	repo := NewMaintenanceWindowRepo(db)
	list, err := repo.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("List = %+v, want empty", list)
	}
}

func TestMaintenanceWindowRepo_DeleteAbsentIsNotAnError(t *testing.T) {
	db := openTestDB(t)
	repo := NewMaintenanceWindowRepo(db)
	if err := repo.Delete(context.Background(), "does-not-exist"); err != nil {
		t.Fatalf("Delete(absent): %v, want nil", err)
	}
}

func TestMaintenanceWindowRepo_Delete(t *testing.T) {
	db := openTestDB(t)
	repo := NewMaintenanceWindowRepo(db)
	ctx := context.Background()
	w := MaintenanceWindow{ID: "a", Node: "n1", CreatedBy: "x", Zone: "UTC", Start: 100, End: 150, CreatedAt: 1}
	if err := repo.Create(ctx, w); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.Delete(ctx, w.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, w.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after Delete: err = %v, want ErrNotFound", err)
	}
}
