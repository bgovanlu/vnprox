package store

import (
	"context"
	"errors"
	"testing"
)

// TestChangesetProposalRepo_OneRowPerChangeset is the AC4 invariant at the
// storage layer: re-proposing updates the single row rather than accumulating
// proposals, and the FIRST proposal's timestamp survives the update — it is
// what "this changeset has been proposed since X" means.
func TestChangesetProposalRepo_OneRowPerChangeset(t *testing.T) {
	db := openTestDB(t)
	repo := NewChangesetProposalRepo(db)
	ctx := context.Background()

	first := ChangesetProposal{
		ChangesetID: "cs-1", Remote: "https://github.com/org/infra (github)",
		Branch: "vnprox/changeset-cs-1", Path: "network/cluster.yaml",
		CommitSHA: "aaa", PRID: "42", PRURL: "https://github.test/org/infra/pull/42",
		ProposedBy: "brian", CreatedAt: 1_754_000_000, UpdatedAt: 1_754_000_000,
	}
	if err := repo.Upsert(ctx, first); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	second := first
	second.CommitSHA = "bbb"
	second.CreatedAt = 1_754_009_999 // a caller passing a fresh created_at must not win
	second.UpdatedAt = 1_754_009_999
	if err := repo.Upsert(ctx, second); err != nil {
		t.Fatalf("Upsert (second): %v", err)
	}

	got, err := repo.Get(ctx, "cs-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CommitSHA != "bbb" || got.UpdatedAt != 1_754_009_999 {
		t.Errorf("the second proposal did not update the row: %+v", got)
	}
	if got.CreatedAt != 1_754_000_000 {
		t.Errorf("created_at = %d, want the first proposal's %d", got.CreatedAt, 1_754_000_000)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM changeset_proposals`).Scan(&count); err != nil {
		t.Fatalf("counting proposals: %v", err)
	}
	if count != 1 {
		t.Errorf("changeset_proposals holds %d rows for one changeset, want 1", count)
	}

	// --- control: the table CAN hold a second changeset's proposal --------
	// Otherwise "exactly 1" above would be a property of nothing.
	other := first
	other.ChangesetID = "cs-2"
	if err := repo.Upsert(ctx, other); err != nil {
		t.Fatalf("Upsert (other changeset): %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM changeset_proposals`).Scan(&count); err != nil {
		t.Fatalf("counting proposals: %v", err)
	}
	if count != 2 {
		t.Fatalf("control failed: the table holds %d rows for two changesets, so the count above proves nothing", count)
	}
}

// TestChangesetProposalRepo_UnproposedIsNotFound: most changesets have no
// proposal, and that must be a plain answer rather than an error to interpret.
func TestChangesetProposalRepo_UnproposedIsNotFound(t *testing.T) {
	repo := NewChangesetProposalRepo(openTestDB(t))
	if _, err := repo.Get(context.Background(), "cs-never-proposed"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(unproposed) = %v, want ErrNotFound", err)
	}
}
