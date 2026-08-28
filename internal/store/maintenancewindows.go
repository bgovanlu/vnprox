// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// MaintenanceWindow is the maintenance_windows table's one row per declared
// node maintenance window (T-4007) — see migrations/0052_maintenance_windows
// .sql for why this is its own table rather than a reuse of policy_sets.
//
// Start/End are unix seconds, resolved ONCE at declare time from the
// operator's local wall-clock start/end plus the mandatory Zone
// (internal/change/maintenance.go owns that conversion; this layer only
// stores the result). Expiry is evaluated at READ time by internal/findings
// against a clock it is given, never by a sweeper here — the same
// finding_acks/0035 discipline, for the same reason: a daemon that is
// stopped, crashed, or simply not running a cleanup tick must not be able
// to leave a node muted past the instant its operator chose.
type MaintenanceWindow struct {
	ID        string
	Node      string
	Reason    string
	CreatedBy string
	Zone      string
	Start     int64
	End       int64
	CreatedAt int64
}

// MaintenanceWindowRepo is the maintenance_windows table repository.
type MaintenanceWindowRepo struct {
	db *DB
}

// NewMaintenanceWindowRepo constructs a MaintenanceWindowRepo.
func NewMaintenanceWindowRepo(db *DB) *MaintenanceWindowRepo { return &MaintenanceWindowRepo{db: db} }

// Create inserts a new maintenance window row. w.ID must already be set
// (the caller mints it — internal/change uses store.NewULID, mirroring
// every other app-owned id in this package).
func (r *MaintenanceWindowRepo) Create(ctx context.Context, w MaintenanceWindow) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO maintenance_windows (id, node, reason, created_by, zone, start_epoch, end_epoch, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		w.ID, w.Node, w.Reason, w.CreatedBy, w.Zone, w.Start, w.End, w.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("store: recording maintenance window for node %s: %w", w.Node, err)
	}
	return nil
}

// Get returns the maintenance window with the given id, or ErrNotFound.
func (r *MaintenanceWindowRepo) Get(ctx context.Context, id string) (MaintenanceWindow, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, node, reason, created_by, zone, start_epoch, end_epoch, created_at
		FROM maintenance_windows WHERE id = ?`, id)
	var w MaintenanceWindow
	err := row.Scan(&w.ID, &w.Node, &w.Reason, &w.CreatedBy, &w.Zone, &w.Start, &w.End, &w.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return MaintenanceWindow{}, ErrNotFound
	}
	if err != nil {
		return MaintenanceWindow{}, fmt.Errorf("store: reading maintenance window %s: %w", id, err)
	}
	return w, nil
}

// List returns every declared maintenance window, expired ones included —
// same "expiry is the caller's decision, not this layer's" contract
// FindingAckRepo.List documents, and for the identical reason: a window
// that just ended is still evidence of what was declared, and internal/
// findings (not this repository) is what decides whether it is still
// suppressing anything right now.
func (r *MaintenanceWindowRepo) List(ctx context.Context) ([]MaintenanceWindow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, node, reason, created_by, zone, start_epoch, end_epoch, created_at
		FROM maintenance_windows ORDER BY start_epoch, id`)
	if err != nil {
		return nil, fmt.Errorf("store: listing maintenance windows: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []MaintenanceWindow
	for rows.Next() {
		var w MaintenanceWindow
		if err := rows.Scan(&w.ID, &w.Node, &w.Reason, &w.CreatedBy, &w.Zone, &w.Start, &w.End, &w.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: scanning maintenance window: %w", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing maintenance windows: %w", err)
	}
	return out, nil
}

// Delete removes the maintenance window with the given id. Not an error to
// delete an absent one — an operator ending a declared window early (or
// correcting a mistaken declaration) must always be able to, the same
// "always clearable" contract FindingAckRepo.Delete documents for an
// acknowledgement.
func (r *MaintenanceWindowRepo) Delete(ctx context.Context, id string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM maintenance_windows WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: clearing maintenance window %s: %w", id, err)
	}
	return nil
}
