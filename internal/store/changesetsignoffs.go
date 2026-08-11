package store

import (
	"context"
	"fmt"
)

// ChangesetSignoff is the changeset_signoffs table's one row per
// (changeset, principal) (T-2604): one named person's standing endorsement
// of a changeset's current ops.
//
// It is deliberately keyed on the PRINCIPAL, not on a session, a token, or
// an approval event: the two-person rule counts people, and the primary key
// is what makes that a storage-level guarantee rather than a counting
// convention (see the migration's own note).
type ChangesetSignoff struct {
	ChangesetID string
	Principal   string
	DecidedAt   int64
}

// ChangesetSignoffRepo is the changeset_signoffs table repository.
type ChangesetSignoffRepo struct {
	db *DB
}

// NewChangesetSignoffRepo constructs a ChangesetSignoffRepo.
func NewChangesetSignoffRepo(db *DB) *ChangesetSignoffRepo {
	return &ChangesetSignoffRepo{db: db}
}

// Upsert records that principal endorses changesetID as of decidedAt. The
// same principal approving again (through another session, another token, or
// simply twice) updates the timestamp on their existing row — it never adds
// a second one.
func (r *ChangesetSignoffRepo) Upsert(ctx context.Context, s ChangesetSignoff) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO changeset_signoffs (changeset_id, principal, decided_at)
		VALUES (?, ?, ?)
		ON CONFLICT (changeset_id, principal) DO UPDATE SET
			decided_at = excluded.decided_at`,
		s.ChangesetID, s.Principal, s.DecidedAt,
	)
	if err != nil {
		return fmt.Errorf("store: recording sign-off by %s on changeset %s: %w", s.Principal, s.ChangesetID, err)
	}
	return nil
}

// List returns every sign-off on changesetID, ordered by principal so the
// set is reported deterministically wherever it is displayed or audited.
func (r *ChangesetSignoffRepo) List(ctx context.Context, changesetID string) ([]ChangesetSignoff, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT changeset_id, principal, decided_at FROM changeset_signoffs
		WHERE changeset_id = ? ORDER BY principal`, changesetID)
	if err != nil {
		return nil, fmt.Errorf("store: listing sign-offs for changeset %s: %w", changesetID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []ChangesetSignoff
	for rows.Next() {
		var s ChangesetSignoff
		if err := rows.Scan(&s.ChangesetID, &s.Principal, &s.DecidedAt); err != nil {
			return nil, fmt.Errorf("store: scanning sign-off for changeset %s: %w", changesetID, err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing sign-offs for changeset %s: %w", changesetID, err)
	}
	return out, nil
}

// Delete removes principal's sign-off from changesetID — the rejection path
// (an endorsement withdrawn is not an endorsement). Not an error when there
// is nothing to remove.
func (r *ChangesetSignoffRepo) Delete(ctx context.Context, changesetID, principal string) error {
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM changeset_signoffs WHERE changeset_id = ? AND principal = ?`, changesetID, principal); err != nil {
		return fmt.Errorf("store: clearing sign-off by %s on changeset %s: %w", principal, changesetID, err)
	}
	return nil
}

// Clear removes every sign-off on changesetID. Called whenever a draft's ops
// are replaced (change.Service.UpdateDraft): people endorsed a specific set
// of ops, and that endorsement must never silently carry over to a
// materially different changeset — the same rule changeset_approvals'
// Clear already applies to the single review decision.
func (r *ChangesetSignoffRepo) Clear(ctx context.Context, changesetID string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM changeset_signoffs WHERE changeset_id = ?`, changesetID); err != nil {
		return fmt.Errorf("store: clearing sign-offs for changeset %s: %w", changesetID, err)
	}
	return nil
}
