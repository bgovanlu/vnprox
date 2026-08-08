package store

import (
	"context"
	"errors"
	"testing"
)

func TestFindingAckRepo_GetMissingIsNotFound(t *testing.T) {
	db := openTestDB(t)
	repo := NewFindingAckRepo(db)
	if _, err := repo.Get(context.Background(), "health:x|y"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(never acked): err = %v, want ErrNotFound", err)
	}
}

func TestFindingAckRepo_UpsertGetDelete(t *testing.T) {
	db := openTestDB(t)
	repo := NewFindingAckRepo(db)
	ctx := context.Background()

	// A realistic id: findings ids embed the check name, a pipe, and
	// comma-joined refs containing colons. Round-tripping the real shape
	// matters more than round-tripping "a".
	const id = "health:mtu_mismatch|bridge:pve1:vmbr0,bridge:pve2:vmbr0"
	a := FindingAck{FindingID: id, Reason: "jumbo on storage only", AckedBy: "brian", AckedAt: 100, ExpiresAt: 500}
	if err := repo.Upsert(ctx, a); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != a {
		t.Fatalf("Get = %+v, want %+v", got, a)
	}

	if err := repo.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after Delete: err = %v, want ErrNotFound", err)
	}
}

func TestFindingAckRepo_UpsertReplacesRatherThanDuplicating(t *testing.T) {
	db := openTestDB(t)
	repo := NewFindingAckRepo(db)
	ctx := context.Background()

	if err := repo.Upsert(ctx, FindingAck{FindingID: "f1", Reason: "first", AckedBy: "alice", AckedAt: 1}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := repo.Upsert(ctx, FindingAck{FindingID: "f1", Reason: "second", AckedBy: "brian", AckedAt: 2, ExpiresAt: 9}); err != nil {
		t.Fatalf("re-Upsert: %v", err)
	}

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("List returned %d rows after two upserts of one id, want 1", len(all))
	}
	got := all["f1"]
	if got.Reason != "second" || got.AckedBy != "brian" || got.ExpiresAt != 9 {
		t.Fatalf("upsert did not replace every field: %+v", got)
	}
}

// The repository must NOT filter expired rows. Expiry is decided at read time
// by internal/findings against a clock it is given; a repository that hid
// expired rows would make that decision untestable and would silently move it
// to a layer with no clock.
func TestFindingAckRepo_ListReturnsExpiredRowsToo(t *testing.T) {
	db := openTestDB(t)
	repo := NewFindingAckRepo(db)
	ctx := context.Background()

	if err := repo.Upsert(ctx, FindingAck{FindingID: "live", Reason: "r", AckedBy: "b", AckedAt: 1, ExpiresAt: 0}); err != nil {
		t.Fatalf("Upsert live: %v", err)
	}
	if err := repo.Upsert(ctx, FindingAck{FindingID: "expired", Reason: "r", AckedBy: "b", AckedAt: 1, ExpiresAt: 2}); err != nil {
		t.Fatalf("Upsert expired: %v", err)
	}

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("List returned %d rows, want 2 — expired rows must still be returned", len(all))
	}
	if _, ok := all["expired"]; !ok {
		t.Fatal("List filtered out an expired row; that decision does not belong in the repository")
	}
}

func TestFindingAckRepo_ListEmptyIsNotNil(t *testing.T) {
	db := openTestDB(t)
	repo := NewFindingAckRepo(db)
	all, err := repo.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if all == nil {
		t.Fatal("List on an empty table returned a nil map; callers index it directly")
	}
	if len(all) != 0 {
		t.Fatalf("List on an empty table returned %d rows", len(all))
	}
}

func TestFindingAckRepo_DeleteAbsentIsNotAnError(t *testing.T) {
	db := openTestDB(t)
	repo := NewFindingAckRepo(db)
	if err := repo.Delete(context.Background(), "never-existed"); err != nil {
		t.Fatalf("Delete(absent): %v", err)
	}
}
