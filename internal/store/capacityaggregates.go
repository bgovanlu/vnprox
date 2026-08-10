package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// CapacityKind names which utilization series a CapacityAggregate summarizes
// (docs/data-model.md §2's capacity_aggregates.kind column). Kept as bare
// string constants (like store's other small enums) so the store layer need
// not import internal/capacity for its own repo type.
const (
	CapacityKindLink     = "link"
	CapacityKindIPAMPool = "ipam_pool"
)

// CapacityAggregate is one row of the capacity_aggregates table
// (docs/data-model.md §2, migration 0026_capacity_samples.sql): a
// downsampled daily rollup of one ref's utilization. BucketAt is the
// start-of-day (UTC) unix timestamp the rollup covers; (Ref, Kind, BucketAt)
// is the natural key, so re-running a day's rollup upserts rather than
// duplicating. AvgUtilization/MaxUtilization are percentages (0-100).
//
// This is the arc's single deliberate retention exception: unlike
// metric_samples (24h) or flow_samples (60m), these rows are kept for
// [capacity] aggregate_retention_days (default 400) — long enough to fit a
// year-over-year growth curve, still explicitly bounded and pruned.
type CapacityAggregate struct {
	Ref            string
	Kind           string
	BucketAt       int64
	AvgUtilization float64
	MaxUtilization float64
	CreatedAt      int64
}

// CapacityAggregateRepo is the capacity_aggregates table repository.
type CapacityAggregateRepo struct {
	db *DB
}

// NewCapacityAggregateRepo constructs a CapacityAggregateRepo.
func NewCapacityAggregateRepo(db *DB) *CapacityAggregateRepo {
	return &CapacityAggregateRepo{db: db}
}

// DefaultCapacityRetentionDays is T-1606's documented capacity_aggregates
// age cap ([capacity] aggregate_retention_days) — ~13 months, enough for a
// year-over-year trend line without the table becoming an unbounded
// warehouse.
const DefaultCapacityRetentionDays = 400

// Upsert writes a daily rollup row, replacing any existing row for the same
// (ref, kind, bucket_at). This makes the rollup job idempotent: re-running a
// day that was already computed overwrites it with the recomputed values
// rather than inserting a duplicate (T-1606 AC5).
func (r *CapacityAggregateRepo) Upsert(ctx context.Context, a CapacityAggregate) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO capacity_aggregates (ref, kind, bucket_at, avg_utilization, max_utilization, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (ref, kind, bucket_at) DO UPDATE SET
			avg_utilization = excluded.avg_utilization,
			max_utilization = excluded.max_utilization,
			created_at = excluded.created_at`,
		a.Ref, a.Kind, a.BucketAt, a.AvgUtilization, a.MaxUtilization, a.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("store: upserting capacity aggregate %s/%s@%d: %w", a.Kind, a.Ref, a.BucketAt, err)
	}
	return nil
}

// Exists reports whether a rollup row already exists for (ref, kind,
// bucketAt). The rollup job uses it to skip recomputing a day it has already
// summarized (a cheaper idempotency guard than recomputing and upserting).
func (r *CapacityAggregateRepo) Exists(ctx context.Context, ref, kind string, bucketAt int64) (bool, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT count(*) FROM capacity_aggregates WHERE ref = ? AND kind = ? AND bucket_at = ?`,
		ref, kind, bucketAt,
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("store: checking capacity aggregate %s/%s@%d: %w", kind, ref, bucketAt, err)
	}
	return n > 0, nil
}

// ListAll returns every aggregate row, ordered by (ref, kind, bucket_at) —
// the shape the forecast producer folds by ref before fitting a trend line.
func (r *CapacityAggregateRepo) ListAll(ctx context.Context) ([]CapacityAggregate, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT ref, kind, bucket_at, avg_utilization, max_utilization, created_at
		FROM capacity_aggregates ORDER BY ref ASC, kind ASC, bucket_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: listing capacity aggregates: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanCapacityAggregates(rows)
}

// ListByRefSince returns aggregates for one (ref, kind) with bucket_at >=
// since, ordered by bucket_at ascending. GET /capacity/export passes the
// retention-window cutoff as since, so an export can never surface a row
// older than aggregate_retention_days even in the window between prune ticks
// (T-1606 AC4).
func (r *CapacityAggregateRepo) ListByRefSince(ctx context.Context, ref, kind string, since int64) ([]CapacityAggregate, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT ref, kind, bucket_at, avg_utilization, max_utilization, created_at
		FROM capacity_aggregates WHERE ref = ? AND kind = ? AND bucket_at >= ?
		ORDER BY bucket_at ASC`, ref, kind, since)
	if err != nil {
		return nil, fmt.Errorf("store: listing capacity aggregates for %s/%s: %w", kind, ref, err)
	}
	defer func() { _ = rows.Close() }()
	return scanCapacityAggregates(rows)
}

// Prune deletes aggregates with bucket_at < cutoff, returning the number of
// rows removed. Callers/tests compute cutoff; PruneRetention wraps this with
// the documented age cap and wall-clock time.
func (r *CapacityAggregateRepo) Prune(ctx context.Context, cutoff int64) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM capacity_aggregates WHERE bucket_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("store: pruning capacity aggregates older than %d: %w", cutoff, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: counting pruned capacity aggregates: %w", err)
	}
	return n, nil
}

// PruneRetention deletes capacity_aggregates rows whose bucket day is older
// than keepDays, measured from now (T-1606 AC5). keepDays <= 0 falls back to
// DefaultCapacityRetentionDays.
func (r *CapacityAggregateRepo) PruneRetention(ctx context.Context, now time.Time, keepDays int) (int64, error) {
	if keepDays <= 0 {
		keepDays = DefaultCapacityRetentionDays
	}
	cutoff := now.AddDate(0, 0, -keepDays).Unix()
	return r.Prune(ctx, cutoff)
}

// RunPruneLoop runs PruneRetention every interval until ctx is cancelled,
// logging failures via logFn (nil discards them) rather than stopping the
// loop — the same "log and keep going" prune-loop contract
// MetricSampleRepo.RunPruneLoop establishes, suitable for cmd/vnproxd's
// runGroup.
func (r *CapacityAggregateRepo) RunPruneLoop(ctx context.Context, interval time.Duration, keepDays int, logFn func(err error)) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			if _, err := r.PruneRetention(ctx, now, keepDays); err != nil && logFn != nil &&
				!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				logFn(fmt.Errorf("store: pruning capacity aggregates: %w", err))
			}
		}
	}
}

func scanCapacityAggregates(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]CapacityAggregate, error) {
	var out []CapacityAggregate
	for rows.Next() {
		var a CapacityAggregate
		if err := rows.Scan(&a.Ref, &a.Kind, &a.BucketAt, &a.AvgUtilization, &a.MaxUtilization, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: scanning capacity aggregate: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating capacity aggregates: %w", err)
	}
	return out, nil
}
