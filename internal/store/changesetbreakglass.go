package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ChangesetBreakGlass is the changeset_breakglass table's one row per
// changeset that has had emergency break-glass invoked on it (T-2604): the
// reasoned, audited override of the two-person requirement.
//
// OpsFingerprint pins the override to the ops it was invoked for, so an
// edit after the fact cannot inherit it (see the migration's note).
type ChangesetBreakGlass struct {
	ChangesetID    string
	Reason         string
	InvokedBy      string
	OpsFingerprint string
	InvokedAt      int64
}

// ChangesetBreakGlassRepo is the changeset_breakglass table repository.
// There is deliberately no Delete: these rows are the evidence trail behind
// the break-glass finding, and that finding's 24-hour acknowledgement floor
// would be trivially defeated by deleting the row it is computed from. Only
// deleting the changeset itself cascades one away.
type ChangesetBreakGlassRepo struct {
	db *DB
}

// NewChangesetBreakGlassRepo constructs a ChangesetBreakGlassRepo.
func NewChangesetBreakGlassRepo(db *DB) *ChangesetBreakGlassRepo {
	return &ChangesetBreakGlassRepo{db: db}
}

// Upsert records (or replaces) the break-glass invocation for
// b.ChangesetID. Re-invoking after an edit replaces the row — the finding
// stays raised either way, and its 24-hour floor restarts from the newer
// invocation, which is the honest reading: a second override is a second
// event.
func (r *ChangesetBreakGlassRepo) Upsert(ctx context.Context, b ChangesetBreakGlass) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO changeset_breakglass (changeset_id, reason, invoked_by, invoked_at, ops_fingerprint)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (changeset_id) DO UPDATE SET
			reason          = excluded.reason,
			invoked_by      = excluded.invoked_by,
			invoked_at      = excluded.invoked_at,
			ops_fingerprint = excluded.ops_fingerprint`,
		b.ChangesetID, b.Reason, b.InvokedBy, b.InvokedAt, b.OpsFingerprint,
	)
	if err != nil {
		return fmt.Errorf("store: recording break-glass for changeset %s: %w", b.ChangesetID, err)
	}
	return nil
}

// Get returns the break-glass record for changesetID, or ErrNotFound when
// none was ever invoked.
func (r *ChangesetBreakGlassRepo) Get(ctx context.Context, changesetID string) (ChangesetBreakGlass, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT changeset_id, reason, invoked_by, invoked_at, ops_fingerprint
		FROM changeset_breakglass WHERE changeset_id = ?`, changesetID)
	var b ChangesetBreakGlass
	err := row.Scan(&b.ChangesetID, &b.Reason, &b.InvokedBy, &b.InvokedAt, &b.OpsFingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return ChangesetBreakGlass{}, ErrNotFound
	}
	if err != nil {
		return ChangesetBreakGlass{}, fmt.Errorf("store: reading break-glass for changeset %s: %w", changesetID, err)
	}
	return b, nil
}

// List returns every break-glass record, newest first — the input the
// break-glass finding is computed from each findings cycle.
func (r *ChangesetBreakGlassRepo) List(ctx context.Context) ([]ChangesetBreakGlass, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT changeset_id, reason, invoked_by, invoked_at, ops_fingerprint
		FROM changeset_breakglass ORDER BY invoked_at DESC, changeset_id`)
	if err != nil {
		return nil, fmt.Errorf("store: listing break-glass records: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []ChangesetBreakGlass
	for rows.Next() {
		var b ChangesetBreakGlass
		if err := rows.Scan(&b.ChangesetID, &b.Reason, &b.InvokedBy, &b.InvokedAt, &b.OpsFingerprint); err != nil {
			return nil, fmt.Errorf("store: scanning break-glass record: %w", err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing break-glass records: %w", err)
	}
	return out, nil
}
