// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Staged-apply states (ChangesetApplyStage.State, T-2602). The set is
// closed and deliberately tiny: a staged apply is either paused waiting for
// a decision, or executing the decision. Anything else has no row at all.
const (
	// StageCanaryHold: the canary stage completed and the sequence is paused.
	// The changeset is neither applied nor rolled back; AppliedNodes have been
	// mutated, PendingNodes have not been contacted for a write at all.
	StageCanaryHold = "canary_hold"
	// StagePromoting: a continue (manual or auto-gated) is executing the
	// remaining stage right now. A daemon that crashes here cannot know how
	// far the remaining stage got, so recovery restores everything the plan
	// touches and fails the changeset — the same stance recovery of an
	// interrupted ordinary apply already takes.
	StagePromoting = "promoting"
)

// ChangesetApplyStage is one row of the changeset_apply_stages table
// (docs/data-model.md §2): the persisted paused state of a T-2602 staged
// apply.
//
// It exists because the pause must survive the process. Holding it in
// memory would make a daemon restart mid-hold indistinguishable from a
// changeset that was never staged at all — the changeset would be
// half-applied with nothing recording which half, which is precisely the
// "unknown state" the card forbids.
type ChangesetApplyStage struct {
	ChangesetID  string
	State        string // canary_hold|promoting
	StrategyJSON string
	// AppliedNodesJSON and PendingNodesJSON are JSON string arrays. Applied
	// nodes are the ones an abort must restore; pending nodes are the ones an
	// abort must NOT contact, because nothing was ever written to them.
	AppliedNodesJSON string
	PendingNodesJSON string
	Author           string
	HoldStartedAt    int64
	HoldDeadline     int64
	ConfirmDeadline  int64
}

// ChangesetStageRepo is the changeset_apply_stages table repository.
type ChangesetStageRepo struct {
	db *DB
}

// NewChangesetStageRepo constructs a ChangesetStageRepo.
func NewChangesetStageRepo(db *DB) *ChangesetStageRepo { return &ChangesetStageRepo{db: db} }

// Get returns the staged-apply row for changesetID, or ErrNotFound when the
// changeset is not in a staged pause (which is every ordinary,
// all-at-once apply).
func (r *ChangesetStageRepo) Get(ctx context.Context, changesetID string) (ChangesetApplyStage, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT changeset_id, state, strategy_json, applied_nodes, pending_nodes, author,
		       hold_started_at, hold_deadline, confirm_deadline
		FROM changeset_apply_stages WHERE changeset_id = ?`, changesetID,
	)
	var s ChangesetApplyStage
	err := row.Scan(&s.ChangesetID, &s.State, &s.StrategyJSON, &s.AppliedNodesJSON, &s.PendingNodesJSON,
		&s.Author, &s.HoldStartedAt, &s.HoldDeadline, &s.ConfirmDeadline)
	if errors.Is(err, sql.ErrNoRows) {
		return ChangesetApplyStage{}, ErrNotFound
	}
	if err != nil {
		return ChangesetApplyStage{}, fmt.Errorf("store: reading staged-apply state for changeset %s: %w", changesetID, err)
	}
	return s, nil
}

// Upsert records (or replaces) the staged-apply row for s.ChangesetID.
func (r *ChangesetStageRepo) Upsert(ctx context.Context, s ChangesetApplyStage) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO changeset_apply_stages
			(changeset_id, state, strategy_json, applied_nodes, pending_nodes, author, hold_started_at, hold_deadline, confirm_deadline)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (changeset_id) DO UPDATE SET
			state            = excluded.state,
			strategy_json    = excluded.strategy_json,
			applied_nodes    = excluded.applied_nodes,
			pending_nodes    = excluded.pending_nodes,
			author           = excluded.author,
			hold_started_at  = excluded.hold_started_at,
			hold_deadline    = excluded.hold_deadline,
			confirm_deadline = excluded.confirm_deadline`,
		s.ChangesetID, s.State, s.StrategyJSON, s.AppliedNodesJSON, s.PendingNodesJSON,
		s.Author, s.HoldStartedAt, s.HoldDeadline, s.ConfirmDeadline,
	)
	if err != nil {
		return fmt.Errorf("store: recording staged-apply state for changeset %s: %w", s.ChangesetID, err)
	}
	return nil
}

// Delete removes changesetID's staged-apply row. Deleting a row that does
// not exist is not an error: every terminal path deletes unconditionally,
// so that a changeset can never be left carrying a stale pause.
func (r *ChangesetStageRepo) Delete(ctx context.Context, changesetID string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM changeset_apply_stages WHERE changeset_id = ?`, changesetID); err != nil {
		return fmt.Errorf("store: deleting staged-apply state for changeset %s: %w", changesetID, err)
	}
	return nil
}

// List returns every staged-apply row, oldest hold first — the daemon's
// startup recovery sweep (T-2602 AC4).
func (r *ChangesetStageRepo) List(ctx context.Context) ([]ChangesetApplyStage, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT changeset_id, state, strategy_json, applied_nodes, pending_nodes, author,
		       hold_started_at, hold_deadline, confirm_deadline
		FROM changeset_apply_stages ORDER BY hold_started_at ASC, changeset_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: listing staged-apply state: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []ChangesetApplyStage
	for rows.Next() {
		var s ChangesetApplyStage
		if err := rows.Scan(&s.ChangesetID, &s.State, &s.StrategyJSON, &s.AppliedNodesJSON, &s.PendingNodesJSON,
			&s.Author, &s.HoldStartedAt, &s.HoldDeadline, &s.ConfirmDeadline); err != nil {
			return nil, fmt.Errorf("store: scanning staged-apply state: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating staged-apply state: %w", err)
	}
	return out, nil
}
