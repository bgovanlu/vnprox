package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Node timer statuses (node_timers.status). Mirrors the lifecycle a single
// per-node local commit-confirm timer moves through (T-304,
// docs/features/change-management.md §4): armed at step start, then either
// cancelled (the changeset was confirmed) or, if the deadline elapses first,
// resolved to rolled_back / rollback_failed.
const (
	NodeTimerArmed          = "armed"
	NodeTimerCancelled      = "cancelled"
	NodeTimerRolledBack     = "rolled_back"
	NodeTimerRollbackFailed = "rollback_failed"
)

// NodeTimer is one row of the node_timers table: this node's record of a
// rollback deadline it was asked to arm for a changeset it may otherwise
// know nothing about (see the 0003_node_timers.sql migration comment).
type NodeTimer struct {
	ChangesetID string
	Node        string
	PreContent  string
	Status      string
	Error       sql.NullString
	Deadline    int64
	ArmedAt     int64
	ResolvedAt  sql.NullInt64
}

// NodeTimerRepo is the node_timers table repository.
type NodeTimerRepo struct {
	db *DB
}

// NewNodeTimerRepo constructs a NodeTimerRepo.
func NewNodeTimerRepo(db *DB) *NodeTimerRepo { return &NodeTimerRepo{db: db} }

// Arm inserts or overwrites the (changesetID, node) timer row in the armed
// state — re-arming (the same coordinator retrying, or a fresh arm after an
// earlier one on the same key resolved) simply replaces the prior record.
func (r *NodeTimerRepo) Arm(ctx context.Context, t NodeTimer) error {
	_, err := r.db.sqlDB.ExecContext(ctx, `
		INSERT INTO node_timers (changeset_id, node, pre_content, deadline, status, armed_at, resolved_at, error)
		VALUES (?, ?, ?, ?, ?, ?, NULL, NULL)
		ON CONFLICT (changeset_id, node) DO UPDATE SET
			pre_content = excluded.pre_content,
			deadline    = excluded.deadline,
			status      = excluded.status,
			armed_at    = excluded.armed_at,
			resolved_at = NULL,
			error       = NULL`,
		t.ChangesetID, t.Node, t.PreContent, t.Deadline, NodeTimerArmed, t.ArmedAt,
	)
	if err != nil {
		return fmt.Errorf("store: arming node timer for changeset %s node %s: %w", t.ChangesetID, t.Node, err)
	}
	return nil
}

// Resolve moves a timer row to a terminal status (cancelled, rolled_back, or
// rollback_failed), stamping resolvedAt and, for rollback_failed, the error
// detail. It is a no-op (not an error) if no row exists for the key, so a
// cancel that races an as-yet-unarmed timer degrades gracefully.
func (r *NodeTimerRepo) Resolve(ctx context.Context, changesetID, node, status string, resolvedAt int64, errDetail string) error {
	var errArg sql.NullString
	if errDetail != "" {
		errArg = sql.NullString{String: errDetail, Valid: true}
	}
	_, err := r.db.sqlDB.ExecContext(ctx, `
		UPDATE node_timers SET status = ?, resolved_at = ?, error = ?
		WHERE changeset_id = ? AND node = ?`,
		status, resolvedAt, errArg, changesetID, node,
	)
	if err != nil {
		return fmt.Errorf("store: resolving node timer for changeset %s node %s: %w", changesetID, node, err)
	}
	return nil
}

// Get returns the timer row for (changesetID, node), or ErrNotFound if this
// node was never asked to arm one.
func (r *NodeTimerRepo) Get(ctx context.Context, changesetID, node string) (NodeTimer, error) {
	row := r.db.sqlDB.QueryRowContext(ctx, `
		SELECT changeset_id, node, pre_content, deadline, status, armed_at, resolved_at, error
		FROM node_timers WHERE changeset_id = ? AND node = ?`, changesetID, node,
	)
	t, err := scanNodeTimer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return NodeTimer{}, ErrNotFound
	}
	return t, err
}

// ListByStatus returns every timer row currently in status, oldest-armed
// first — used both at daemon startup (re-arming every still-armed timer,
// mirroring change.Service.ArmPendingRollbacks) and by coordinator-side
// reconciliation scans.
func (r *NodeTimerRepo) ListByStatus(ctx context.Context, status string) ([]NodeTimer, error) {
	rows, err := r.db.sqlDB.QueryContext(ctx, `
		SELECT changeset_id, node, pre_content, deadline, status, armed_at, resolved_at, error
		FROM node_timers WHERE status = ? ORDER BY armed_at ASC`, status,
	)
	if err != nil {
		return nil, fmt.Errorf("store: listing node timers with status %s: %w", status, err)
	}
	defer func() { _ = rows.Close() }()

	var out []NodeTimer
	for rows.Next() {
		t, err := scanNodeTimer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing node timers with status %s: %w", status, err)
	}
	return out, nil
}

func scanNodeTimer(row rowScanner) (NodeTimer, error) {
	var t NodeTimer
	if err := row.Scan(&t.ChangesetID, &t.Node, &t.PreContent, &t.Deadline, &t.Status, &t.ArmedAt, &t.ResolvedAt, &t.Error); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NodeTimer{}, err
		}
		return NodeTimer{}, fmt.Errorf("store: scanning node timer: %w", err)
	}
	return t, nil
}
