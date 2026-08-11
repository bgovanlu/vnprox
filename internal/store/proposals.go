package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ChangesetProposal is one row of the changeset_proposals table (T-2702):
// the pull request a changeset was proposed as, against the spec repository.
//
// It is app-owned bookkeeping about an external object — never a shadow copy
// of anything PVE owns, and never a credential: the push token has no field
// here and no writer of this row ever holds one.
//
//nolint:govet // fieldalignment: field order mirrors the migration's column order, which is the reviewable shape.
type ChangesetProposal struct {
	ChangesetID string
	Remote      string
	Branch      string
	Path        string
	CommitSHA   string
	PRID        string
	PRURL       string
	ProposedBy  string
	CreatedAt   int64
	UpdatedAt   int64
}

// ChangesetProposalRepo is the changeset_proposals table repository.
type ChangesetProposalRepo struct {
	db *DB
}

// NewChangesetProposalRepo constructs a ChangesetProposalRepo.
func NewChangesetProposalRepo(db *DB) *ChangesetProposalRepo {
	return &ChangesetProposalRepo{db: db}
}

// Get returns the proposal recorded for changesetID, or ErrNotFound when the
// changeset has never been proposed.
func (r *ChangesetProposalRepo) Get(ctx context.Context, changesetID string) (ChangesetProposal, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT changeset_id, remote, branch, path, commit_sha, pr_id, pr_url, proposed_by, created_at, updated_at
		FROM changeset_proposals WHERE changeset_id = ?`, changesetID)
	var p ChangesetProposal
	err := row.Scan(&p.ChangesetID, &p.Remote, &p.Branch, &p.Path, &p.CommitSHA, &p.PRID, &p.PRURL, &p.ProposedBy, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ChangesetProposal{}, ErrNotFound
	}
	if err != nil {
		return ChangesetProposal{}, fmt.Errorf("store: reading proposal for changeset %s: %w", changesetID, err)
	}
	return p, nil
}

// Upsert records (or brings up to date) the proposal for p.ChangesetID.
// created_at is preserved across an update — the row's identity is the
// changeset, and re-proposing updates one pull request rather than opening a
// second (T-2702 AC4), so the first proposal's timestamp is the one that
// means something.
func (r *ChangesetProposalRepo) Upsert(ctx context.Context, p ChangesetProposal) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO changeset_proposals (changeset_id, remote, branch, path, commit_sha, pr_id, pr_url, proposed_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (changeset_id) DO UPDATE SET
			remote      = excluded.remote,
			branch      = excluded.branch,
			path        = excluded.path,
			commit_sha  = excluded.commit_sha,
			pr_id       = excluded.pr_id,
			pr_url      = excluded.pr_url,
			proposed_by = excluded.proposed_by,
			updated_at  = excluded.updated_at`,
		p.ChangesetID, p.Remote, p.Branch, p.Path, p.CommitSHA, p.PRID, p.PRURL, p.ProposedBy, p.CreatedAt, p.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("store: recording proposal for changeset %s: %w", p.ChangesetID, err)
	}
	return nil
}
