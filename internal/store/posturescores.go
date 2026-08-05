package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PostureScore is one row of the posture_scores table (docs/data-model.md §2,
// migration 0027_posture_scores.sql): one scheduled posture computation — the
// overall 0..100 score, the qualified flag (partial/uncertain, never a clean
// bill of health), and the serialized named-factor breakdown. FactorsJSON is an
// opaque payload to the store layer (the serialized []posture.Factor); the
// store neither parses nor validates it, keeping internal/store free of any
// internal/posture import.
type PostureScore struct {
	FactorsJSON string
	ID          int64
	ComputedAt  int64
	Overall     int
	Qualified   bool
}

// PostureScoreRepo is the posture_scores table repository.
type PostureScoreRepo struct {
	db *DB
}

// NewPostureScoreRepo constructs a PostureScoreRepo.
func NewPostureScoreRepo(db *DB) *PostureScoreRepo { return &PostureScoreRepo{db: db} }

// Posture retention defaults (T-1607): keep the most recent
// DefaultPostureKeepCount computations OR anything within
// DefaultPostureRetentionDays by age, whichever is smaller — the bound the
// scheduled prune loop enforces. Both are applied; a row must satisfy both to
// survive.
const (
	// DefaultPostureKeepCount caps the table by row count — the last 90
	// scheduled computations (roughly a quarter at daily cadence).
	DefaultPostureKeepCount = 90
	// DefaultPostureRetentionDays caps the table by age — 400 days (~13
	// months), the same age ceiling the arc uses elsewhere.
	DefaultPostureRetentionDays = 400
)

// Insert records one posture computation, returning the assigned id.
func (r *PostureScoreRepo) Insert(ctx context.Context, s PostureScore) (int64, error) {
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO posture_scores (computed_at, overall, qualified, factors_json)
		VALUES (?, ?, ?, ?)`,
		s.ComputedAt, s.Overall, boolToInt(s.Qualified), s.FactorsJSON,
	)
	if err != nil {
		return 0, fmt.Errorf("store: inserting posture score: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: reading inserted posture score id: %w", err)
	}
	return id, nil
}

// Latest returns the most recent posture computation, or ErrNotFound when the
// table is empty (no computation has run yet). GET /posture serves it.
func (r *PostureScoreRepo) Latest(ctx context.Context) (PostureScore, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, computed_at, overall, qualified, factors_json
		FROM posture_scores ORDER BY computed_at DESC, id DESC LIMIT 1`)
	s, err := scanPostureScore(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PostureScore{}, ErrNotFound
	}
	if err != nil {
		return PostureScore{}, fmt.Errorf("store: reading latest posture score: %w", err)
	}
	return s, nil
}

// History returns up to limit most-recent computations, newest first (the
// bounded trend GET /posture/history serves). limit <= 0 falls back to
// DefaultPostureKeepCount so a caller can never accidentally unbound the query.
func (r *PostureScoreRepo) History(ctx context.Context, limit int) ([]PostureScore, error) {
	if limit <= 0 {
		limit = DefaultPostureKeepCount
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, computed_at, overall, qualified, factors_json
		FROM posture_scores ORDER BY computed_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: listing posture scores: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []PostureScore
	for rows.Next() {
		s, scanErr := scanPostureScore(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("store: scanning posture score: %w", scanErr)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating posture scores: %w", err)
	}
	return out, nil
}

// Count returns the total number of stored computations.
func (r *PostureScoreRepo) Count(ctx context.Context) (int64, error) {
	var n int64
	if err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM posture_scores`).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: counting posture scores: %w", err)
	}
	return n, nil
}

// DeleteInRange removes every row with computed_at in [from, to), returning the
// number removed. The scheduled job calls it to make a same-day recomputation
// idempotent: it clears any prior row for today's UTC day before inserting the
// fresh one, so re-running the day's computation never duplicates a row
// (T-1607 AC5).
func (r *PostureScoreRepo) DeleteInRange(ctx context.Context, from, to int64) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM posture_scores WHERE computed_at >= ? AND computed_at < ?`, from, to)
	if err != nil {
		return 0, fmt.Errorf("store: deleting posture scores in [%d,%d): %w", from, to, err)
	}
	return rowsAffected(res, "posture scores")
}

// PruneRetention enforces both documented bounds (T-1607): it deletes rows
// older than keepDays by age AND rows beyond the newest keepCount by count —
// whichever is smaller wins, since a row must satisfy both to survive. Returns
// the total number removed. Non-positive keepCount/keepDays fall back to the
// package defaults.
func (r *PostureScoreRepo) PruneRetention(ctx context.Context, now time.Time, keepCount, keepDays int) (int64, error) {
	if keepCount <= 0 {
		keepCount = DefaultPostureKeepCount
	}
	if keepDays <= 0 {
		keepDays = DefaultPostureRetentionDays
	}

	var removed int64
	cutoff := now.AddDate(0, 0, -keepDays).Unix()
	ageRes, err := r.db.ExecContext(ctx, `DELETE FROM posture_scores WHERE computed_at < ?`, cutoff)
	if err != nil {
		return removed, fmt.Errorf("store: pruning posture scores older than %d: %w", cutoff, err)
	}
	n, err := rowsAffected(ageRes, "posture scores")
	if err != nil {
		return removed, err
	}
	removed += n

	// Count bound: keep only the newest keepCount by computed_at.
	countRes, err := r.db.ExecContext(ctx, `
		DELETE FROM posture_scores WHERE id NOT IN (
			SELECT id FROM posture_scores ORDER BY computed_at DESC, id DESC LIMIT ?
		)`, keepCount)
	if err != nil {
		return removed, fmt.Errorf("store: pruning posture scores beyond newest %d: %w", keepCount, err)
	}
	n, err = rowsAffected(countRes, "posture scores")
	if err != nil {
		return removed, err
	}
	removed += n
	return removed, nil
}

// RunPruneLoop runs PruneRetention every interval until ctx is cancelled,
// logging failures via logFn (nil discards them) rather than stopping the loop
// — the same "log and keep going" prune-loop contract MetricSampleRepo /
// FindingEventRepo establish, suitable for cmd/vnproxd's runGroup. Shutdown-race
// context errors are expected teardown and not logged.
func (r *PostureScoreRepo) RunPruneLoop(ctx context.Context, interval time.Duration, keepCount, keepDays int, logFn func(err error)) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			if _, err := r.PruneRetention(ctx, now, keepCount, keepDays); err != nil && logFn != nil &&
				!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				logFn(fmt.Errorf("store: pruning posture scores: %w", err))
			}
		}
	}
}

func scanPostureScore(row rowScanner) (PostureScore, error) {
	var s PostureScore
	var qualified int
	if err := row.Scan(&s.ID, &s.ComputedAt, &s.Overall, &qualified, &s.FactorsJSON); err != nil {
		return PostureScore{}, err
	}
	s.Qualified = qualified != 0
	return s, nil
}
