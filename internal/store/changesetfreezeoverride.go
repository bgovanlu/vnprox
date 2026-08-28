// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ChangesetFreezeOverride is the changeset_freeze_override table's one row
// per changeset that has had T-4006's freeze-window override invoked on it
// — the reasoned, audited override of a declared freeze-window policy
// deny. See migrations/0051_freeze_override.sql for why this is its own
// table rather than a reuse of changeset_breakglass.
//
// OpsFingerprint pins the override to the ops it was invoked for, so an
// edit after the fact cannot inherit it — mirrors ChangesetBreakGlass's
// identical field for the identical reason.
type ChangesetFreezeOverride struct {
	ChangesetID    string
	Reason         string
	InvokedBy      string
	OpsFingerprint string
	InvokedAt      int64
}

// ChangesetFreezeOverrideRepo is the changeset_freeze_override table
// repository. Deliberately no Delete: these rows are the evidence trail
// behind a freeze-window bypass; only the changeset's own deletion cascades
// one away.
type ChangesetFreezeOverrideRepo struct {
	db *DB
}

// NewChangesetFreezeOverrideRepo constructs a ChangesetFreezeOverrideRepo.
func NewChangesetFreezeOverrideRepo(db *DB) *ChangesetFreezeOverrideRepo {
	return &ChangesetFreezeOverrideRepo{db: db}
}

// Upsert records (or replaces) the freeze override for o.ChangesetID.
// Re-invoking after an edit replaces the row: a second override is a
// second event, and the ops_fingerprint moves with it.
func (r *ChangesetFreezeOverrideRepo) Upsert(ctx context.Context, o ChangesetFreezeOverride) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO changeset_freeze_override (changeset_id, reason, invoked_by, invoked_at, ops_fingerprint)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (changeset_id) DO UPDATE SET
			reason          = excluded.reason,
			invoked_by      = excluded.invoked_by,
			invoked_at      = excluded.invoked_at,
			ops_fingerprint = excluded.ops_fingerprint`,
		o.ChangesetID, o.Reason, o.InvokedBy, o.InvokedAt, o.OpsFingerprint,
	)
	if err != nil {
		return fmt.Errorf("store: recording freeze override for changeset %s: %w", o.ChangesetID, err)
	}
	return nil
}

// Get returns the freeze-override record for changesetID, or ErrNotFound
// when none was ever invoked.
func (r *ChangesetFreezeOverrideRepo) Get(ctx context.Context, changesetID string) (ChangesetFreezeOverride, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT changeset_id, reason, invoked_by, invoked_at, ops_fingerprint
		FROM changeset_freeze_override WHERE changeset_id = ?`, changesetID)
	var o ChangesetFreezeOverride
	err := row.Scan(&o.ChangesetID, &o.Reason, &o.InvokedBy, &o.InvokedAt, &o.OpsFingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return ChangesetFreezeOverride{}, ErrNotFound
	}
	if err != nil {
		return ChangesetFreezeOverride{}, fmt.Errorf("store: reading freeze override for changeset %s: %w", changesetID, err)
	}
	return o, nil
}

// List returns every freeze-override record, newest first.
func (r *ChangesetFreezeOverrideRepo) List(ctx context.Context) ([]ChangesetFreezeOverride, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT changeset_id, reason, invoked_by, invoked_at, ops_fingerprint
		FROM changeset_freeze_override ORDER BY invoked_at DESC, changeset_id`)
	if err != nil {
		return nil, fmt.Errorf("store: listing freeze override records: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []ChangesetFreezeOverride
	for rows.Next() {
		var o ChangesetFreezeOverride
		if err := rows.Scan(&o.ChangesetID, &o.Reason, &o.InvokedBy, &o.InvokedAt, &o.OpsFingerprint); err != nil {
			return nil, fmt.Errorf("store: scanning freeze override record: %w", err)
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing freeze override records: %w", err)
	}
	return out, nil
}
