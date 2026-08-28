// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestMetricSampleRepo_RoundTrip(t *testing.T) {
	db := openTestDB(t)
	repo := NewMetricSampleRepo(db)
	ctx := context.Background()

	s := MetricSample{
		Ref:     "node/pve1/iface/vmbr0",
		At:      1000,
		RxBytes: sql.NullInt64{Int64: 111, Valid: true},
		TxBytes: sql.NullInt64{Int64: 222, Valid: true},
		RxPkts:  sql.NullInt64{Int64: 1, Valid: true},
		TxPkts:  sql.NullInt64{Int64: 2, Valid: true},
		RxErrs:  sql.NullInt64{Int64: 0, Valid: true},
		TxErrs:  sql.NullInt64{Int64: 0, Valid: true},
		RxDrop:  sql.NullInt64{Int64: 0, Valid: true},
		TxDrop:  sql.NullInt64{Int64: 0, Valid: true},
	}
	if err := repo.Insert(ctx, s); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.Get(ctx, s.Ref, s.At)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != s {
		t.Errorf("Get() = %+v, want %+v", got, s)
	}

	s2 := s
	s2.At = 2000
	s2.RxBytes = sql.NullInt64{Int64: 333, Valid: true}
	if insertErr := repo.Insert(ctx, s2); insertErr != nil {
		t.Fatalf("Insert #2: %v", insertErr)
	}

	list, err := repo.List(ctx, s.Ref, 0, 3000)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List() len = %d, want 2", len(list))
	}
	if list[0].At != 1000 || list[1].At != 2000 {
		t.Errorf("List() order = [%d, %d], want [1000, 2000]", list[0].At, list[1].At)
	}

	// "Update" for a time series is re-inserting the same (ref, at): the
	// ON CONFLICT upsert in Insert covers this.
	s.RxBytes = sql.NullInt64{Int64: 999, Valid: true}
	if insertErr := repo.Insert(ctx, s); insertErr != nil {
		t.Fatalf("Insert (overwrite): %v", insertErr)
	}
	got, err = repo.Get(ctx, s.Ref, s.At)
	if err != nil {
		t.Fatalf("Get after overwrite: %v", err)
	}
	if got.RxBytes.Int64 != 999 {
		t.Errorf("RxBytes after overwrite = %d, want 999", got.RxBytes.Int64)
	}
}

func TestMetricSampleRepo_GetNotFound(t *testing.T) {
	db := openTestDB(t)
	repo := NewMetricSampleRepo(db)
	if _, err := repo.Get(context.Background(), "nope", 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(missing): got %v, want ErrNotFound", err)
	}
}

func TestMetricSampleRepo_Prune(t *testing.T) {
	db := openTestDB(t)
	repo := NewMetricSampleRepo(db)
	ctx := context.Background()

	old := MetricSample{Ref: "r", At: 100}
	recent := MetricSample{Ref: "r", At: 100000}
	if err := repo.Insert(ctx, old); err != nil {
		t.Fatalf("Insert old: %v", err)
	}
	if err := repo.Insert(ctx, recent); err != nil {
		t.Fatalf("Insert recent: %v", err)
	}

	n, err := repo.Prune(ctx, 1000)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 1 {
		t.Errorf("Prune deleted %d rows, want 1", n)
	}

	if _, err := repo.Get(ctx, "r", 100); !errors.Is(err, ErrNotFound) {
		t.Errorf("old sample after Prune: got %v, want ErrNotFound", err)
	}
	if _, err := repo.Get(ctx, "r", 100000); err != nil {
		t.Errorf("recent sample after Prune: got %v, want nil", err)
	}
}

func TestMetricSampleRepo_PruneRetentionUses24h(t *testing.T) {
	db := openTestDB(t)
	repo := NewMetricSampleRepo(db)
	ctx := context.Background()

	now := time.Unix(1_000_000, 0)
	old := MetricSample{Ref: "r", At: now.Add(-25 * time.Hour).Unix()}
	recent := MetricSample{Ref: "r", At: now.Add(-1 * time.Hour).Unix()}
	if err := repo.Insert(ctx, old); err != nil {
		t.Fatalf("Insert old: %v", err)
	}
	if err := repo.Insert(ctx, recent); err != nil {
		t.Fatalf("Insert recent: %v", err)
	}

	n, err := repo.PruneRetention(ctx, now)
	if err != nil {
		t.Fatalf("PruneRetention: %v", err)
	}
	if n != 1 {
		t.Errorf("PruneRetention deleted %d rows, want 1", n)
	}
	if _, err := repo.Get(ctx, "r", old.At); !errors.Is(err, ErrNotFound) {
		t.Errorf("sample older than 24h after PruneRetention: got %v, want ErrNotFound", err)
	}
	if _, err := repo.Get(ctx, "r", recent.At); err != nil {
		t.Errorf("sample within 24h after PruneRetention: got %v, want nil", err)
	}
}

func TestMetricSampleRepo_RunPruneLoop_StopsOnContextCancel(t *testing.T) {
	db := openTestDB(t)
	repo := NewMetricSampleRepo(db)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- repo.RunPruneLoop(ctx, time.Millisecond, nil)
	}()

	// Let it tick at least once, then cancel and make sure it returns
	// promptly (this mirrors the runGroup actor contract: must return once
	// ctx is cancelled).
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("RunPruneLoop returned %v, want nil after cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunPruneLoop did not return within 2s of context cancellation")
	}
}
