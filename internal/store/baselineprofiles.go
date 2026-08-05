package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// BaselineProfile is one row of the baseline_profiles table (T-1601,
// docs/data-model.md §2, migration 0025_flow_baselines.sql): a single
// learned traffic baseline for one inventory Ref, serialized. ProfileJSON is
// the full internal/baseline.Profile value; this package deliberately does
// not import internal/baseline (the same "internal/store never imports the
// packages that use it" layering flow_samples/blueprints already follow) —
// callers encode/decode the JSON themselves.
//
// This is app-owned SUMMARY data, not a shadow copy of raw flow rows: a
// learned shape outlives the short-lived flow_samples it was learned from
// (see the migration's doc comment), so it is retained on its own, far
// longer, [baseline] profile_retention_days window (Prune).
type BaselineProfile struct {
	Ref         string
	ProfileJSON string
	WindowStart int64
	WindowEnd   int64
	UpdatedAt   int64
}

// BaselineProfileRepo is the baseline_profiles table repository.
type BaselineProfileRepo struct {
	db *DB
}

// NewBaselineProfileRepo constructs a BaselineProfileRepo.
func NewBaselineProfileRepo(db *DB) *BaselineProfileRepo { return &BaselineProfileRepo{db: db} }

// Put upserts a profile by ref (insert if absent, overwrite if present) — a
// re-learn of the same Ref replaces its single current baseline rather than
// accumulating history, mirroring BlueprintRepo.Put/LayoutRepo.Put's
// idempotent-by-key convention (baseline_profiles is not a time-series ring).
func (r *BaselineProfileRepo) Put(ctx context.Context, p BaselineProfile) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO baseline_profiles (ref, profile_json, window_start, window_end, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (ref) DO UPDATE SET
			profile_json = excluded.profile_json,
			window_start = excluded.window_start,
			window_end = excluded.window_end,
			updated_at = excluded.updated_at`,
		p.Ref, p.ProfileJSON, p.WindowStart, p.WindowEnd, p.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("store: upserting baseline profile %s: %w", p.Ref, err)
	}
	return nil
}

// Get returns the current baseline for ref, or ErrNotFound.
func (r *BaselineProfileRepo) Get(ctx context.Context, ref string) (BaselineProfile, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT ref, profile_json, window_start, window_end, updated_at
		FROM baseline_profiles WHERE ref = ?`, ref,
	)
	p, err := scanBaselineProfile(row)
	if errors.Is(err, sql.ErrNoRows) {
		return BaselineProfile{}, ErrNotFound
	}
	return p, err
}

// List returns every stored baseline, ordered by ref for a stable listing.
func (r *BaselineProfileRepo) List(ctx context.Context) ([]BaselineProfile, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT ref, profile_json, window_start, window_end, updated_at
		FROM baseline_profiles ORDER BY ref ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("store: listing baseline profiles: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []BaselineProfile
	for rows.Next() {
		p, err := scanBaselineProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing baseline profiles: %w", err)
	}
	return out, nil
}

// Count returns the total row count (test/observability helper).
func (r *BaselineProfileRepo) Count(ctx context.Context) (int64, error) {
	var n int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM baseline_profiles`).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: counting baseline profiles: %w", err)
	}
	return n, nil
}

// Prune deletes profiles whose updated_at is older than cutoff, returning the
// number removed. Callers/tests compute cutoff themselves; PruneRetention
// wraps this with the documented [baseline] profile_retention_days window.
func (r *BaselineProfileRepo) Prune(ctx context.Context, cutoff int64) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM baseline_profiles WHERE updated_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("store: pruning baseline profiles older than %d: %w", cutoff, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: counting pruned baseline profiles: %w", err)
	}
	return n, nil
}

// DefaultBaselineProfileRetentionDays is the documented [baseline]
// profile_retention_days default (T-1601's card): a learned baseline is kept
// 90 days. This is deliberately many orders of magnitude longer than
// flow_samples' own retention (minutes/hours), so a baseline is never lost
// before flow_samples' own window has already closed on the raw flows it was
// learned from — the "a learned shape outlives the raw flows" guarantee the
// migration's doc comment names.
const DefaultBaselineProfileRetentionDays = 90

// PruneRetention deletes baseline_profiles rows older than keepDays measured
// from now (falling back to DefaultBaselineProfileRetentionDays when keepDays
// is non-positive).
func (r *BaselineProfileRepo) PruneRetention(ctx context.Context, now time.Time, keepDays int) (int64, error) {
	if keepDays <= 0 {
		keepDays = DefaultBaselineProfileRetentionDays
	}
	cutoff := now.AddDate(0, 0, -keepDays).Unix()
	return r.Prune(ctx, cutoff)
}

// RunPruneLoop runs PruneRetention every interval until ctx is cancelled,
// logging failures via logFn (which may be nil to discard them) rather than
// stopping the loop — the identical tick-based prune-loop contract
// MetricSampleRepo.RunPruneLoop establishes (func(ctx context.Context) error,
// suitable for cmd/vnproxd's runGroup). The daemon wires it up.
func (r *BaselineProfileRepo) RunPruneLoop(ctx context.Context, interval time.Duration, keepDays int, logFn func(err error)) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			if _, err := r.PruneRetention(ctx, now, keepDays); err != nil && logFn != nil {
				logFn(fmt.Errorf("store: pruning baseline profiles: %w", err))
			}
		}
	}
}

func scanBaselineProfile(row rowScanner) (BaselineProfile, error) {
	var p BaselineProfile
	if err := row.Scan(&p.Ref, &p.ProfileJSON, &p.WindowStart, &p.WindowEnd, &p.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return BaselineProfile{}, err
		}
		return BaselineProfile{}, fmt.Errorf("store: scanning baseline profile: %w", err)
	}
	return p, nil
}
