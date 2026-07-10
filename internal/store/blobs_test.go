package store

import (
	"context"
	"testing"
)

func TestBlobRepo_PutGetRoundTrip(t *testing.T) {
	db := openTestDB(t)
	repo := NewBlobRepo(db)
	ctx := context.Background()

	content := "auto lo\niface lo inet loopback\n"
	hash, err := repo.Put(ctx, content)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if hash == "" {
		t.Fatal("Put returned empty hash")
	}

	got, err := repo.Get(ctx, hash)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != content {
		t.Errorf("Get() = %q, want %q", got, content)
	}
}

func TestBlobRepo_PutDedup(t *testing.T) {
	db := openTestDB(t)
	repo := NewBlobRepo(db)
	ctx := context.Background()

	content := "iface eth0 inet manual\n"
	h1, err := repo.Put(ctx, content)
	if err != nil {
		t.Fatalf("Put 1: %v", err)
	}
	h2, err := repo.Put(ctx, content)
	if err != nil {
		t.Fatalf("Put 2 (same content): %v", err)
	}
	if h1 != h2 {
		t.Fatalf("hashes differ for identical content: %s vs %s", h1, h2)
	}

	var count int
	if err := db.sqlDB.QueryRowContext(ctx, `SELECT count(*) FROM blobs WHERE sha256 = ?`, h1).Scan(&count); err != nil {
		t.Fatalf("counting blob rows: %v", err)
	}
	if count != 1 {
		t.Errorf("blob row count = %d, want 1 (dedup)", count)
	}
}

func TestBlobRepo_GetNotFound(t *testing.T) {
	db := openTestDB(t)
	repo := NewBlobRepo(db)
	if _, err := repo.Get(context.Background(), "deadbeef"); err != ErrNotFound {
		t.Errorf("Get(missing) = %v, want ErrNotFound", err)
	}
}

func TestBlobRepo_PruneOrphans(t *testing.T) {
	db := openTestDB(t)
	blobs := NewBlobRepo(db)
	snapshots := NewSnapshotRepo(db)
	ctx := context.Background()

	kept, err := blobs.Put(ctx, "kept content")
	if err != nil {
		t.Fatalf("Put kept: %v", err)
	}
	orphan, err := blobs.Put(ctx, "orphaned content")
	if err != nil {
		t.Fatalf("Put orphan: %v", err)
	}

	if insertErr := snapshots.Insert(ctx, Snapshot{ID: "snap-1", TakenAt: 1, Kind: "manual", FilesJSON: "[]"}); insertErr != nil {
		t.Fatalf("Insert snapshot: %v", insertErr)
	}
	if filesErr := snapshots.InsertFiles(ctx, []SnapshotFileRef{
		{SnapshotID: "snap-1", Node: "pve1", Path: "/etc/network/interfaces", SHA256: kept},
	}); filesErr != nil {
		t.Fatalf("InsertFiles: %v", filesErr)
	}

	n, err := blobs.PruneOrphans(ctx)
	if err != nil {
		t.Fatalf("PruneOrphans: %v", err)
	}
	if n != 1 {
		t.Fatalf("PruneOrphans deleted %d, want 1", n)
	}
	if _, err := blobs.Get(ctx, orphan); err != ErrNotFound {
		t.Errorf("orphan blob still present: err = %v", err)
	}
	if _, err := blobs.Get(ctx, kept); err != nil {
		t.Errorf("kept blob was deleted: %v", err)
	}
}
