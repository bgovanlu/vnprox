package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ChangesetSDNPendingAck is the changeset_sdn_pending_acks table's one row
// per changeset (T-3101-followup-01): the operator's latest server-recorded
// acknowledgement of foreign SDN pending state that a changeset's sdn.apply
// step would additionally commit. Absent (ErrNotFound on Get) means no
// acknowledgement has ever been recorded — the implicit state every
// changeset starts in, and the only state a pre-T-3101-followup-01
// changeset can ever be in.
type ChangesetSDNPendingAck struct {
	ChangesetID string
	// AcknowledgedBy is the identity that acknowledged, mirroring
	// ChangesetApproval.DecidedBy.
	AcknowledgedBy string
	// EntriesJSON is the exact []change.SDNPendingEntry the operator was
	// shown at acknowledgement time, JSON-marshaled — internal/change's
	// isSDNForeignPendingAcknowledged compares a fresh live read against
	// this, not merely a boolean, so a foreign-pending set that changed
	// since the operator last looked is never silently treated as covered.
	EntriesJSON    string
	AcknowledgedAt int64
}

// ChangesetSDNPendingAckRepo is the changeset_sdn_pending_acks table
// repository, mirroring ChangesetApprovalRepo's shape exactly.
type ChangesetSDNPendingAckRepo struct {
	db *DB
}

// NewChangesetSDNPendingAckRepo constructs a ChangesetSDNPendingAckRepo.
func NewChangesetSDNPendingAckRepo(db *DB) *ChangesetSDNPendingAckRepo {
	return &ChangesetSDNPendingAckRepo{db: db}
}

// Get returns the current acknowledgement for changesetID, or ErrNotFound
// if none has ever been recorded.
func (r *ChangesetSDNPendingAckRepo) Get(ctx context.Context, changesetID string) (ChangesetSDNPendingAck, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT changeset_id, acknowledged_by, entries_json, acknowledged_at
		FROM changeset_sdn_pending_acks WHERE changeset_id = ?`, changesetID,
	)
	var a ChangesetSDNPendingAck
	err := row.Scan(&a.ChangesetID, &a.AcknowledgedBy, &a.EntriesJSON, &a.AcknowledgedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ChangesetSDNPendingAck{}, ErrNotFound
	}
	if err != nil {
		return ChangesetSDNPendingAck{}, fmt.Errorf("store: reading sdn pending ack for changeset %s: %w", changesetID, err)
	}
	return a, nil
}

// Upsert records (or replaces) the acknowledgement for a.ChangesetID — only
// the latest acknowledgement is kept (the audit log is where the history
// lives, exactly like ChangesetApprovalRepo.Upsert's own doc comment).
func (r *ChangesetSDNPendingAckRepo) Upsert(ctx context.Context, a ChangesetSDNPendingAck) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO changeset_sdn_pending_acks (changeset_id, acknowledged_by, entries_json, acknowledged_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (changeset_id) DO UPDATE SET
			acknowledged_by = excluded.acknowledged_by,
			entries_json    = excluded.entries_json,
			acknowledged_at = excluded.acknowledged_at`,
		a.ChangesetID, a.AcknowledgedBy, a.EntriesJSON, a.AcknowledgedAt,
	)
	if err != nil {
		return fmt.Errorf("store: recording sdn pending ack for changeset %s: %w", a.ChangesetID, err)
	}
	return nil
}

// Clear removes any recorded acknowledgement for changesetID. Called
// whenever a draft/validated changeset's ops are replaced
// (change.Service.UpdateDraft), mirroring ChangesetApprovalRepo.Clear: an
// acknowledgement was made against a specific set of ops (and a specific
// foreign-pending snapshot) and must never silently carry over to a
// materially different changeset. Not an error to clear an already-absent
// acknowledgement.
func (r *ChangesetSDNPendingAckRepo) Clear(ctx context.Context, changesetID string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM changeset_sdn_pending_acks WHERE changeset_id = ?`, changesetID); err != nil {
		return fmt.Errorf("store: clearing sdn pending ack for changeset %s: %w", changesetID, err)
	}
	return nil
}
