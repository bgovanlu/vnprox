// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"testing"
)

func TestChangeScheduleRepo_UpsertGetResolve(t *testing.T) {
	db := openTestDB(t)
	repo := NewChangeScheduleRepo(db)
	ctx := context.Background()

	if _, err := repo.Get(ctx, "cs-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get before upsert: err = %v, want ErrNotFound", err)
	}

	sched := ChangesetSchedule{
		ChangesetID: "cs-1", WindowStart: 1000, WindowEnd: 1060,
		ConfirmTimeoutSec: 30, MissedWindowPolicy: "skip",
		CallbackTokenHash: "deadbeef", Status: ScheduleStatusPending,
		CreatedBy: "alice", CreatedAt: 900,
	}
	if err := repo.Upsert(ctx, sched); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := repo.Get(ctx, "cs-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != ScheduleStatusPending || got.WindowStart != 1000 || got.WindowEnd != 1060 {
		t.Errorf("Get() = %+v, want pending window [1000,1060]", got)
	}
	if got.FiredAt.Valid || got.CancelledAt.Valid {
		t.Errorf("Get() = %+v, want no fired_at/cancelled_at on a fresh row", got)
	}

	pending, err := repo.ListByStatus(ctx, ScheduleStatusPending)
	if err != nil {
		t.Fatalf("ListByStatus(pending): %v", err)
	}
	if len(pending) != 1 || pending[0].ChangesetID != "cs-1" {
		t.Errorf("ListByStatus(pending) = %+v, want [cs-1]", pending)
	}

	if resolveErr := repo.Resolve(ctx, "cs-1", ScheduleStatusFired, 1005); resolveErr != nil {
		t.Fatalf("Resolve: %v", resolveErr)
	}
	got, err = repo.Get(ctx, "cs-1")
	if err != nil {
		t.Fatalf("Get after resolve: %v", err)
	}
	if got.Status != ScheduleStatusFired || !got.FiredAt.Valid || got.FiredAt.Int64 != 1005 {
		t.Errorf("Get() after resolve = %+v, want fired/firedAt=1005", got)
	}

	pending, err = repo.ListByStatus(ctx, ScheduleStatusPending)
	if err != nil {
		t.Fatalf("ListByStatus(pending) after resolve: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("ListByStatus(pending) after resolve = %+v, want empty", pending)
	}
}

func TestChangeScheduleRepo_UpsertReplacesResolvedRow(t *testing.T) {
	db := openTestDB(t)
	repo := NewChangeScheduleRepo(db)
	ctx := context.Background()

	first := ChangesetSchedule{
		ChangesetID: "cs-1", WindowStart: 1000, WindowEnd: 1060,
		ConfirmTimeoutSec: 30, MissedWindowPolicy: "skip",
		CallbackTokenHash: "aaa", Status: ScheduleStatusPending, CreatedBy: "alice", CreatedAt: 900,
	}
	if err := repo.Upsert(ctx, first); err != nil {
		t.Fatalf("Upsert first: %v", err)
	}
	if err := repo.Cancel(ctx, "cs-1", 950); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	second := ChangesetSchedule{
		ChangesetID: "cs-1", WindowStart: 2000, WindowEnd: 2060,
		ConfirmTimeoutSec: 60, MissedWindowPolicy: "applyImmediately",
		CallbackTokenHash: "bbb", Status: ScheduleStatusPending, CreatedBy: "bob", CreatedAt: 1900,
	}
	if err := repo.Upsert(ctx, second); err != nil {
		t.Fatalf("Upsert second (replacing cancelled row): %v", err)
	}

	got, err := repo.Get(ctx, "cs-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != ScheduleStatusPending || got.WindowStart != 2000 || got.CreatedBy != "bob" {
		t.Errorf("Get() after re-upsert = %+v, want the fresh pending row", got)
	}
	if got.CancelledAt.Valid {
		t.Errorf("Get() after re-upsert = %+v, want cancelled_at cleared", got)
	}
}

func TestChangeScheduleRepo_Cancel(t *testing.T) {
	db := openTestDB(t)
	repo := NewChangeScheduleRepo(db)
	ctx := context.Background()

	if err := repo.Cancel(ctx, "missing", 100); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Cancel on missing row: err = %v, want ErrNotFound", err)
	}

	sched := ChangesetSchedule{
		ChangesetID: "cs-1", WindowStart: 1000, WindowEnd: 1060,
		ConfirmTimeoutSec: 30, MissedWindowPolicy: "skip",
		CallbackTokenHash: "aaa", Status: ScheduleStatusPending, CreatedBy: "alice", CreatedAt: 900,
	}
	if err := repo.Upsert(ctx, sched); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := repo.Cancel(ctx, "cs-1", 950); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	got, err := repo.Get(ctx, "cs-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != ScheduleStatusCancelled || !got.CancelledAt.Valid || got.CancelledAt.Int64 != 950 {
		t.Errorf("Get() after cancel = %+v, want cancelled/cancelledAt=950", got)
	}

	if err := repo.Cancel(ctx, "cs-1", 960); !errors.Is(err, ErrIllegalState) {
		t.Fatalf("Cancel already-cancelled row: err = %v, want ErrIllegalState", err)
	}
}
