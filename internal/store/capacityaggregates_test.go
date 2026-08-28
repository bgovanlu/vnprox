// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"testing"
	"time"
)

func day(unixDay int64) int64 { return unixDay * 86400 }

func TestCapacityAggregateRepo_UpsertIdempotent(t *testing.T) {
	db := openTestDB(t)
	repo := NewCapacityAggregateRepo(db)
	ctx := context.Background()

	a := CapacityAggregate{Ref: "iface:pve1:vmbr1", Kind: CapacityKindLink, BucketAt: day(19000), AvgUtilization: 40, MaxUtilization: 55, CreatedAt: 100}
	if err := repo.Upsert(ctx, a); err != nil {
		t.Fatalf("Upsert #1: %v", err)
	}
	// Re-running the same day's rollup with refined values upserts, never
	// duplicates (AC5 idempotency).
	a.AvgUtilization, a.MaxUtilization, a.CreatedAt = 42, 60, 200
	if err := repo.Upsert(ctx, a); err != nil {
		t.Fatalf("Upsert #2: %v", err)
	}

	all, err := repo.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("ListAll len = %d, want 1 (upsert must not duplicate)", len(all))
	}
	if all[0].MaxUtilization != 60 || all[0].AvgUtilization != 42 || all[0].CreatedAt != 200 {
		t.Errorf("row = %+v, want the recomputed values (avg 42, max 60, createdAt 200)", all[0])
	}
}

func TestCapacityAggregateRepo_Exists(t *testing.T) {
	db := openTestDB(t)
	repo := NewCapacityAggregateRepo(db)
	ctx := context.Background()

	ok, err := repo.Exists(ctx, "10.0.0.0/24", CapacityKindIPAMPool, day(19000))
	if err != nil {
		t.Fatalf("Exists (absent): %v", err)
	}
	if ok {
		t.Fatal("Exists returned true for a row that was never written")
	}
	if insErr := repo.Upsert(ctx, CapacityAggregate{Ref: "10.0.0.0/24", Kind: CapacityKindIPAMPool, BucketAt: day(19000), AvgUtilization: 10, MaxUtilization: 10, CreatedAt: 1}); insErr != nil {
		t.Fatalf("Upsert: %v", insErr)
	}
	ok, err = repo.Exists(ctx, "10.0.0.0/24", CapacityKindIPAMPool, day(19000))
	if err != nil {
		t.Fatalf("Exists (present): %v", err)
	}
	if !ok {
		t.Fatal("Exists returned false for a row that was written")
	}
}

func TestCapacityAggregateRepo_ListByRefSince(t *testing.T) {
	db := openTestDB(t)
	repo := NewCapacityAggregateRepo(db)
	ctx := context.Background()

	for _, d := range []int64{18990, 18995, 19000} {
		if err := repo.Upsert(ctx, CapacityAggregate{Ref: "iface:pve1:vmbr1", Kind: CapacityKindLink, BucketAt: day(d), AvgUtilization: 1, MaxUtilization: 1, CreatedAt: 1}); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}
	// A different ref must not leak into the query.
	if err := repo.Upsert(ctx, CapacityAggregate{Ref: "iface:pve1:vmbr0", Kind: CapacityKindLink, BucketAt: day(19000), AvgUtilization: 9, MaxUtilization: 9, CreatedAt: 1}); err != nil {
		t.Fatalf("Upsert other ref: %v", err)
	}

	got, err := repo.ListByRefSince(ctx, "iface:pve1:vmbr1", CapacityKindLink, day(18995))
	if err != nil {
		t.Fatalf("ListByRefSince: %v", err)
	}
	if len(got) != 2 || got[0].BucketAt != day(18995) || got[1].BucketAt != day(19000) {
		t.Fatalf("ListByRefSince = %+v, want the two buckets >= 18995 in ascending order", got)
	}
}

func TestCapacityAggregateRepo_PruneRetention(t *testing.T) {
	db := openTestDB(t)
	repo := NewCapacityAggregateRepo(db)
	ctx := context.Background()

	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -401).Truncate(24 * time.Hour)  // just past the 400-day cap
	keep := now.AddDate(0, 0, -399).Truncate(24 * time.Hour) // just inside it

	for _, at := range []int64{old.Unix(), keep.Unix()} {
		if err := repo.Upsert(ctx, CapacityAggregate{Ref: "iface:pve1:vmbr1", Kind: CapacityKindLink, BucketAt: at, AvgUtilization: 1, MaxUtilization: 1, CreatedAt: 1}); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}

	n, err := repo.PruneRetention(ctx, now, DefaultCapacityRetentionDays)
	if err != nil {
		t.Fatalf("PruneRetention: %v", err)
	}
	if n != 1 {
		t.Fatalf("PruneRetention removed %d rows, want 1 (only the >400-day-old bucket)", n)
	}
	all, err := repo.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 1 || all[0].BucketAt != keep.Unix() {
		t.Fatalf("survivors = %+v, want only the within-window bucket", all)
	}
}
