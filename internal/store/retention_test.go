package store

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func seedSnapshotAt(t *testing.T, ctx context.Context, snapshots *SnapshotRepo, blobs *BlobRepo, id string, takenAt int64, changesetID string) {
	t.Helper()
	hash, err := blobs.Put(ctx, "content-"+id)
	if err != nil {
		t.Fatalf("seed blob for %s: %v", id, err)
	}
	cs := sql.NullString{}
	if changesetID != "" {
		cs = sql.NullString{String: changesetID, Valid: true}
	}
	if err := snapshots.Insert(ctx, Snapshot{ID: id, ChangesetID: cs, TakenAt: takenAt, Kind: "pre", FilesJSON: "[]"}); err != nil {
		t.Fatalf("seed snapshot %s: %v", id, err)
	}
	if err := snapshots.InsertFiles(ctx, []SnapshotFileRef{{SnapshotID: id, Node: "pve1", Path: "/etc/network/interfaces", SHA256: hash}}); err != nil {
		t.Fatalf("seed snapshot_files %s: %v", id, err)
	}
}

// TestSnapshotRetention_TimeTravel exercises the documented policy (keep 90
// days by default, committed-changeset snapshots pinned 7 days minimum) with
// a synthetic "now" so the test doesn't depend on wall-clock time.
func TestSnapshotRetention_TimeTravel(t *testing.T) {
	db := openTestDB(t)
	snapshots := NewSnapshotRepo(db)
	blobs := NewBlobRepo(db)
	changesets := NewChangesetRepo(db)
	ctx := context.Background()

	now := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	day := 24 * time.Hour

	// A committed changeset whose snapshot is 5 days old: past nothing, but
	// exercises the pin path (kept regardless of keepDays).
	if err := changesets.Insert(ctx, Changeset{ID: "cs-committed", Author: "root@pam", Status: "committed", OpsJSON: "[]", CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatalf("seed committed changeset: %v", err)
	}
	// A rolled-back changeset whose snapshot is also 5 days old: not
	// committed, so the pin doesn't protect it.
	if err := changesets.Insert(ctx, Changeset{ID: "cs-rolledback", Author: "root@pam", Status: "rolled_back", OpsJSON: "[]", CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatalf("seed rolled-back changeset: %v", err)
	}

	seedSnapshotAt(t, ctx, snapshots, blobs, "old-manual", now.Add(-100*day).Unix(), "")
	seedSnapshotAt(t, ctx, snapshots, blobs, "recent-manual", now.Add(-1*day).Unix(), "")
	seedSnapshotAt(t, ctx, snapshots, blobs, "committed-old-window", now.Add(-95*day).Unix(), "cs-committed")
	seedSnapshotAt(t, ctx, snapshots, blobs, "rolledback-old-window", now.Add(-95*day).Unix(), "cs-rolledback")

	// keepDays=90, pinDays=7: "old-manual" (100d) expired and unpinned ->
	// deleted. "recent-manual" (1d) not expired -> kept. Both changeset-
	// linked snapshots are 95d old (past the 90d keep window); the
	// committed one is NOT within the 7d pin window either (95d > 7d), so
	// the pin doesn't save it here — it only floors retention below 7d.
	deleted, blobsDeleted, err := SnapshotRetention(ctx, snapshots, blobs, now, 90, 7)
	if err != nil {
		t.Fatalf("SnapshotRetention: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("snapshots deleted = %d, want 3 (old-manual, committed-old-window, rolledback-old-window)", deleted)
	}
	if blobsDeleted != 3 {
		t.Fatalf("blobs deleted = %d, want 3", blobsDeleted)
	}

	remaining, err := snapshots.List(ctx, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != "recent-manual" {
		t.Fatalf("remaining snapshots = %+v, want only recent-manual", remaining)
	}
}

// TestSnapshotRetention_PinFloor exercises the actual pin scenario: an
// operator configuring keepDays shorter than the 7-day rollback window must
// not lose a committed changeset's restore point before that window closes.
func TestSnapshotRetention_PinFloor(t *testing.T) {
	db := openTestDB(t)
	snapshots := NewSnapshotRepo(db)
	blobs := NewBlobRepo(db)
	changesets := NewChangesetRepo(db)
	ctx := context.Background()

	now := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	day := 24 * time.Hour

	if err := changesets.Insert(ctx, Changeset{ID: "cs-committed", Author: "root@pam", Status: "committed", OpsJSON: "[]", CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatalf("seed committed changeset: %v", err)
	}
	// 3 days old: past a hypothetical 1-day keepDays, but within the 7-day
	// pin floor.
	seedSnapshotAt(t, ctx, snapshots, blobs, "committed-recent", now.Add(-3*day).Unix(), "cs-committed")

	deleted, _, err := SnapshotRetention(ctx, snapshots, blobs, now, 1, 7)
	if err != nil {
		t.Fatalf("SnapshotRetention: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("snapshots deleted = %d, want 0 (pin floor must protect it)", deleted)
	}
}

func TestSnapshotRepo_ListPage_Cursor(t *testing.T) {
	db := openTestDB(t)
	snapshots := NewSnapshotRepo(db)
	blobs := NewBlobRepo(db)
	ctx := context.Background()

	for i, id := range []string{"s1", "s2", "s3", "s4", "s5"} {
		seedSnapshotAt(t, ctx, snapshots, blobs, id, int64(100+i), "")
	}

	page1, cursor1, err := snapshots.ListPage(ctx, "", 2)
	if err != nil {
		t.Fatalf("ListPage page1: %v", err)
	}
	if len(page1) != 2 || page1[0].ID != "s5" || page1[1].ID != "s4" {
		t.Fatalf("page1 = %+v, want [s5,s4]", page1)
	}
	if cursor1 == "" {
		t.Fatal("expected a next-page cursor")
	}

	page2, cursor2, err := snapshots.ListPage(ctx, cursor1, 2)
	if err != nil {
		t.Fatalf("ListPage page2: %v", err)
	}
	if len(page2) != 2 || page2[0].ID != "s3" || page2[1].ID != "s2" {
		t.Fatalf("page2 = %+v, want [s3,s2]", page2)
	}

	page3, cursor3, err := snapshots.ListPage(ctx, cursor2, 2)
	if err != nil {
		t.Fatalf("ListPage page3: %v", err)
	}
	if len(page3) != 1 || page3[0].ID != "s1" {
		t.Fatalf("page3 = %+v, want [s1]", page3)
	}
	if cursor3 != "" {
		t.Fatalf("expected no further page, got cursor %q", cursor3)
	}
}

func TestAuditRepo_ListPage_FiltersAndCursor(t *testing.T) {
	db := openTestDB(t)
	repo := NewAuditRepo(db)
	ctx := context.Background()

	entries := []AuditEntry{
		{At: 100, Username: "alice", Action: "changeset.apply", Result: "success", Target: sql.NullString{String: "bridge:pve1:vmbr1", Valid: true}},
		{At: 101, Username: "bob", Action: "changeset.apply", Result: "failed"},
		{At: 102, Username: "alice", Action: "changeset.rollback", Result: "success"},
	}
	for _, e := range entries {
		if _, err := repo.Append(ctx, e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	byUser, next, err := repo.ListPage(ctx, AuditFilter{User: "alice"}, "", 10)
	if err != nil {
		t.Fatalf("ListPage(user=alice): %v", err)
	}
	if next != "" {
		t.Fatalf("unexpected next cursor: %q", next)
	}
	if len(byUser) != 2 {
		t.Fatalf("ListPage(user=alice) len = %d, want 2", len(byUser))
	}
	if byUser[0].At != 102 || byUser[1].At != 100 {
		t.Fatalf("ListPage(user=alice) order = %+v", byUser)
	}

	byResult, _, err := repo.ListPage(ctx, AuditFilter{Result: "failed"}, "", 10)
	if err != nil {
		t.Fatalf("ListPage(result=failed): %v", err)
	}
	if len(byResult) != 1 || byResult[0].Username != "bob" {
		t.Fatalf("ListPage(result=failed) = %+v", byResult)
	}

	byTarget, _, err := repo.ListPage(ctx, AuditFilter{Target: "bridge:pve1:vmbr1"}, "", 10)
	if err != nil {
		t.Fatalf("ListPage(target=...): %v", err)
	}
	if len(byTarget) != 1 || byTarget[0].At != 100 {
		t.Fatalf("ListPage(target=...) = %+v", byTarget)
	}

	byRange, _, err := repo.ListPage(ctx, AuditFilter{From: 101, To: 101}, "", 10)
	if err != nil {
		t.Fatalf("ListPage(from/to): %v", err)
	}
	if len(byRange) != 1 || byRange[0].Username != "bob" {
		t.Fatalf("ListPage(from/to) = %+v", byRange)
	}

	// cursor pagination across all three entries, one at a time.
	all := []int64{}
	cursor := ""
	for {
		page, next, err := repo.ListPage(ctx, AuditFilter{}, cursor, 1)
		if err != nil {
			t.Fatalf("ListPage cursor loop: %v", err)
		}
		if len(page) != 1 {
			t.Fatalf("expected 1 entry per page, got %d", len(page))
		}
		all = append(all, page[0].At)
		if next == "" {
			break
		}
		cursor = next
	}
	if len(all) != 3 || all[0] != 102 || all[1] != 101 || all[2] != 100 {
		t.Fatalf("paginated all = %+v, want [102,101,100]", all)
	}
}
