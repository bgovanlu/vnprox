package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ChangesetApproval is the changeset_approvals table's one row per changeset
// (T-2003): its current review-approval decision. Absent (ErrNotFound on
// Get) is the implicit "none" state — the only state a pre-T-2003 changeset
// can ever be in.
type ChangesetApproval struct {
	ChangesetID string
	Status      string // approved|rejected
	DecidedBy   string
	Reason      string
	DecidedAt   int64
}

// ChangesetApprovalRepo is the changeset_approvals table repository.
type ChangesetApprovalRepo struct {
	db *DB
}

// NewChangesetApprovalRepo constructs a ChangesetApprovalRepo.
func NewChangesetApprovalRepo(db *DB) *ChangesetApprovalRepo {
	return &ChangesetApprovalRepo{db: db}
}

// Get returns the current decision for changesetID, or ErrNotFound if none
// has ever been recorded.
func (r *ChangesetApprovalRepo) Get(ctx context.Context, changesetID string) (ChangesetApproval, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT changeset_id, status, decided_by, reason, decided_at FROM changeset_approvals WHERE changeset_id = ?`, changesetID,
	)
	var a ChangesetApproval
	err := row.Scan(&a.ChangesetID, &a.Status, &a.DecidedBy, &a.Reason, &a.DecidedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ChangesetApproval{}, ErrNotFound
	}
	if err != nil {
		return ChangesetApproval{}, fmt.Errorf("store: reading approval state for changeset %s: %w", changesetID, err)
	}
	return a, nil
}

// Upsert records (or replaces) the decision for a.ChangesetID — a changeset
// may be rejected and later approved (or vice versa); only the latest
// decision is kept (this is a live apply gate, not an approval history —
// the audit log is where that history lives).
func (r *ChangesetApprovalRepo) Upsert(ctx context.Context, a ChangesetApproval) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO changeset_approvals (changeset_id, status, decided_by, reason, decided_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (changeset_id) DO UPDATE SET
			status     = excluded.status,
			decided_by = excluded.decided_by,
			reason     = excluded.reason,
			decided_at = excluded.decided_at`,
		a.ChangesetID, a.Status, a.DecidedBy, a.Reason, a.DecidedAt,
	)
	if err != nil {
		return fmt.Errorf("store: recording approval state for changeset %s: %w", a.ChangesetID, err)
	}
	return nil
}

// Clear removes any recorded decision for changesetID. Called whenever a
// draft/validated changeset's ops are replaced (change.Service.UpdateDraft):
// an approval decision was made against a specific set of ops and must never
// silently carry over to a materially different changeset — mirrors
// StatusValidated's own "editing invalidates" rule, applied to approval
// instead of validation. Not an error to clear an already-absent decision.
func (r *ChangesetApprovalRepo) Clear(ctx context.Context, changesetID string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM changeset_approvals WHERE changeset_id = ?`, changesetID); err != nil {
		return fmt.Errorf("store: clearing approval state for changeset %s: %w", changesetID, err)
	}
	return nil
}
