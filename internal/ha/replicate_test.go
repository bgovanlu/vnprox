// SPDX-License-Identifier: Apache-2.0

package ha_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/bgovanlu/vnprox/internal/ha"
	"github.com/bgovanlu/vnprox/internal/store"
)

func openStore(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "vnprox.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func repos(db *store.DB) ha.StoreReplicationRepos {
	return ha.StoreReplicationRepos{
		Changesets: store.NewChangesetRepo(db),
		Schedules:  store.NewChangeScheduleRepo(db),
		Tokens:     store.NewAPITokenRepo(db),
		Snapshots:  store.NewSnapshotRepo(db),
		Blobs:      store.NewBlobRepo(db),
		Audit:      store.NewAuditRepo(db),
	}
}

// TestStoreReplication_GatherApply_CarriesInFlightRollbackState proves the
// replication feed carries everything a promoted standby needs to complete an
// in-flight rollback: the awaiting_confirm changeset (with its absolute
// confirm_deadline), the pre-apply snapshot, its blob content, tokens, and the
// audit tail.
func TestStoreReplication_GatherApply_CarriesInFlightRollbackState(t *testing.T) {
	ctx := context.Background()
	activeDB := openStore(t)
	standbyDB := openStore(t)
	activeRepos := repos(activeDB)
	standbyRepos := repos(standbyDB)

	// Seed the "active" store with an in-flight awaiting_confirm changeset, its
	// pre-apply snapshot + blob, a token, and an audit row.
	preContent := "auto lo\niface lo inet loopback\n"
	hash, err := activeRepos.Blobs.Put(ctx, preContent)
	if err != nil {
		t.Fatalf("Blobs.Put: %v", err)
	}
	if err = activeRepos.Changesets.Insert(ctx, store.Changeset{
		ID: "cs-1", Title: "add vmbr9", Author: "alice@pam", Status: "awaiting_confirm",
		OpsJSON: "[]", ConfirmDeadline: sql.NullInt64{Int64: haEpoch + 30, Valid: true},
		CreatedAt: haEpoch, UpdatedAt: haEpoch + 1,
	}); err != nil {
		t.Fatalf("Changesets.Insert: %v", err)
	}
	snapID := "snap-1"
	if err = activeRepos.Snapshots.Insert(ctx, store.Snapshot{
		ID: snapID, ChangesetID: sql.NullString{String: "cs-1", Valid: true}, TakenAt: haEpoch,
		Kind: "pre", FilesJSON: `[{"node":"pve1","path":"/etc/network/interfaces","sha256":"` + hash + `"}]`,
	}); err != nil {
		t.Fatalf("Snapshots.Insert: %v", err)
	}
	if err = activeRepos.Snapshots.InsertFiles(ctx, []store.SnapshotFileRef{{SnapshotID: snapID, Node: "pve1", Path: "/etc/network/interfaces", SHA256: hash}}); err != nil {
		t.Fatalf("Snapshots.InsertFiles: %v", err)
	}
	if err = activeRepos.Tokens.Create(ctx, store.APIToken{ID: "tok-1", Name: "ci", TokenHash: "abc", ScopesJSON: `["netRead"]`, CreatedBy: "alice@pam", CreatedAt: haEpoch}); err != nil {
		t.Fatalf("Tokens.Create: %v", err)
	}
	if _, err = activeRepos.Audit.Append(ctx, store.AuditEntry{At: haEpoch, Username: "alice@pam", Action: "changeset.apply", Result: "awaiting_confirm", ChangesetID: sql.NullString{String: "cs-1", Valid: true}}); err != nil {
		t.Fatalf("Audit.Append: %v", err)
	}

	src := ha.NewStoreReplication(activeRepos)
	dst := ha.NewStoreReplication(standbyRepos)

	batch, err := src.Gather(ctx, 0)
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if err = dst.Apply(ctx, batch); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// The standby now has the changeset with the SAME absolute confirm_deadline.
	got, err := standbyRepos.Changesets.Get(ctx, "cs-1")
	if err != nil {
		t.Fatalf("standby Changesets.Get: %v", err)
	}
	if got.Status != "awaiting_confirm" || !got.ConfirmDeadline.Valid || got.ConfirmDeadline.Int64 != haEpoch+30 {
		t.Errorf("replicated changeset = %+v, want awaiting_confirm with deadline %d", got, int64(haEpoch+30))
	}
	// The standby can reconstruct the pre-apply file content (blob replicated).
	blob, err := standbyRepos.Blobs.Get(ctx, hash)
	if err != nil || blob != preContent {
		t.Errorf("standby Blobs.Get = (%q, %v), want %q", blob, err, preContent)
	}
	if _, err := standbyRepos.Snapshots.Get(ctx, snapID); err != nil {
		t.Errorf("standby Snapshots.Get: %v", err)
	}
	if _, err := standbyRepos.Tokens.Get(ctx, "tok-1"); err != nil {
		t.Errorf("standby Tokens.Get: %v", err)
	}
	if hw, _ := dst.AuditHighWater(ctx); hw != 1 {
		t.Errorf("standby audit high water = %d, want 1", hw)
	}

	// Re-applying the same batch is idempotent (no duplicate rows / errors).
	if err := dst.Apply(ctx, batch); err != nil {
		t.Fatalf("Apply (replay): %v", err)
	}
}
