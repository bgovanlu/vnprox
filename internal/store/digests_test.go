package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestDigestRepo_ScheduleRoundTripsAndUpserts(t *testing.T) {
	db := openTestDB(t)
	repo := NewDigestRepo(db)
	ctx := context.Background()

	if _, err := repo.Schedule(ctx, DefaultDigestScheduleID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Schedule on an unconfigured daemon: err = %v, want ErrNotFound", err)
	}

	want := DigestSchedule{
		ID:        DefaultDigestScheduleID,
		Enabled:   true,
		EverySec:  604800,
		RuleIDs:   []string{"ar-1", "ar-2"},
		UpdatedAt: 1750000000,
		UpdatedBy: "alice@pam",
	}
	if err := repo.UpsertSchedule(ctx, want); err != nil {
		t.Fatalf("UpsertSchedule: %v", err)
	}

	got, err := repo.Schedule(ctx, DefaultDigestScheduleID)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if got.Enabled != want.Enabled || got.EverySec != want.EverySec ||
		got.UpdatedAt != want.UpdatedAt || got.UpdatedBy != want.UpdatedBy {
		t.Errorf("Schedule = %+v, want %+v", got, want)
	}
	if len(got.RuleIDs) != 2 || got.RuleIDs[0] != "ar-1" || got.RuleIDs[1] != "ar-2" {
		t.Errorf("Schedule.RuleIDs = %v, want [ar-1 ar-2]", got.RuleIDs)
	}

	// Upsert REPLACES: clearing the recipient list must actually clear it,
	// not leave the previous one in place. A partial update is exactly how a
	// narrowed digest keeps going to a target somebody removed.
	cleared := want
	cleared.RuleIDs = nil
	cleared.Enabled = false
	cleared.EverySec = 86400
	if clearErr := repo.UpsertSchedule(ctx, cleared); clearErr != nil {
		t.Fatalf("UpsertSchedule (clearing): %v", clearErr)
	}
	got, err = repo.Schedule(ctx, DefaultDigestScheduleID)
	if err != nil {
		t.Fatalf("Schedule after clearing: %v", err)
	}
	if len(got.RuleIDs) != 0 {
		t.Errorf("Schedule.RuleIDs after clearing = %v, want empty", got.RuleIDs)
	}
	if got.Enabled || got.EverySec != 86400 {
		t.Errorf("Schedule after clearing = (enabled %v, every %d), want (false, 86400)", got.Enabled, got.EverySec)
	}
}

func TestDigestRepo_LatestRunIsTheBaseline(t *testing.T) {
	db := openTestDB(t)
	repo := NewDigestRepo(db)
	ctx := context.Background()

	if _, err := repo.LatestRun(ctx, DefaultDigestScheduleID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LatestRun before any digest: err = %v, want ErrNotFound (the no-baseline case)", err)
	}

	older := DigestRun{
		ID: "run-1", ScheduleID: DefaultDigestScheduleID,
		PeriodStart: 0, PeriodEnd: 1750000000, GeneratedAt: 1750000000,
		PostureOverall: 71, OpenedCount: 3, ClosedCount: 1, DriftCount: 2, CapacityCount: 0,
		Quiet: false, Status: DigestStatusDelivered, Detail: "3 opened",
	}
	newer := DigestRun{
		ID: "run-2", ScheduleID: DefaultDigestScheduleID,
		PeriodStart: 1750000000, PeriodEnd: 1750604800, GeneratedAt: 1750604800,
		PostureOverall: DigestPostureNotScored, Quiet: true,
		Status: DigestStatusDelivered, Detail: "quiet period; nothing to report",
	}
	// Inserted out of order on purpose: "latest" must mean the newest
	// PERIOD, not the most recent insert.
	if err := repo.RecordRun(ctx, newer); err != nil {
		t.Fatalf("RecordRun(newer): %v", err)
	}
	if err := repo.RecordRun(ctx, older); err != nil {
		t.Fatalf("RecordRun(older): %v", err)
	}

	got, err := repo.LatestRun(ctx, DefaultDigestScheduleID)
	if err != nil {
		t.Fatalf("LatestRun: %v", err)
	}
	if got.ID != "run-2" {
		t.Fatalf("LatestRun.ID = %q, want run-2 (the newest period, not the newest insert)", got.ID)
	}
	if got.PostureOverall != DigestPostureNotScored {
		t.Errorf("LatestRun.PostureOverall = %d, want the not-scored sentinel %d", got.PostureOverall, DigestPostureNotScored)
	}
	if !got.Quiet || got.Status != DigestStatusDelivered {
		t.Errorf("LatestRun = (quiet %v, status %q), want (true, %q)", got.Quiet, got.Status, DigestStatusDelivered)
	}

	// A different schedule's history is invisible to this one.
	if otherErr := repo.RecordRun(ctx, DigestRun{
		ID: "run-other", ScheduleID: "other", PeriodEnd: 1760000000, GeneratedAt: 1760000000,
		PostureOverall: 99, Status: DigestStatusDelivered,
	}); otherErr != nil {
		t.Fatalf("RecordRun(other schedule): %v", otherErr)
	}
	got, err = repo.LatestRun(ctx, DefaultDigestScheduleID)
	if err != nil {
		t.Fatalf("LatestRun after another schedule ran: %v", err)
	}
	if got.ID != "run-2" {
		t.Errorf("LatestRun.ID = %q, want run-2 — another schedule's run leaked into this one's baseline", got.ID)
	}
}

func TestDigestRepo_RecordRunTrimsHistory(t *testing.T) {
	db := openTestDB(t)
	repo := NewDigestRepo(db)
	ctx := context.Background()

	total := DefaultDigestRunKeep + 5
	for i := range total {
		if err := repo.RecordRun(ctx, DigestRun{
			ID:         fmt.Sprintf("run-%03d", i),
			ScheduleID: DefaultDigestScheduleID,
			PeriodEnd:  int64(1750000000 + i*604800),
			Status:     DigestStatusDelivered,
		}); err != nil {
			t.Fatalf("RecordRun(%d): %v", i, err)
		}
	}

	runs, err := repo.ListRuns(ctx, DefaultDigestScheduleID, 1000)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != DefaultDigestRunKeep {
		t.Fatalf("digest_runs after %d inserts = %d rows, want the bound %d — the table is unbounded",
			total, len(runs), DefaultDigestRunKeep)
	}
	// The trim must drop the OLDEST, never the newest: the newest is the
	// baseline the next digest reads.
	if runs[0].ID != fmt.Sprintf("run-%03d", total-1) {
		t.Errorf("newest surviving run = %q, want run-%03d", runs[0].ID, total-1)
	}
}
