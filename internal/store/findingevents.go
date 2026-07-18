package store

import (
	"context"
	"fmt"
	"time"
)

// FindingEvent is one row of the finding_events table (docs/data-model.md
// §2, T-1007): a single finding transition (new/escalated/resolved),
// captured at the moment findings.Notifier's EXISTING transition detection
// (internal/findings/notify.go's evaluateNotifications) fired it — see
// internal/findings/findingevents.go's FindingEventsNotifier, the sole
// writer. There is no Update: like metric_samples/flow_samples, this is an
// immutable time-series log.
type FindingEvent struct {
	FindingID  string
	Transition string
	ID         int64
	At         int64
}

// FindingEventRepo is the finding_events table repository.
type FindingEventRepo struct {
	db *DB
}

// NewFindingEventRepo constructs a FindingEventRepo.
func NewFindingEventRepo(db *DB) *FindingEventRepo { return &FindingEventRepo{db: db} }

// Insert records one finding transition. finding_events has no natural
// dedup key (the same finding id legitimately transitions more than once
// within the retention window), so every call always inserts a new row.
func (r *FindingEventRepo) Insert(ctx context.Context, e FindingEvent) error {
	_, err := r.db.sqlDB.ExecContext(ctx, `
		INSERT INTO finding_events (finding_id, at, transition) VALUES (?, ?, ?)`,
		e.FindingID, e.At, e.Transition,
	)
	if err != nil {
		return fmt.Errorf("store: inserting finding event for %s: %w", e.FindingID, err)
	}
	return nil
}

// ListByTimeRange returns every finding_events row with at in [from, to]
// (0 on either side means "unbounded on that side"), ordered by at
// ascending — GET /history/events' finding-transition half
// (internal/api/history.go, T-1007) and, transitively,
// HistoryTimeline.tsx's timeline-marker feed.
func (r *FindingEventRepo) ListByTimeRange(ctx context.Context, from, to int64) ([]FindingEvent, error) {
	query := `SELECT id, finding_id, at, transition FROM finding_events WHERE 1=1`
	var args []any
	if from > 0 {
		query += ` AND at >= ?`
		args = append(args, from)
	}
	if to > 0 {
		query += ` AND at <= ?`
		args = append(args, to)
	}
	query += ` ORDER BY at ASC, id ASC`

	rows, err := r.db.sqlDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: listing finding events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []FindingEvent
	for rows.Next() {
		var e FindingEvent
		if scanErr := rows.Scan(&e.ID, &e.FindingID, &e.At, &e.Transition); scanErr != nil {
			return nil, fmt.Errorf("store: scanning finding event: %w", scanErr)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing finding events: %w", err)
	}
	return out, nil
}

// PruneOlderThan deletes rows with at < cutoff, returning the number
// removed.
func (r *FindingEventRepo) PruneOlderThan(ctx context.Context, cutoff int64) (int64, error) {
	res, err := r.db.sqlDB.ExecContext(ctx, `DELETE FROM finding_events WHERE at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("store: pruning finding events older than %d: %w", cutoff, err)
	}
	return rowsAffected(res, "finding events")
}

// PruneRetention deletes finding_events rows older than MetricRetention
// (T-1007's card: "bounded to the same window as metric_samples"), measured
// from now — mirrors MetricSampleRepo.PruneRetention exactly, over this
// table, reusing the same MetricRetention constant rather than a second
// one so the two windows can never silently drift apart.
func (r *FindingEventRepo) PruneRetention(ctx context.Context, now time.Time) (int64, error) {
	return r.PruneOlderThan(ctx, now.Add(-MetricRetention).Unix())
}

// RunPruneLoop runs PruneRetention every interval until ctx is cancelled,
// logging failures via logFn (nil discards them) rather than stopping the
// loop — mirrors MetricSampleRepo.RunPruneLoop's contract exactly
// (func(ctx context.Context) error, suitable for cmd/vnproxd's runGroup).
// cmd/vnproxd wires this alongside (not instead of) metric_samples' own
// prune loop, per this task's card ("pruned alongside" metric_samples).
func (r *FindingEventRepo) RunPruneLoop(ctx context.Context, interval time.Duration, logFn func(err error)) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			if _, err := r.PruneRetention(ctx, now); err != nil && logFn != nil {
				logFn(fmt.Errorf("store: pruning finding events: %w", err))
			}
		}
	}
}
