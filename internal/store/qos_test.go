package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func intPtr(n int) *int { return &n }

func TestQosShapeRepo_InsertGetListUpdateDelete(t *testing.T) {
	db := openTestDB(t)
	repo := NewQosShapeRepo(db)
	ctx := context.Background()

	s := QosShape{
		ID: "shape1", Node: "pve1", Bridge: "vmbr0", RateMbit: 10,
		CreatedBy: "root@pam", CreatedAt: 100, UpdatedAt: 100,
	}
	if err := repo.Insert(ctx, s); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.Get(ctx, s.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(got, s) {
		t.Errorf("Get() = %+v, want %+v", got, s)
	}

	s2 := QosShape{
		ID: "shape2", Node: "pve1", Bridge: "vmbr1", MatchCIDR: "10.0.0.0/24", MatchVlan: intPtr(100),
		RateMbit: 5, CeilMbit: intPtr(20), Priority: intPtr(1), CreatedBy: "root@pam", CreatedAt: 200, UpdatedAt: 200,
	}
	if insertErr := repo.Insert(ctx, s2); insertErr != nil {
		t.Fatalf("Insert second: %v", insertErr)
	}
	got2, err := repo.Get(ctx, s2.ID)
	if err != nil {
		t.Fatalf("Get second: %v", err)
	}
	if !reflect.DeepEqual(got2, s2) {
		t.Errorf("Get(second) = %+v, want %+v (nullable fields round-trip)", got2, s2)
	}

	list, err := repo.List(ctx, "pve1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List(pve1) len = %d, want 2", len(list))
	}

	s.RateMbit = 15
	s.CeilMbit = intPtr(30)
	s.UpdatedAt = 150
	if updateErr := repo.Update(ctx, s); updateErr != nil {
		t.Fatalf("Update: %v", updateErr)
	}
	got, err = repo.Get(ctx, s.ID)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if got.RateMbit != 15 || got.CeilMbit == nil || *got.CeilMbit != 30 {
		t.Errorf("Get after update = %+v, want rateMbit=15 ceilMbit=30", got)
	}

	if updateErr := repo.Update(ctx, QosShape{ID: "missing", RateMbit: 1}); !errors.Is(updateErr, ErrNotFound) {
		t.Errorf("Update(missing) error = %v, want ErrNotFound", updateErr)
	}

	if deleteErr := repo.Delete(ctx, s.ID); deleteErr != nil {
		t.Fatalf("Delete: %v", deleteErr)
	}
	if _, getErr := repo.Get(ctx, s.ID); !errors.Is(getErr, ErrNotFound) {
		t.Errorf("Get after delete error = %v, want ErrNotFound", getErr)
	}
	// Deleting an already-absent row is not an error (rollback convergence).
	if deleteErr := repo.Delete(ctx, s.ID); deleteErr != nil {
		t.Errorf("Delete(already-absent) = %v, want nil", deleteErr)
	}

	list, err = repo.List(ctx, "")
	if err != nil {
		t.Fatalf("List(all): %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List(all) len = %d, want 1 (only s2 remains)", len(list))
	}
}
