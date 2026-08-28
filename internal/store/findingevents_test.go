// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"testing"
	"time"
)

func TestFindingEventRepo_InsertAndListByTimeRange(t *testing.T) {
	db := openTestDB(t)
	repo := NewFindingEventRepo(db)
	ctx := context.Background()

	events := []FindingEvent{
		{FindingID: "f1", At: 1000, Transition: "new"},
		{FindingID: "f1", At: 2000, Transition: "escalated"},
		{FindingID: "f2", At: 1500, Transition: "new"},
		{FindingID: "f1", At: 3000, Transition: "resolved"},
	}
	for _, e := range events {
		if err := repo.Insert(ctx, e); err != nil {
			t.Fatalf("Insert(%+v): %v", e, err)
		}
	}

	t.Run("unbounded", func(t *testing.T) {
		got, err := repo.ListByTimeRange(ctx, 0, 0)
		if err != nil {
			t.Fatalf("ListByTimeRange: %v", err)
		}
		if len(got) != 4 {
			t.Fatalf("ListByTimeRange(0,0) len = %d, want 4", len(got))
		}
		// Ascending by at.
		for i := 1; i < len(got); i++ {
			if got[i].At < got[i-1].At {
				t.Errorf("results not ascending by at: %+v", got)
			}
		}
	})

	t.Run("bounded both sides", func(t *testing.T) {
		got, err := repo.ListByTimeRange(ctx, 1200, 2500)
		if err != nil {
			t.Fatalf("ListByTimeRange: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("ListByTimeRange(1200,2500) len = %d, want 2 (f2@1500, f1@2000)", len(got))
		}
		if got[0].At != 1500 || got[1].At != 2000 {
			t.Errorf("ListByTimeRange(1200,2500) = %+v, want at 1500 then 2000", got)
		}
	})

	t.Run("from only", func(t *testing.T) {
		got, err := repo.ListByTimeRange(ctx, 2000, 0)
		if err != nil {
			t.Fatalf("ListByTimeRange: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("ListByTimeRange(2000,0) len = %d, want 2 (2000, 3000)", len(got))
		}
	})

	t.Run("to only", func(t *testing.T) {
		got, err := repo.ListByTimeRange(ctx, 0, 1500)
		if err != nil {
			t.Fatalf("ListByTimeRange: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("ListByTimeRange(0,1500) len = %d, want 2 (1000, 1500)", len(got))
		}
	})
}

func TestFindingEventRepo_PruneOlderThan(t *testing.T) {
	db := openTestDB(t)
	repo := NewFindingEventRepo(db)
	ctx := context.Background()

	if err := repo.Insert(ctx, FindingEvent{FindingID: "old", At: 1000, Transition: "new"}); err != nil {
		t.Fatalf("Insert old: %v", err)
	}
	if err := repo.Insert(ctx, FindingEvent{FindingID: "recent", At: 5000, Transition: "new"}); err != nil {
		t.Fatalf("Insert recent: %v", err)
	}

	n, err := repo.PruneOlderThan(ctx, 2000)
	if err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if n != 1 {
		t.Fatalf("PruneOlderThan deleted %d rows, want 1", n)
	}

	got, err := repo.ListByTimeRange(ctx, 0, 0)
	if err != nil {
		t.Fatalf("ListByTimeRange: %v", err)
	}
	if len(got) != 1 || got[0].FindingID != "recent" {
		t.Errorf("ListByTimeRange after prune = %+v, want just 'recent'", got)
	}
}

func TestFindingEventRepo_PruneRetentionUses24h(t *testing.T) {
	db := openTestDB(t)
	repo := NewFindingEventRepo(db)
	ctx := context.Background()

	now := time.Unix(1_000_000, 0)
	old := FindingEvent{FindingID: "old", At: now.Add(-25 * time.Hour).Unix(), Transition: "new"}
	recent := FindingEvent{FindingID: "recent", At: now.Add(-1 * time.Hour).Unix(), Transition: "new"}
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

	got, err := repo.ListByTimeRange(ctx, 0, 0)
	if err != nil {
		t.Fatalf("ListByTimeRange: %v", err)
	}
	if len(got) != 1 || got[0].FindingID != "recent" {
		t.Errorf("ListByTimeRange after PruneRetention = %+v, want just 'recent'", got)
	}
}

func TestFindingEventRepo_RunPruneLoop(t *testing.T) {
	db := openTestDB(t)
	repo := NewFindingEventRepo(db)
	ctx := context.Background()

	now := time.Now()
	if err := repo.Insert(ctx, FindingEvent{FindingID: "old", At: now.Add(-48 * time.Hour).Unix(), Transition: "new"}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- repo.RunPruneLoop(loopCtx, 5*time.Millisecond, func(err error) {
			t.Errorf("RunPruneLoop logged an error: %v", err)
		})
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		got, err := repo.ListByTimeRange(ctx, 0, 0)
		if err != nil {
			t.Fatalf("ListByTimeRange: %v", err)
		}
		if len(got) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("RunPruneLoop did not prune the old row within the deadline")
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	if err := <-done; err != nil {
		t.Errorf("RunPruneLoop returned %v, want nil after context cancel", err)
	}
}

// TestFindingEventRepo_EarliestAt covers the reading T-2706's compliance
// report refuses an out-of-window as-of request with: how far back the
// retained finding history actually reaches, from the table rather than from
// an assumption about the prune loop.
func TestFindingEventRepo_EarliestAt(t *testing.T) {
	db := openTestDB(t)
	repo := NewFindingEventRepo(db)
	ctx := context.Background()

	if _, ok, err := repo.EarliestAt(ctx); err != nil || ok {
		t.Fatalf("EarliestAt on an empty table = (_, %v, %v), want (_, false, nil)", ok, err)
	}

	for _, e := range []FindingEvent{
		{FindingID: "f1", At: 5000, Transition: "new"},
		{FindingID: "f2", At: 1200, Transition: "new"},
		{FindingID: "f3", At: 9000, Transition: "resolved"},
	} {
		if err := repo.Insert(ctx, e); err != nil {
			t.Fatalf("Insert(%+v): %v", e, err)
		}
	}

	at, ok, err := repo.EarliestAt(ctx)
	if err != nil || !ok {
		t.Fatalf("EarliestAt = (%d, %v, %v)", at, ok, err)
	}
	if at != 1200 {
		t.Errorf("EarliestAt = %d, want 1200", at)
	}

	// Pruning moves the floor: the reported earliest must follow the table,
	// not a constant.
	if _, pruneErr := repo.PruneOlderThan(ctx, 4000); pruneErr != nil {
		t.Fatalf("PruneOlderThan: %v", pruneErr)
	}
	if at, ok, err = repo.EarliestAt(ctx); err != nil || !ok || at != 5000 {
		t.Errorf("EarliestAt after prune = (%d, %v, %v), want (5000, true, nil)", at, ok, err)
	}
}
