// SPDX-License-Identifier: Apache-2.0

package capacity

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// DefaultRollupInterval is the daily cadence the rollup job ticks on: it
// computes one bucket per UTC day.
const DefaultRollupInterval = 24 * time.Hour

// BucketSource computes the utilization aggregates for a single UTC day.
// Link aggregates come from that day's metric_samples counter deltas; pool
// aggregates from live IPAM allocation counts. Implementations populate every
// Aggregate field except BucketAt, which RollupJob stamps to the day start.
//
// The concrete implementation lives at the cmd/vnproxd composition root (it
// reads store.MetricSampleRepo and internal/ipam), keeping this package free
// of those imports.
type BucketSource interface {
	DayAggregates(ctx context.Context, dayStart, dayEnd time.Time) ([]Aggregate, error)
}

// Sink persists rolled-up aggregates. *store.CapacityAggregateRepo satisfies
// it via a thin cmd/vnproxd adapter that converts capacity.Aggregate into the
// store row shape — so internal/store stays out of this package's imports.
type Sink interface {
	Upsert(ctx context.Context, a Aggregate) error
}

// RollupJob is T-1606's supervised daily rollup: once per UTC day it asks its
// BucketSource for the just-completed day's utilization aggregates, stamps
// each with that day's start instant, and upserts them into the Sink. Upsert
// is keyed by (ref, kind, bucket_at), so re-running a day (a daemon restart,
// an extra tick) overwrites rather than duplicating — restart-safe and
// idempotent (T-1606 AC5).
type RollupJob struct {
	src  BucketSource
	sink Sink
	now  func() time.Time
	log  *slog.Logger
}

// NewRollupJob constructs a RollupJob. now defaults to time.Now and log to
// slog.Default when nil (the same defaulting every other supervised job in
// this codebase uses).
func NewRollupJob(src BucketSource, sink Sink, now func() time.Time, log *slog.Logger) *RollupJob {
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	return &RollupJob{src: src, sink: sink, now: now, log: log}
}

// RunOnce computes and persists the most recent complete UTC day's bucket
// (yesterday). It is safe to call repeatedly for the same day.
func (j *RollupJob) RunOnce(ctx context.Context) error {
	today := startOfUTCDay(j.now())
	dayStart := today.AddDate(0, 0, -1)
	return j.rollupDay(ctx, dayStart, today)
}

// rollupDay computes [dayStart, dayEnd) and upserts each aggregate stamped at
// dayStart.
func (j *RollupJob) rollupDay(ctx context.Context, dayStart, dayEnd time.Time) error {
	aggs, err := j.src.DayAggregates(ctx, dayStart, dayEnd)
	if err != nil {
		return fmt.Errorf("capacity: computing rollup for %s: %w", dayStart.Format("2006-01-02"), err)
	}
	for _, a := range aggs {
		a.BucketAt = dayStart
		if err := j.sink.Upsert(ctx, a); err != nil {
			return fmt.Errorf("capacity: persisting rollup %s/%s for %s: %w", a.Kind, a.Ref, dayStart.Format("2006-01-02"), err)
		}
	}
	j.log.Info("capacity: daily rollup computed", "day", dayStart.Format("2006-01-02"), "aggregates", len(aggs))
	return nil
}

// Run drives the daily rollup on DefaultRollupInterval until ctx is cancelled,
// matching cmd/vnproxd's runGroup actor signature. It rolls up once at startup
// (restart-safe: idempotent on a day already computed) and then once per tick.
// A failed rollup is logged and the loop continues, the same "log and keep
// going" contract every prune loop in this codebase uses.
func (j *RollupJob) Run(ctx context.Context) error {
	if err := j.RunOnce(ctx); err != nil {
		j.log.Error("capacity: startup rollup failed", "error", err)
	}
	ticker := time.NewTicker(DefaultRollupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := j.RunOnce(ctx); err != nil {
				j.log.Error("capacity: daily rollup failed", "error", err)
			}
		}
	}
}

// startOfUTCDay truncates t to 00:00:00 UTC of the same calendar day.
func startOfUTCDay(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}
