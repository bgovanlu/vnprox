// SPDX-License-Identifier: Apache-2.0

// digests.go implements T-2807's scheduled-digest storage (migration
// 0043_digest_schedules.sql): the schedule the digest runner re-reads on every
// tick, and the previous digest's summary the next digest computes its deltas
// against.
//
// App-owned data per CLAUDE.md's storage rule — a schedule and a record of
// what was sent. Nothing here is a copy of PVE state, and nothing here is a
// copy of the rendered digest: the document is regenerated from the live
// surfaces every time.

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// DefaultDigestScheduleID is the single schedule the daemon runs. The table is
// keyed rather than single-row so a later per-tenant schedule needs an INSERT
// and not a migration.
const DefaultDigestScheduleID = "default"

// DigestPostureNotScored is DigestRun.PostureOverall's "there was no posture
// score in this digest" sentinel — distinct from a genuine 0, which means
// "scored, and bad". Mirrors internal/posture.NotEvaluatedScore's own
// discipline; a reader must branch on it before showing a delta.
const DigestPostureNotScored = -1

// DefaultDigestRunKeep bounds digest_runs. One row per digest — weekly, by
// default — so 52 is about a year of history and the table can never become a
// growth surface. RecordRun trims to it on every insert, which is why this
// table needs no prune actor.
const DefaultDigestRunKeep = 52

// DigestSchedule is one row of digest_schedules: when a digest goes out, and
// to which alert targets.
//
// RuleIDs is nil/empty to mean "every enabled alert rule that matches",
// which is T-2407's ordinary fan-out — the same optional-filter contract
// every other filter in this codebase follows. It is a filter over
// alert_rules, never a second address book.
type DigestSchedule struct {
	ID        string
	UpdatedBy string
	RuleIDs   []string
	EverySec  int64
	UpdatedAt int64
	Enabled   bool
}

// DigestRun is one row of digest_runs: the summary of a digest that was
// generated, and the baseline the next one measures against.
type DigestRun struct {
	ID         string
	ScheduleID string
	Status     string
	Detail     string
	// PeriodStart is the previous run's PeriodEnd, or 0 on a first-ever
	// digest — which is also how "this digest has no baseline" is recognised.
	PeriodStart int64
	PeriodEnd   int64
	GeneratedAt int64
	// PostureOverall is the 0..100 score this digest carried, or
	// DigestPostureNotScored.
	PostureOverall int64
	OpenedCount    int64
	ClosedCount    int64
	DriftCount     int64
	CapacityCount  int64
	Quiet          bool
}

// Digest run status vocabulary.
const (
	// DigestStatusDelivered means the notifier accepted the digest — which,
	// under quiet hours, includes "held for later delivery": deferral is not
	// a failure, exactly as internal/findings' own delivery log treats it.
	DigestStatusDelivered = "delivered"
	// DigestStatusFailed means delivery was attempted and every attempt was
	// exhausted. The digest was still generated and is still the next one's
	// baseline: a target that was down does not rewrite what was true.
	DigestStatusFailed = "failed"
	// DigestStatusSkipped means no digest was sent for this period.
	DigestStatusSkipped = "skipped"
)

// DigestRepo is the digest_schedules / digest_runs repository.
type DigestRepo struct {
	db *DB
}

// NewDigestRepo constructs a DigestRepo.
func NewDigestRepo(db *DB) *DigestRepo { return &DigestRepo{db: db} }

const digestScheduleCols = `id, enabled, every_sec, rule_ids_json, updated_at, updated_by`

// Schedule returns one schedule by id, or ErrNotFound. A missing row is the
// ordinary state of a daemon nobody has configured a digest on, so callers
// treat ErrNotFound as "no digest configured" rather than as a failure.
func (r *DigestRepo) Schedule(ctx context.Context, id string) (DigestSchedule, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+digestScheduleCols+` FROM digest_schedules WHERE id = ?`, id)

	var s DigestSchedule
	var ruleIDs sql.NullString
	err := row.Scan(&s.ID, &s.Enabled, &s.EverySec, &ruleIDs, &s.UpdatedAt, &s.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return DigestSchedule{}, ErrNotFound
	}
	if err != nil {
		return DigestSchedule{}, fmt.Errorf("store: reading digest schedule %s: %w", id, err)
	}
	if ruleIDs.Valid && ruleIDs.String != "" {
		if unmarshalErr := json.Unmarshal([]byte(ruleIDs.String), &s.RuleIDs); unmarshalErr != nil {
			return DigestSchedule{}, fmt.Errorf("store: decoding digest schedule %s's recipients: %w", id, unmarshalErr)
		}
	}
	return s, nil
}

// UpsertSchedule writes s, replacing any existing row with the same id.
//
// Replace rather than partial update on purpose: a schedule is small and is
// always set as a whole, and a field-by-field update is how a recipient list
// silently survives a change that meant to clear it.
func (r *DigestRepo) UpsertSchedule(ctx context.Context, s DigestSchedule) error {
	ruleIDs, err := marshalFilter(s.RuleIDs)
	if err != nil {
		return fmt.Errorf("store: encoding digest schedule %s's recipients: %w", s.ID, err)
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO digest_schedules (`+digestScheduleCols+`)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			enabled = excluded.enabled,
			every_sec = excluded.every_sec,
			rule_ids_json = excluded.rule_ids_json,
			updated_at = excluded.updated_at,
			updated_by = excluded.updated_by`,
		s.ID, s.Enabled, s.EverySec, ruleIDs, s.UpdatedAt, s.UpdatedBy,
	)
	if err != nil {
		return fmt.Errorf("store: writing digest schedule %s: %w", s.ID, err)
	}
	return nil
}

const digestRunCols = `id, schedule_id, period_start, period_end, generated_at, ` +
	`posture_overall, opened_count, closed_count, drift_count, capacity_count, quiet, status, detail`

// LatestRun returns the newest run for scheduleID, or ErrNotFound when the
// schedule has never produced a digest — the "no baseline" case T-2807 AC2
// requires a first-ever digest to state rather than paper over.
func (r *DigestRepo) LatestRun(ctx context.Context, scheduleID string) (DigestRun, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT `+digestRunCols+` FROM digest_runs
		WHERE schedule_id = ?
		ORDER BY period_end DESC, id DESC
		LIMIT 1`, scheduleID)

	run, err := scanDigestRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return DigestRun{}, ErrNotFound
	}
	if err != nil {
		return DigestRun{}, fmt.Errorf("store: reading latest digest run for %s: %w", scheduleID, err)
	}
	return run, nil
}

// scanDigestRun reads one row in digestRunCols order. It takes the package's
// shared rowScanner (sessions.go) so the single-row and multi-row queries
// cannot drift apart on column order.
func scanDigestRun(row rowScanner) (DigestRun, error) {
	var run DigestRun
	err := row.Scan(&run.ID, &run.ScheduleID, &run.PeriodStart, &run.PeriodEnd, &run.GeneratedAt,
		&run.PostureOverall, &run.OpenedCount, &run.ClosedCount, &run.DriftCount, &run.CapacityCount,
		&run.Quiet, &run.Status, &run.Detail)
	return run, err
}

// RecordRun inserts one run and trims the schedule's history to
// DefaultDigestRunKeep rows.
//
// The trim happens here, on the write, rather than in a prune actor: this
// table gains one row per digest (weekly by default), so a background prune
// loop would spend a daily wakeup on a table that grows by four rows a month.
// Bounding it at the only place that can grow it is both cheaper and
// impossible to forget to wire.
func (r *DigestRepo) RecordRun(ctx context.Context, run DigestRun) error {
	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO digest_runs (`+digestRunCols+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.ScheduleID, run.PeriodStart, run.PeriodEnd, run.GeneratedAt,
		run.PostureOverall, run.OpenedCount, run.ClosedCount, run.DriftCount, run.CapacityCount,
		run.Quiet, run.Status, run.Detail,
	); err != nil {
		return fmt.Errorf("store: recording digest run %s: %w", run.ID, err)
	}

	if _, err := r.db.ExecContext(ctx, `
		DELETE FROM digest_runs
		WHERE schedule_id = ? AND id NOT IN (
			SELECT id FROM digest_runs WHERE schedule_id = ?
			ORDER BY period_end DESC, id DESC LIMIT ?
		)`, run.ScheduleID, run.ScheduleID, DefaultDigestRunKeep,
	); err != nil {
		return fmt.Errorf("store: trimming digest run history for %s: %w", run.ScheduleID, err)
	}
	return nil
}

// ListRuns returns scheduleID's runs, newest first, bounded by limit
// (limit <= 0 means DefaultDigestRunKeep).
func (r *DigestRepo) ListRuns(ctx context.Context, scheduleID string, limit int) ([]DigestRun, error) {
	if limit <= 0 {
		limit = DefaultDigestRunKeep
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+digestRunCols+` FROM digest_runs
		WHERE schedule_id = ?
		ORDER BY period_end DESC, id DESC
		LIMIT ?`, scheduleID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: listing digest runs for %s: %w", scheduleID, err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]DigestRun, 0, limit)
	for rows.Next() {
		run, scanErr := scanDigestRun(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("store: scanning digest run for %s: %w", scheduleID, scanErr)
		}
		out = append(out, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating digest runs for %s: %w", scheduleID, err)
	}
	return out, nil
}
