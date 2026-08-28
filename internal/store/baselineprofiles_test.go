// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBaselineProfileRepo_PutGetUpsert(t *testing.T) {
	db := openTestDB(t)
	repo := NewBaselineProfileRepo(db)
	ctx := context.Background()

	if _, err := repo.Get(ctx, "guest:pve1:100"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get missing: err = %v, want ErrNotFound", err)
	}

	p := BaselineProfile{Ref: "guest:pve1:100", ProfileJSON: `{"ref":"guest:pve1:100"}`, WindowStart: 100, WindowEnd: 200, UpdatedAt: 200}
	if err := repo.Put(ctx, p); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := repo.Get(ctx, p.Ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != p {
		t.Fatalf("Get = %+v, want %+v", got, p)
	}

	// Re-learn upserts (one row per ref, not history).
	p2 := BaselineProfile{Ref: p.Ref, ProfileJSON: `{"ref":"guest:pve1:100","v":2}`, WindowStart: 300, WindowEnd: 400, UpdatedAt: 400}
	if putErr := repo.Put(ctx, p2); putErr != nil {
		t.Fatalf("Put upsert: %v", putErr)
	}
	got, err = repo.Get(ctx, p.Ref)
	if err != nil {
		t.Fatalf("Get after upsert: %v", err)
	}
	if got != p2 {
		t.Fatalf("after upsert Get = %+v, want %+v", got, p2)
	}
	n, err := repo.Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 1 {
		t.Fatalf("Count = %d, want 1 (upsert, not a second row)", n)
	}
}

// TestBaselineProfileRepo_PruneRetention is T-1601 AC4: profiles older than
// the retention window are pruned; a fresher one survives. It also asserts the
// default retention window is far longer than any flow_samples window, so a
// baseline is never lost before flow_samples' own window has closed on the
// flows it was learned from.
func TestBaselineProfileRepo_PruneRetention(t *testing.T) {
	db := openTestDB(t)
	repo := NewBaselineProfileRepo(db)
	ctx := context.Background()
	now := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)

	old := BaselineProfile{Ref: "guest:pve1:1", ProfileJSON: "{}", WindowStart: 0, WindowEnd: 1, UpdatedAt: now.AddDate(0, 0, -100).Unix()}
	fresh := BaselineProfile{Ref: "guest:pve1:2", ProfileJSON: "{}", WindowStart: 0, WindowEnd: 1, UpdatedAt: now.AddDate(0, 0, -10).Unix()}
	for _, p := range []BaselineProfile{old, fresh} {
		if err := repo.Put(ctx, p); err != nil {
			t.Fatalf("Put %s: %v", p.Ref, err)
		}
	}

	// Default 90-day retention: the 100-day-old profile prunes, the 10-day one
	// survives.
	removed, err := repo.PruneRetention(ctx, now, 0)
	if err != nil {
		t.Fatalf("PruneRetention: %v", err)
	}
	if removed != 1 {
		t.Fatalf("PruneRetention removed %d, want 1", removed)
	}
	if _, err := repo.Get(ctx, old.Ref); !errors.Is(err, ErrNotFound) {
		t.Errorf("old profile survived retention: err = %v", err)
	}
	if _, err := repo.Get(ctx, fresh.Ref); err != nil {
		t.Errorf("fresh profile pruned too early: %v", err)
	}

	// The retention window dwarfs any realistic flow_samples window (minutes/
	// hours), so a learned baseline always outlives the raw flows it summarizes.
	if DefaultBaselineProfileRetentionDays*24*time.Hour <= 7*24*time.Hour {
		t.Errorf("default baseline retention (%d days) is not comfortably longer than flow_samples' window", DefaultBaselineProfileRetentionDays)
	}
}

// TestBaselineProfileRepo_PruneCadenceMatchesMetricSamples asserts the prune
// loop uses the same tick-based cadence contract MetricSampleRepo.RunPruneLoop
// establishes: it returns nil promptly on context cancellation without needing
// a tick to fire.
func TestBaselineProfileRepo_PruneCadenceMatchesMetricSamples(t *testing.T) {
	db := openTestDB(t)
	repo := NewBaselineProfileRepo(db)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { done <- repo.RunPruneLoop(ctx, time.Hour, 90, nil) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunPruneLoop on cancelled ctx = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunPruneLoop did not return promptly on context cancellation")
	}
}
