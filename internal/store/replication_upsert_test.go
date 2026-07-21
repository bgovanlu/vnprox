package store

import (
	"context"
	"database/sql"
	"testing"
)

// These cover the id-preserving upsert/replication feed methods T-1704's HA
// replication relies on (ChangesetRepo.Upsert, APITokenRepo.Upsert,
// AuditRepo.UpsertReplicated/ListSince/MaxAuditID).

func TestChangesetRepo_Upsert_InsertThenOverwrite(t *testing.T) {
	db := openTestDB(t)
	repo := NewChangesetRepo(db)
	ctx := context.Background()

	c := Changeset{ID: "cs-1", Title: "t", Author: "a", Status: "awaiting_confirm", OpsJSON: "[]", ConfirmDeadline: sql.NullInt64{Int64: 1_800_000_030, Valid: true}, CreatedAt: 1, UpdatedAt: 2}
	if err := repo.Upsert(ctx, c); err != nil { // insert path
		t.Fatalf("Upsert insert: %v", err)
	}
	c.Status = "committed"
	c.ConfirmDeadline = sql.NullInt64{}
	c.UpdatedAt = 5
	if err := repo.Upsert(ctx, c); err != nil { // overwrite path
		t.Fatalf("Upsert overwrite: %v", err)
	}
	got, err := repo.Get(ctx, "cs-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != "committed" || got.ConfirmDeadline.Valid || got.UpdatedAt != 5 || got.CreatedAt != 1 {
		t.Errorf("Get() = %+v, want committed / no deadline / updated 5 / created preserved 1", got)
	}
}

func TestAPITokenRepo_Upsert_InsertThenOverwrite(t *testing.T) {
	db := openTestDB(t)
	repo := NewAPITokenRepo(db)
	ctx := context.Background()

	tok := APIToken{ID: "tok-1", Name: "n", TokenHash: "h", ScopesJSON: `["netRead"]`, CreatedBy: "a", CreatedAt: 1}
	if err := repo.Upsert(ctx, tok); err != nil {
		t.Fatalf("Upsert insert: %v", err)
	}
	tok.RevokedAt = sql.NullInt64{Int64: 99, Valid: true} // a revocation replicated from the active
	if err := repo.Upsert(ctx, tok); err != nil {
		t.Fatalf("Upsert overwrite: %v", err)
	}
	got, err := repo.Get(ctx, "tok-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.RevokedAt.Valid || got.RevokedAt.Int64 != 99 {
		t.Errorf("Get().RevokedAt = %+v, want valid 99 (revocation propagated)", got.RevokedAt)
	}
}

func TestAuditRepo_ReplicationFeed(t *testing.T) {
	db := openTestDB(t)
	repo := NewAuditRepo(db)
	ctx := context.Background()

	if max, err := repo.MaxAuditID(ctx); err != nil || max != 0 {
		t.Fatalf("MaxAuditID empty = (%d, %v), want (0, nil)", max, err)
	}

	// Simulate the "active" appending rows, then replicate them by id onto a
	// second (standby) store.
	for i := 0; i < 3; i++ {
		if _, err := repo.Append(ctx, AuditEntry{At: int64(100 + i), Username: "a", Action: "changeset.apply", Result: "success"}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	max, err := repo.MaxAuditID(ctx)
	if err != nil || max != 3 {
		t.Fatalf("MaxAuditID = (%d, %v), want (3, nil)", max, err)
	}

	feed, err := repo.ListSince(ctx, 1, 0)
	if err != nil {
		t.Fatalf("ListSince: %v", err)
	}
	if len(feed) != 2 || feed[0].ID != 2 || feed[1].ID != 3 {
		t.Fatalf("ListSince(1) = %+v, want ids [2,3] ascending", feed)
	}

	standby := NewAuditRepo(openTestDB(t))
	for _, e := range feed {
		if upErr := standby.UpsertReplicated(ctx, e); upErr != nil {
			t.Fatalf("UpsertReplicated: %v", upErr)
		}
	}
	// Re-applying is a no-op (append-only immutability), not a duplicate.
	if err = standby.UpsertReplicated(ctx, feed[0]); err != nil {
		t.Fatalf("UpsertReplicated replay: %v", err)
	}
	got, err := standby.Get(ctx, 2)
	if err != nil {
		t.Fatalf("standby Get(2): %v", err)
	}
	if got.At != 101 {
		t.Errorf("replicated entry 2 At = %d, want 101 (id and fields preserved)", got.At)
	}
	if smax, _ := standby.MaxAuditID(ctx); smax != 3 {
		t.Errorf("standby MaxAuditID = %d, want 3", smax)
	}
}
