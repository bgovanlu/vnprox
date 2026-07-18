package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Schedule row statuses (changeset_schedules.status). Mirrors T-1103's
// lifecycle: a schedule is created pending, and resolves exactly once —
// either the scheduler fires it (fired), the window elapses with no fire
// (missed, missedWindowPolicy "skip"), a fresh re-validation/mgmt-path
// recheck at fire time aborts it (blocked), the resulting Apply call itself
// fails (failed), or the user cancels it before windowStart (cancelled).
const (
	ScheduleStatusPending   = "pending"
	ScheduleStatusFired     = "fired"
	ScheduleStatusMissed    = "missed"
	ScheduleStatusBlocked   = "blocked"
	ScheduleStatusFailed    = "failed"
	ScheduleStatusCancelled = "cancelled"
)

// ChangesetSchedule is one row of the changeset_schedules table (see the
// 0010_changeset_schedules.sql migration comment for the full design
// rationale): one maintenance-window apply, keyed by the changeset it
// belongs to.
type ChangesetSchedule struct {
	ChangesetID        string
	MissedWindowPolicy string
	CallbackTokenHash  string
	Status             string
	CreatedBy          string
	FiredAt            sql.NullInt64
	CancelledAt        sql.NullInt64
	WindowStart        int64
	WindowEnd          int64
	ConfirmTimeoutSec  int
	CreatedAt          int64
}

// ChangeScheduleRepo is the changeset_schedules table repository.
type ChangeScheduleRepo struct {
	db *DB
}

// NewChangeScheduleRepo constructs a ChangeScheduleRepo.
func NewChangeScheduleRepo(db *DB) *ChangeScheduleRepo { return &ChangeScheduleRepo{db: db} }

// Upsert inserts a new schedule row for s.ChangesetID, or replaces an
// existing one (docs/data-model.md's "one row per changeset" design — see
// the migration comment: scheduling again after an earlier schedule
// resolved replaces that row). Callers are responsible for refusing to
// upsert over a still-pending row (change.Service.Schedule does, via Get).
func (r *ChangeScheduleRepo) Upsert(ctx context.Context, s ChangesetSchedule) error {
	_, err := r.db.sqlDB.ExecContext(ctx, `
		INSERT INTO changeset_schedules (
			changeset_id, window_start, window_end, confirm_timeout_sec, missed_window_policy,
			callback_token_hash, status, created_by, created_at, fired_at, cancelled_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL)
		ON CONFLICT (changeset_id) DO UPDATE SET
			window_start         = excluded.window_start,
			window_end           = excluded.window_end,
			confirm_timeout_sec  = excluded.confirm_timeout_sec,
			missed_window_policy = excluded.missed_window_policy,
			callback_token_hash  = excluded.callback_token_hash,
			status               = excluded.status,
			created_by           = excluded.created_by,
			created_at           = excluded.created_at,
			fired_at             = NULL,
			cancelled_at         = NULL`,
		s.ChangesetID, s.WindowStart, s.WindowEnd, s.ConfirmTimeoutSec, s.MissedWindowPolicy,
		s.CallbackTokenHash, s.Status, s.CreatedBy, s.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("store: upserting changeset schedule for %s: %w", s.ChangesetID, err)
	}
	return nil
}

// Get returns the schedule row for changesetID, or ErrNotFound.
func (r *ChangeScheduleRepo) Get(ctx context.Context, changesetID string) (ChangesetSchedule, error) {
	row := r.db.sqlDB.QueryRowContext(ctx, `
		SELECT changeset_id, window_start, window_end, confirm_timeout_sec, missed_window_policy,
			callback_token_hash, status, created_by, created_at, fired_at, cancelled_at
		FROM changeset_schedules WHERE changeset_id = ?`, changesetID,
	)
	s, err := scanChangeSchedule(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ChangesetSchedule{}, ErrNotFound
	}
	return s, err
}

// ListByStatus returns every schedule row currently in status, oldest
// window_start first — the scheduler's own per-tick scan (change.Service.
// TickSchedules) lists ScheduleStatusPending this way.
func (r *ChangeScheduleRepo) ListByStatus(ctx context.Context, status string) ([]ChangesetSchedule, error) {
	rows, err := r.db.sqlDB.QueryContext(ctx, `
		SELECT changeset_id, window_start, window_end, confirm_timeout_sec, missed_window_policy,
			callback_token_hash, status, created_by, created_at, fired_at, cancelled_at
		FROM changeset_schedules WHERE status = ? ORDER BY window_start ASC`, status,
	)
	if err != nil {
		return nil, fmt.Errorf("store: listing changeset schedules with status %s: %w", status, err)
	}
	defer func() { _ = rows.Close() }()

	var out []ChangesetSchedule
	for rows.Next() {
		s, err := scanChangeSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing changeset schedules with status %s: %w", status, err)
	}
	return out, nil
}

// Resolve moves a pending schedule to a terminal status (fired/missed/
// blocked/failed), stamping firedAt. It is a no-op (not an error, zero rows
// affected) if the row is no longer pending — the scheduler's own
// single-pass-per-tick loop is the only writer, but this keeps a
// hypothetical concurrent resolve idempotent rather than clobbering an
// already-resolved row's status.
func (r *ChangeScheduleRepo) Resolve(ctx context.Context, changesetID, status string, firedAt int64) error {
	_, err := r.db.sqlDB.ExecContext(ctx, `
		UPDATE changeset_schedules SET status = ?, fired_at = ?
		WHERE changeset_id = ? AND status = ?`,
		status, firedAt, changesetID, ScheduleStatusPending,
	)
	if err != nil {
		return fmt.Errorf("store: resolving changeset schedule %s: %w", changesetID, err)
	}
	return nil
}

// Cancel moves a pending schedule to cancelled, stamping cancelledAt. It
// returns ErrNotFound if changesetID has no schedule row, and
// ErrIllegalState if that row is no longer pending (already fired/missed/
// resolved/cancelled) — the API layer's "DELETE .../schedule before
// windowStart" contract (docs/api.md).
func (r *ChangeScheduleRepo) Cancel(ctx context.Context, changesetID string, cancelledAt int64) error {
	res, err := r.db.sqlDB.ExecContext(ctx, `
		UPDATE changeset_schedules SET status = ?, cancelled_at = ?
		WHERE changeset_id = ? AND status = ?`,
		ScheduleStatusCancelled, cancelledAt, changesetID, ScheduleStatusPending,
	)
	if err != nil {
		return fmt.Errorf("store: cancelling changeset schedule %s: %w", changesetID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: cancelling changeset schedule %s: %w", changesetID, err)
	}
	if n == 0 {
		if _, getErr := r.Get(ctx, changesetID); getErr != nil {
			return getErr
		}
		return ErrIllegalState
	}
	return nil
}

func scanChangeSchedule(row rowScanner) (ChangesetSchedule, error) {
	var s ChangesetSchedule
	err := row.Scan(
		&s.ChangesetID, &s.WindowStart, &s.WindowEnd, &s.ConfirmTimeoutSec, &s.MissedWindowPolicy,
		&s.CallbackTokenHash, &s.Status, &s.CreatedBy, &s.CreatedAt, &s.FiredAt, &s.CancelledAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ChangesetSchedule{}, err
		}
		return ChangesetSchedule{}, fmt.Errorf("store: scanning changeset schedule: %w", err)
	}
	return s, nil
}
