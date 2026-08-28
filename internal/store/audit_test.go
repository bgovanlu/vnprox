// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestAuditRepo_OnAppendHookFiresWithAssignedID(t *testing.T) {
	db := openTestDB(t)
	repo := NewAuditRepo(db)
	ctx := context.Background()

	var got []AuditEntry
	repo.SetOnAppend(func(e AuditEntry) { got = append(got, e) })

	e := AuditEntry{At: 100, Username: "root@pam", Action: "token.use", Result: "allowed"}
	id, err := repo.Append(ctx, e)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("onAppend called %d times, want 1", len(got))
	}
	if got[0].ID != id {
		t.Errorf("onAppend entry.ID = %d, want the assigned id %d", got[0].ID, id)
	}
	if got[0].Action != "token.use" || got[0].Username != "root@pam" {
		t.Errorf("onAppend entry = %+v, want the appended fields", got[0])
	}

	// A nil hook (the default — no SetOnAppend call) must not panic.
	repo2 := NewAuditRepo(db)
	if _, err := repo2.Append(ctx, e); err != nil {
		t.Fatalf("Append with no hook registered: %v", err)
	}
}

func TestAuditRepo_RoundTrip(t *testing.T) {
	db := openTestDB(t)
	repo := NewAuditRepo(db)
	ctx := context.Background()

	e := AuditEntry{
		At:          100,
		Username:    "root@pam",
		Action:      "network.apply",
		Target:      sql.NullString{String: "node/pve1", Valid: true},
		ChangesetID: sql.NullString{String: "cs-1", Valid: true},
		Result:      "ok",
		DetailJSON:  sql.NullString{String: `{"ops":2}`, Valid: true},
	}

	id, err := repo.Append(ctx, e)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if id == 0 {
		t.Fatal("Append returned id 0, want an assigned autoincrement id")
	}
	e.ID = id

	got, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != e {
		t.Errorf("Get() = %+v, want %+v", got, e)
	}

	id2, err := repo.Append(ctx, AuditEntry{At: 200, Username: "root@pam", Action: "login", Result: "ok"})
	if err != nil {
		t.Fatalf("Append #2: %v", err)
	}

	all, err := repo.List(ctx, "", 0)
	if err != nil {
		t.Fatalf("List(all): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("List(all) len = %d, want 2", len(all))
	}
	// newest first
	if all[0].ID != id2 || all[1].ID != id {
		t.Errorf("List(all) order = [%d, %d], want [%d, %d]", all[0].ID, all[1].ID, id2, id)
	}

	byChangeset, err := repo.List(ctx, "cs-1", 0)
	if err != nil {
		t.Fatalf("List(cs-1): %v", err)
	}
	if len(byChangeset) != 1 || byChangeset[0].ID != id {
		t.Errorf("List(cs-1) = %+v, want just entry %d", byChangeset, id)
	}

	limited, err := repo.List(ctx, "", 1)
	if err != nil {
		t.Fatalf("List(limit=1): %v", err)
	}
	if len(limited) != 1 {
		t.Errorf("List(limit=1) len = %d, want 1", len(limited))
	}
}

func TestAuditRepo_GetNotFound(t *testing.T) {
	db := openTestDB(t)
	repo := NewAuditRepo(db)
	if _, err := repo.Get(context.Background(), 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(missing): got %v, want ErrNotFound", err)
	}
}

// T-1007: GET /history/events' changeset-lifecycle input.
func TestAuditRepo_ListActionsInRange(t *testing.T) {
	db := openTestDB(t)
	repo := NewAuditRepo(db)
	ctx := context.Background()

	rows := []AuditEntry{
		{At: 1000, Username: "root@pam", Action: "changeset.apply", Result: "applying", ChangesetID: sql.NullString{String: "cs-1", Valid: true}},
		{At: 2000, Username: "root@pam", Action: "changeset.confirm", Result: "committed", ChangesetID: sql.NullString{String: "cs-1", Valid: true}},
		// Not in the lifecycle set: must never appear in ListActionsInRange's
		// output even though it's within the same time range.
		{At: 1500, Username: "root@pam", Action: "session.login", Result: "ok"},
		{At: 3000, Username: "root@pam", Action: "changeset.rollback", Result: "rolled_back", ChangesetID: sql.NullString{String: "cs-2", Valid: true}},
	}
	for _, e := range rows {
		if _, err := repo.Append(ctx, e); err != nil {
			t.Fatalf("Append(%+v): %v", e, err)
		}
	}

	t.Run("unbounded", func(t *testing.T) {
		got, err := repo.ListActionsInRange(ctx, ChangesetLifecycleActions, 0, 0)
		if err != nil {
			t.Fatalf("ListActionsInRange: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("ListActionsInRange(unbounded) len = %d, want 3 (apply/confirm/rollback, not session.login)", len(got))
		}
		for i := 1; i < len(got); i++ {
			if got[i].At < got[i-1].At {
				t.Errorf("results not ascending by at: %+v", got)
			}
		}
	})

	t.Run("bounded", func(t *testing.T) {
		got, err := repo.ListActionsInRange(ctx, ChangesetLifecycleActions, 1000, 2000)
		if err != nil {
			t.Fatalf("ListActionsInRange: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("ListActionsInRange(1000,2000) len = %d, want 2 (apply@1000, confirm@2000)", len(got))
		}
		if got[0].Action != "changeset.apply" || got[1].Action != "changeset.confirm" {
			t.Errorf("ListActionsInRange(1000,2000) = %+v, want apply then confirm", got)
		}
	})

	t.Run("empty actions", func(t *testing.T) {
		got, err := repo.ListActionsInRange(ctx, nil, 0, 0)
		if err != nil {
			t.Fatalf("ListActionsInRange(nil actions): %v", err)
		}
		if got != nil {
			t.Errorf("ListActionsInRange(nil actions) = %+v, want nil", got)
		}
	})
}

// TestAuditRepo_PruneRetention exercises T-1905's audit_log ceiling with a
// synthetic "now" (no wall-clock dependency): rows older than keepDays are
// removed, rows within the window survive untouched, and an unset/invalid
// keepDays falls back to DefaultAuditRetentionDays (730d) rather than
// pruning everything or nothing.
func TestAuditRepo_PruneRetention(t *testing.T) {
	db := openTestDB(t)
	repo := NewAuditRepo(db)
	ctx := context.Background()

	now := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	day := int64(24 * 60 * 60)
	nowUnix := now.Unix()

	seed := func(t *testing.T, at int64, action string) {
		t.Helper()
		if _, err := repo.Append(ctx, AuditEntry{At: at, Username: "root@pam", Action: action, Result: "success"}); err != nil {
			t.Fatalf("seed audit row at %d: %v", at, err)
		}
	}

	seed(t, nowUnix-400*day, "changeset.apply") // within a 730d window
	seed(t, nowUnix-800*day, "changeset.apply") // past a 730d window
	seed(t, nowUnix-1*day, "changeset.confirm") // recent

	deleted, err := repo.PruneRetention(ctx, now, 730)
	if err != nil {
		t.Fatalf("PruneRetention: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1 (only the 800-day-old row)", deleted)
	}
	remaining, err := repo.List(ctx, "", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(remaining) != 2 {
		t.Fatalf("remaining rows = %d, want 2", len(remaining))
	}

	t.Run("aggressive keepDays prunes the rest", func(t *testing.T) {
		deleted, err := repo.PruneRetention(ctx, now, 300)
		if err != nil {
			t.Fatalf("PruneRetention: %v", err)
		}
		if deleted != 1 {
			t.Fatalf("deleted = %d, want 1 (the 400-day-old row, now past a 300d window)", deleted)
		}
	})

	t.Run("keepDays<=0 falls back to the documented default", func(t *testing.T) {
		db := openTestDB(t)
		repo := NewAuditRepo(db)
		seed := func(t *testing.T, at int64) {
			t.Helper()
			if _, err := repo.Append(ctx, AuditEntry{At: at, Username: "root@pam", Action: "changeset.apply", Result: "success"}); err != nil {
				t.Fatalf("seed: %v", err)
			}
		}
		seed(t, nowUnix-(DefaultAuditRetentionDays-1)*day) // inside the 730d default
		seed(t, nowUnix-(DefaultAuditRetentionDays+1)*day) // outside it

		deleted, err := repo.PruneRetention(ctx, now, 0)
		if err != nil {
			t.Fatalf("PruneRetention(keepDays=0): %v", err)
		}
		if deleted != 1 {
			t.Fatalf("deleted = %d, want 1 (only the row past the 730d default)", deleted)
		}
	})
}

// TestAuditRepo_RunPruneLoop drives one real tick of the supervised loop
// against a live ticker (mirrors the store package's other RunPruneLoop
// tests) and asserts it actually prunes, then that ctx cancellation returns
// cleanly.
func TestAuditRepo_RunPruneLoop(t *testing.T) {
	db := openTestDB(t)
	repo := NewAuditRepo(db)
	ctx := context.Background()

	old := time.Now().Add(-800 * 24 * time.Hour).Unix()
	if _, err := repo.Append(ctx, AuditEntry{At: old, Username: "root@pam", Action: "changeset.apply", Result: "success"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- repo.RunPruneLoop(loopCtx, 10*time.Millisecond, DefaultAuditRetentionDays, func(err error) {
			t.Errorf("unexpected prune error: %v", err)
		})
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		rows, err := repo.List(ctx, "", 0)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(rows) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for RunPruneLoop to prune the old row")
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunPruneLoop returned %v, want nil after cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunPruneLoop did not return after ctx cancellation")
	}
}
