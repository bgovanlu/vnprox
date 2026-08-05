package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// MetricSample is one row of the metric_samples table (docs/data-model.md
// §2): a per-interface (or other "ref") counter snapshot at a point in
// time. The table is pruned to a 24h rolling window (see Prune); there is no
// Update, since samples are immutable time-series points.
type MetricSample struct {
	Ref     string
	At      int64
	RxBytes sql.NullInt64
	TxBytes sql.NullInt64
	RxPkts  sql.NullInt64
	TxPkts  sql.NullInt64
	RxErrs  sql.NullInt64
	TxErrs  sql.NullInt64
	RxDrop  sql.NullInt64
	TxDrop  sql.NullInt64
}

// MetricSampleRepo is the metric_samples table repository.
type MetricSampleRepo struct {
	db *DB
}

// NewMetricSampleRepo constructs a MetricSampleRepo.
func NewMetricSampleRepo(db *DB) *MetricSampleRepo { return &MetricSampleRepo{db: db} }

// Insert records a sample. (ref, at) is the primary key; inserting a
// duplicate replaces the existing row rather than erroring, since collectors
// may occasionally re-sample the same tick after a retry.
func (r *MetricSampleRepo) Insert(ctx context.Context, s MetricSample) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO metric_samples (ref, at, rx_bytes, tx_bytes, rx_pkts, tx_pkts, rx_errs, tx_errs, rx_drop, tx_drop)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (ref, at) DO UPDATE SET
			rx_bytes = excluded.rx_bytes, tx_bytes = excluded.tx_bytes,
			rx_pkts = excluded.rx_pkts, tx_pkts = excluded.tx_pkts,
			rx_errs = excluded.rx_errs, tx_errs = excluded.tx_errs,
			rx_drop = excluded.rx_drop, tx_drop = excluded.tx_drop`,
		s.Ref, s.At, s.RxBytes, s.TxBytes, s.RxPkts, s.TxPkts, s.RxErrs, s.TxErrs, s.RxDrop, s.TxDrop,
	)
	if err != nil {
		return fmt.Errorf("store: inserting metric sample %s@%d: %w", s.Ref, s.At, err)
	}
	return nil
}

// Get returns the sample for (ref, at), or ErrNotFound.
func (r *MetricSampleRepo) Get(ctx context.Context, ref string, at int64) (MetricSample, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT ref, at, rx_bytes, tx_bytes, rx_pkts, tx_pkts, rx_errs, tx_errs, rx_drop, tx_drop
		FROM metric_samples WHERE ref = ? AND at = ?`, ref, at,
	)
	s, err := scanMetricSample(row)
	if errors.Is(err, sql.ErrNoRows) {
		return MetricSample{}, ErrNotFound
	}
	return s, err
}

// List returns samples for ref within [since, until] (inclusive), ordered by
// at ascending.
func (r *MetricSampleRepo) List(ctx context.Context, ref string, since, until int64) ([]MetricSample, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT ref, at, rx_bytes, tx_bytes, rx_pkts, tx_pkts, rx_errs, tx_errs, rx_drop, tx_drop
		FROM metric_samples WHERE ref = ? AND at BETWEEN ? AND ? ORDER BY at ASC`,
		ref, since, until,
	)
	if err != nil {
		return nil, fmt.Errorf("store: listing metric samples for %s: %w", ref, err)
	}
	defer func() { _ = rows.Close() }()

	var out []MetricSample
	for rows.Next() {
		s, err := scanMetricSample(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing metric samples for %s: %w", ref, err)
	}
	return out, nil
}

// Prune deletes samples older than the given cutoff, returning the number
// of rows removed. Callers/tests should compute cutoff themselves;
// PruneRetention wraps this with the documented 24h retention window and
// wall-clock time.
func (r *MetricSampleRepo) Prune(ctx context.Context, cutoff int64) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM metric_samples WHERE at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("store: pruning metric samples older than %d: %w", cutoff, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: counting pruned metric samples: %w", err)
	}
	return n, nil
}

// MetricRetention is the documented metric_samples retention window
// (docs/data-model.md §2: "pruned to 24h; longer horizons are out of scope
// for v1").
const MetricRetention = 24 * time.Hour

// PruneRetention deletes metric_samples rows older than MetricRetention,
// measured from now.
func (r *MetricSampleRepo) PruneRetention(ctx context.Context, now time.Time) (int64, error) {
	return r.Prune(ctx, now.Add(-MetricRetention).Unix())
}

// RunPruneLoop runs PruneRetention every interval until ctx is cancelled,
// logging failures via logFn (which may be nil to discard them) rather than
// stopping the loop, since a single failed prune shouldn't take down the
// process. It returns nil once ctx is cancelled, matching cmd/vnproxd's
// runGroup actor contract (func(ctx context.Context) error) documented in
// cmd/vnproxd/rungroup.go, though it is intentionally not registered there
// by this package — the daemon wires it up.
func (r *MetricSampleRepo) RunPruneLoop(ctx context.Context, interval time.Duration, logFn func(err error)) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			if _, err := r.PruneRetention(ctx, now); err != nil && logFn != nil {
				logFn(fmt.Errorf("store: pruning metric samples: %w", err))
			}
		}
	}
}

func scanMetricSample(row rowScanner) (MetricSample, error) {
	var s MetricSample
	err := row.Scan(&s.Ref, &s.At, &s.RxBytes, &s.TxBytes, &s.RxPkts, &s.TxPkts, &s.RxErrs, &s.TxErrs, &s.RxDrop, &s.TxDrop)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MetricSample{}, err
		}
		return MetricSample{}, fmt.Errorf("store: scanning metric sample: %w", err)
	}
	return s, nil
}
