// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func insertPosture(t *testing.T, r *PostureScoreRepo, at time.Time, overall int, qualified bool) int64 {
	t.Helper()
	id, err := r.Insert(context.Background(), PostureScore{
		ComputedAt:  at.Unix(),
		Overall:     overall,
		Qualified:   qualified,
		FactorsJSON: `[{"name":"spof_resilience"}]`,
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	return id
}

func TestPostureScoreRepo_LatestAndHistory(t *testing.T) {
	db := openTestDB(t)
	r := NewPostureScoreRepo(db)
	ctx := context.Background()

	if _, err := r.Latest(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Latest on empty table err = %v, want ErrNotFound", err)
	}

	base := time.Unix(1_700_000_000, 0).UTC()
	insertPosture(t, r, base, 60, false)
	insertPosture(t, r, base.Add(24*time.Hour), 70, true)
	insertPosture(t, r, base.Add(48*time.Hour), 80, false)

	latest, err := r.Latest(ctx)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if latest.Overall != 80 || latest.Qualified {
		t.Errorf("Latest = {overall:%d qualified:%v}, want {80 false}", latest.Overall, latest.Qualified)
	}

	hist, err := r.History(ctx, 2)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 2 {
		t.Fatalf("History(2) len = %d, want 2", len(hist))
	}
	if hist[0].Overall != 80 || hist[1].Overall != 70 {
		t.Errorf("History newest-first = [%d,%d], want [80,70]", hist[0].Overall, hist[1].Overall)
	}
}

// TestPostureScoreRepo_RetentionCountBound is T-1607 AC5's count half: only the
// newest DefaultPostureKeepCount computations survive.
func TestPostureScoreRepo_RetentionCountBound(t *testing.T) {
	db := openTestDB(t)
	r := NewPostureScoreRepo(db)
	ctx := context.Background()

	base := time.Unix(1_700_000_000, 0).UTC()
	// Insert keepCount+5 recent (all within the age window) rows.
	const keep = 4
	for i := 0; i < keep+5; i++ {
		insertPosture(t, r, base.Add(time.Duration(i)*time.Hour), 50+i, false)
	}

	if _, err := r.PruneRetention(ctx, base.Add(24*time.Hour), keep, DefaultPostureRetentionDays); err != nil {
		t.Fatalf("PruneRetention: %v", err)
	}
	n, err := r.Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != keep {
		t.Errorf("Count after count-bound prune = %d, want %d", n, keep)
	}
	// The survivors must be the newest ones.
	latest, _ := r.Latest(ctx)
	if latest.Overall != 50+(keep+5-1) {
		t.Errorf("newest survivor overall = %d, want %d", latest.Overall, 50+(keep+5-1))
	}
}

// TestPostureScoreRepo_RetentionAgeBound is AC5's age half: rows older than
// keepDays are pruned regardless of count.
func TestPostureScoreRepo_RetentionAgeBound(t *testing.T) {
	db := openTestDB(t)
	r := NewPostureScoreRepo(db)
	ctx := context.Background()

	now := time.Unix(1_700_000_000, 0).UTC()
	// One row 500 days old, one row 10 days old.
	insertPosture(t, r, now.AddDate(0, 0, -500), 40, false)
	fresh := insertPosture(t, r, now.AddDate(0, 0, -10), 90, false)

	if _, err := r.PruneRetention(ctx, now, DefaultPostureKeepCount, 400); err != nil {
		t.Fatalf("PruneRetention: %v", err)
	}
	hist, err := r.History(ctx, 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 1 || hist[0].ID != fresh {
		t.Errorf("after age-bound prune, survivors = %+v, want only the 10-day-old row (id %d)", hist, fresh)
	}
}

// TestPostureScoreRepo_DeleteInRangeIdempotent is AC5's idempotency half: the
// scheduled job clears the current day before inserting, so re-running the same
// day's computation never duplicates a row.
func TestPostureScoreRepo_DeleteInRangeIdempotent(t *testing.T) {
	db := openTestDB(t)
	r := NewPostureScoreRepo(db)
	ctx := context.Background()

	day := time.Unix(1_700_000_000, 0).UTC().Truncate(24 * time.Hour)
	from, to := day.Unix(), day.Add(24*time.Hour).Unix()

	// First computation of the day.
	if _, err := r.DeleteInRange(ctx, from, to); err != nil {
		t.Fatalf("DeleteInRange: %v", err)
	}
	insertPosture(t, r, day.Add(1*time.Hour), 60, false)

	// Re-run the same day: clear then insert again.
	if _, err := r.DeleteInRange(ctx, from, to); err != nil {
		t.Fatalf("DeleteInRange (rerun): %v", err)
	}
	insertPosture(t, r, day.Add(2*time.Hour), 65, true)

	n, err := r.Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 1 {
		t.Errorf("Count after same-day re-run = %d, want 1 (no duplicate row)", n)
	}
	latest, _ := r.Latest(ctx)
	if latest.Overall != 65 {
		t.Errorf("latest overall = %d, want 65 (the recomputed value)", latest.Overall)
	}
}
