package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// FindingAck is the finding_acks table's one row per acknowledged finding
// (T-2402), keyed on the finding's stable id.
//
// ExpiresAt is unix seconds, or 0 for "until explicitly un-acked". This
// repository deliberately does NOT filter expired rows: whether an ack still
// applies is decided at read time by internal/findings against a clock it is
// given, so the decision is testable and so a stopped daemon can never leave
// a finding muted past its date. See the migration's own note.
type FindingAck struct {
	FindingID string
	Reason    string
	AckedBy   string
	AckedAt   int64
	ExpiresAt int64
}

// FindingAckRepo is the finding_acks table repository.
type FindingAckRepo struct {
	db *DB
}

// NewFindingAckRepo constructs a FindingAckRepo.
func NewFindingAckRepo(db *DB) *FindingAckRepo { return &FindingAckRepo{db: db} }

// Get returns the ack for findingID, or ErrNotFound if none is recorded.
// An expired row is still returned — expiry is the caller's decision, not
// this layer's.
func (r *FindingAckRepo) Get(ctx context.Context, findingID string) (FindingAck, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT finding_id, reason, acked_by, acked_at, expires_at FROM finding_acks WHERE finding_id = ?`, findingID,
	)
	var a FindingAck
	err := row.Scan(&a.FindingID, &a.Reason, &a.AckedBy, &a.AckedAt, &a.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return FindingAck{}, ErrNotFound
	}
	if err != nil {
		return FindingAck{}, fmt.Errorf("store: reading acknowledgement for finding %s: %w", findingID, err)
	}
	return a, nil
}

// List returns every recorded ack, expired ones included, keyed by finding
// id — the shape internal/findings needs to decorate a whole findings slice
// in one pass rather than one query per finding.
func (r *FindingAckRepo) List(ctx context.Context) (map[string]FindingAck, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT finding_id, reason, acked_by, acked_at, expires_at FROM finding_acks`)
	if err != nil {
		return nil, fmt.Errorf("store: listing finding acknowledgements: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]FindingAck)
	for rows.Next() {
		var a FindingAck
		if err := rows.Scan(&a.FindingID, &a.Reason, &a.AckedBy, &a.AckedAt, &a.ExpiresAt); err != nil {
			return nil, fmt.Errorf("store: scanning finding acknowledgement: %w", err)
		}
		out[a.FindingID] = a
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing finding acknowledgements: %w", err)
	}
	return out, nil
}

// Upsert records (or replaces) the acknowledgement for a.FindingID. Re-acking
// an already-acked finding replaces the reason, actor, and expiry — an
// operator extending a mute should not have to un-ack first.
func (r *FindingAckRepo) Upsert(ctx context.Context, a FindingAck) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO finding_acks (finding_id, reason, acked_by, acked_at, expires_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (finding_id) DO UPDATE SET
			reason     = excluded.reason,
			acked_by   = excluded.acked_by,
			acked_at   = excluded.acked_at,
			expires_at = excluded.expires_at`,
		a.FindingID, a.Reason, a.AckedBy, a.AckedAt, a.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("store: recording acknowledgement for finding %s: %w", a.FindingID, err)
	}
	return nil
}

// Delete removes any acknowledgement for findingID. Not an error to delete an
// absent one.
func (r *FindingAckRepo) Delete(ctx context.Context, findingID string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM finding_acks WHERE finding_id = ?`, findingID); err != nil {
		return fmt.Errorf("store: clearing acknowledgement for finding %s: %w", findingID, err)
	}
	return nil
}
