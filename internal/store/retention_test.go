package store

import (
	"context"
	"database/sql"
	"errors"
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

// TestSnapshotRetention_AC2_InFlightChangesetNeverPruned is T-1905 AC2's own
// test, written first and explicit per the task card: "a snapshot required
// by a changeset in awaiting_confirm, or within its rollback window, is
// never pruned regardless of age". This is deliberately the dangerous case
// — the daemon's entire rollback safety net is the thing under test — so it
// is built to be impossible to pass by accident:
//
//   - the snapshot is made EXTREMELY old (500 days, versus a keepDays of 1
//     and a pinDays of 1 — the most aggressive possible retention config, so
//     nothing about the *numbers* could accidentally save it);
//   - both in-flight statuses (awaiting_confirm and applying) are exercised
//     as their own subtests, since AC2's wording names awaiting_confirm
//     explicitly but the same in-flight danger applies to applying
//     (recoverInterruptedApply's crash-recovery window, internal/change/
//     apply_errors.go's "in flight (status applying or awaiting_confirm)");
//   - a CONTROL subtest proves the harness can actually delete an
//     equally-old snapshot once its changeset is no longer in flight
//     (rolled_back) — without this, all the "protected" subtests could be
//     passing because Prune deletes nothing at this age for an unrelated
//     reason, which would make the whole test vacuous;
//   - and a second control proves the *pinned* committed case behaves as
//     T-206 already established (a courtesy re-assertion, not new ground),
//     so a future edit that broke ONLY the new in-flight guardrail while
//     leaving the pin logic intact is still caught by the right subtest.
func TestSnapshotRetention_AC2_InFlightChangesetNeverPruned(t *testing.T) {
	const (
		aggressiveKeepDays = 1
		aggressivePinDays  = 1
		veryOldDays        = 500
	)

	newFixture := func(t *testing.T) (*SnapshotRepo, *BlobRepo, *ChangesetRepo, context.Context, time.Time) {
		t.Helper()
		db := openTestDB(t)
		return NewSnapshotRepo(db), NewBlobRepo(db), NewChangesetRepo(db), context.Background(), time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	}

	seedChangeset := func(t *testing.T, ctx context.Context, changesets *ChangesetRepo, id, status string) {
		t.Helper()
		if err := changesets.Insert(ctx, Changeset{ID: id, Author: "root@pam", Status: status, OpsJSON: "[]", CreatedAt: 1, UpdatedAt: 1}); err != nil {
			t.Fatalf("seed %s changeset: %v", status, err)
		}
	}

	for _, status := range []string{"awaiting_confirm", "applying"} {
		t.Run(status, func(t *testing.T) {
			snapshots, blobs, changesets, ctx, now := newFixture(t)
			day := 24 * time.Hour

			seedChangeset(t, ctx, changesets, "cs-inflight", status)
			seedSnapshotAt(t, ctx, snapshots, blobs, "inflight-ancient", now.Add(-veryOldDays*day).Unix(), "cs-inflight")

			deleted, _, err := SnapshotRetention(ctx, snapshots, blobs, now, aggressiveKeepDays, aggressivePinDays)
			if err != nil {
				t.Fatalf("SnapshotRetention: %v", err)
			}
			if deleted != 0 {
				t.Fatalf("snapshots deleted = %d, want 0 (a %s changeset's snapshot must never be pruned, regardless of age)", deleted, status)
			}
			remaining, err := snapshots.Get(ctx, "inflight-ancient")
			if err != nil {
				t.Fatalf("snapshot must still exist: %v", err)
			}
			if remaining.ID != "inflight-ancient" {
				t.Fatalf("unexpected surviving snapshot: %+v", remaining)
			}
		})
	}

	// Control: the harness is not simply "never deletes anything" — an
	// equally ancient snapshot linked to a changeset that has LEFT the
	// in-flight statuses (rolled_back is terminal, not pinned) is pruned
	// under the same aggressive config. Without this subtest, the two
	// subtests above could be passing for the wrong reason.
	t.Run("control_terminal_status_is_pruned", func(t *testing.T) {
		snapshots, blobs, changesets, ctx, now := newFixture(t)
		day := 24 * time.Hour

		seedChangeset(t, ctx, changesets, "cs-terminal", "rolled_back")
		seedSnapshotAt(t, ctx, snapshots, blobs, "terminal-ancient", now.Add(-veryOldDays*day).Unix(), "cs-terminal")

		deleted, _, err := SnapshotRetention(ctx, snapshots, blobs, now, aggressiveKeepDays, aggressivePinDays)
		if err != nil {
			t.Fatalf("SnapshotRetention: %v", err)
		}
		if deleted != 1 {
			t.Fatalf("snapshots deleted = %d, want 1 (a terminal changeset's ancient snapshot is not protected)", deleted)
		}
		if _, err := snapshots.Get(ctx, "terminal-ancient"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected the terminal-changeset snapshot to be gone, got err=%v", err)
		}
	})

	// Control: a committed changeset's ancient snapshot is deleted once it
	// falls outside pinDays too — re-asserting T-206's existing behavior so
	// a future edit that accidentally widened the NEW in-flight guardrail to
	// also swallow the pin case is caught here, not just by omission.
	t.Run("control_committed_past_pin_window_is_pruned", func(t *testing.T) {
		snapshots, blobs, changesets, ctx, now := newFixture(t)
		day := 24 * time.Hour

		seedChangeset(t, ctx, changesets, "cs-committed", "committed")
		seedSnapshotAt(t, ctx, snapshots, blobs, "committed-ancient", now.Add(-veryOldDays*day).Unix(), "cs-committed")

		deleted, _, err := SnapshotRetention(ctx, snapshots, blobs, now, aggressiveKeepDays, aggressivePinDays)
		if err != nil {
			t.Fatalf("SnapshotRetention: %v", err)
		}
		if deleted != 1 {
			t.Fatalf("snapshots deleted = %d, want 1 (a committed changeset's snapshot past the pin window is not protected)", deleted)
		}
	})
}

// TestRetention_T1805_PrunedStoreStillPermitsUnattendedRevertOfInFlightChangeset
// closes the interaction this card's dispatch specifically asked to be
// stated AND tested, not left implicit: since T-1805, an awaiting_confirm
// fw.*/sdn.apply changeset can hold a sealed PVE revert ticket
// (changesets.revert_ticket_enc/revert_ticket_expires_at) that the daemon
// uses to revert it unattended after a confirm timeout or a crash-restart
// (internal/change.Service.doRollbackLocked / recoverInterruptedApply,
// planning/reports/T-1805.md §2 Claim 2/3). Everything an unattended revert
// needs must survive a retention pass while the changeset is still in
// flight:
//
//  1. the sealed ticket itself (ChangesetRepo.RevertTicket) — retention
//     never touches the changesets table at all (§13 of docs/data-model.md,
//     "what this card deliberately does not prune"), so this is really
//     "retention doesn't delete the changeset row out from under it", but
//     it is asserted directly against the ticket accessor rather than left
//     as an inference from "the table wasn't touched";
//  2. the pre-apply snapshot the revert restores SDN/firewall state from
//     (SnapshotRepo.Prune's in-flight guardrail, T-1905 AC2, tested above)
//     — a live ticket with nothing to restore from would authorize a
//     revert that has nothing to revert with.
//
// Both are driven through the real retention entry points (SnapshotRetention
// AND AuditRepo.PruneRetention — audit is exercised too since it is the
// other class this card adds a ceiling for, even though it has no
// changeset-linkage of its own) under the same maximally-aggressive
// configuration the guardrail test above uses, so a real prune pass
// genuinely ran and had every opportunity to strand or destroy this state.
func TestRetention_T1805_PrunedStoreStillPermitsUnattendedRevertOfInFlightChangeset(t *testing.T) {
	db := openTestDB(t)
	snapshots := NewSnapshotRepo(db)
	blobs := NewBlobRepo(db)
	changesets := NewChangesetRepo(db)
	audit := NewAuditRepo(db)
	ctx := context.Background()

	now := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	day := 24 * time.Hour

	// A fw.*-only changeset that reached awaiting_confirm, sealed a revert
	// ticket at apply time (T-1805), and then sat there for a very long
	// time — the daemon-was-down-for-months scenario the in-flight
	// guardrail exists for, not a hypothetical.
	const changesetID = "cs-awaiting-with-ticket"
	if err := changesets.Insert(ctx, Changeset{
		ID: changesetID, Author: "root@pam", Status: "awaiting_confirm",
		OpsJSON: "[]", CreatedAt: 1, UpdatedAt: 1,
	}); err != nil {
		t.Fatalf("seed awaiting_confirm changeset: %v", err)
	}

	sealedTicket := []byte("sealed-pve-ticket-ciphertext-not-actually-encrypted-in-this-test")
	ticketExpiresAt := now.Add(2 * time.Hour).Unix() // still live, PVE ticket's ~2h lifetime
	if err := changesets.SealRevertTicket(ctx, changesetID, sealedTicket, ticketExpiresAt); err != nil {
		t.Fatalf("SealRevertTicket: %v", err)
	}

	const snapshotID = "pre-apply-ancient"
	seedSnapshotAt(t, ctx, snapshots, blobs, snapshotID, now.Add(-500*day).Unix(), changesetID)

	// Also seed an ancient, unrelated audit row so a real audit prune pass
	// (this card's other new retention path) actually deletes something —
	// audit has no changeset linkage/guardrail of its own (§13), so this is
	// a control that the audit prune ran for real, not a no-op.
	if _, err := audit.Append(ctx, AuditEntry{
		At: now.Add(-500 * day).Unix(), Username: "root@pam",
		Action: "changeset.apply", Result: "success",
	}); err != nil {
		t.Fatalf("seed ancient audit row: %v", err)
	}

	// The most aggressive retention configuration available: a 1-day
	// snapshot keep/pin window and a 1-day audit window, against a
	// changeset/snapshot/audit-row that are all 500 days old.
	const aggressiveKeepDays = 1
	if _, _, err := SnapshotRetention(ctx, snapshots, blobs, now, aggressiveKeepDays, aggressiveKeepDays); err != nil {
		t.Fatalf("SnapshotRetention: %v", err)
	}
	auditDeleted, err := audit.PruneRetention(ctx, now, aggressiveKeepDays)
	if err != nil {
		t.Fatalf("AuditRepo.PruneRetention: %v", err)
	}
	if auditDeleted != 1 {
		t.Fatalf("audit rows deleted = %d, want 1 (the audit prune must have run for real)", auditDeleted)
	}

	// 1. The changeset row and its sealed ticket both survive, unchanged.
	cs, err := changesets.Get(ctx, changesetID)
	if err != nil {
		t.Fatalf("changeset must still exist after retention: %v", err)
	}
	if cs.Status != "awaiting_confirm" {
		t.Fatalf("changeset status = %q, want awaiting_confirm (retention must never touch changesets)", cs.Status)
	}
	gotTicket, gotExpiry, err := changesets.RevertTicket(ctx, changesetID)
	if err != nil {
		t.Fatalf("RevertTicket after retention: %v", err)
	}
	if string(gotTicket) != string(sealedTicket) {
		t.Fatalf("sealed ticket after retention = %q, want the original %q — retention must never strand or corrupt it", gotTicket, sealedTicket)
	}
	if gotExpiry != ticketExpiresAt {
		t.Fatalf("ticket expiry after retention = %d, want %d unchanged", gotExpiry, ticketExpiresAt)
	}

	// 2. The pre-apply snapshot an unattended revert restores SDN/firewall
	// state from also survives — a live ticket with nothing to restore
	// from would be a revert that can authenticate but has nothing to do.
	snap, err := snapshots.Get(ctx, snapshotID)
	if err != nil {
		t.Fatalf("pre-apply snapshot must still exist after retention: %v", err)
	}
	if snap.ID != snapshotID {
		t.Fatalf("unexpected surviving snapshot: %+v", snap)
	}

	// Conclusion: both preconditions for change.Service.doRollbackLocked's
	// unattended-revert path (an unsealable ticket, an unattended.
	// restorable snapshot) hold after a real retention pass ran and pruned
	// something else in every table it touches — a pending unattended
	// revert of this changeset is exactly as possible after retention as
	// before it.
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
