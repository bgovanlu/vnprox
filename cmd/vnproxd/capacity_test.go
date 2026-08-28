// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/capacity"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

func capacityTestLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func nullI(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }

// TestCapacityRollup_SurvivesMetricPrune is T-1606 AC1: after metric_samples'
// raw counters for a day are pruned, the capacity_aggregates row rolled up
// from them still exists and matches the pre-prune computed values.
func TestCapacityRollup_SurvivesMetricPrune(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "vnprox.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// A 1000 Mbps physical NIC in the live graph.
	graph := inventory.NewGraph()
	nic := &inventory.PhysNic{
		Ref:       inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno1"},
		Name:      "eno1",
		SpeedMbps: 1000,
	}
	graph.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: "pve1", Kinds: []inventory.Kind{inventory.KindPhysNic}}, []inventory.Entity{nic})
	ref := nic.String()

	// Seed a day's worth of counter samples: +12.5 MB rx/s => 10% of the link
	// on each 1s interval.
	metricSamples := store.NewMetricSampleRepo(db)
	targetDay := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	var rx int64
	for i := 0; i < 3; i++ {
		at := targetDay.Add(time.Duration(i) * time.Second)
		if insErr := metricSamples.Insert(ctx, store.MetricSample{Ref: ref, At: at.Unix(), RxBytes: nullI(rx), TxBytes: nullI(0)}); insErr != nil {
			t.Fatalf("seeding metric sample: %v", insErr)
		}
		rx += 12_500_000
	}

	repo := store.NewCapacityAggregateRepo(db)
	src := capacityBucketSource{metrics: metricSamples, graph: graph, ipam: nil, logger: capacityTestLogger()}
	sink := capacitySink{repo: repo, now: func() time.Time { return time.Unix(1_700_000_000, 0) }}
	now := func() time.Time { return time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC) } // "today" -> rolls up yesterday (the 19th)
	job := capacity.NewRollupJob(src, sink, now, capacityTestLogger())

	if runErr := job.RunOnce(ctx); runErr != nil {
		t.Fatalf("RunOnce: %v", runErr)
	}

	before, err := repo.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll before prune: %v", err)
	}
	if len(before) != 1 || before[0].Ref != ref || before[0].Kind != store.CapacityKindLink {
		t.Fatalf("aggregates before prune = %+v, want one link row for %s", before, ref)
	}
	if before[0].MaxUtilization < 9.9 || before[0].MaxUtilization > 10.1 {
		t.Fatalf("max utilization = %.3f, want ~10", before[0].MaxUtilization)
	}
	pre := before[0]

	// Prune every raw metric sample (the 24h ring closing on this day's data).
	if _, pruneErr := metricSamples.Prune(ctx, targetDay.Add(24*time.Hour).Unix()); pruneErr != nil {
		t.Fatalf("pruning metric samples: %v", pruneErr)
	}
	if gone, listErr := metricSamples.List(ctx, ref, 0, 1<<62); listErr != nil || len(gone) != 0 {
		t.Fatalf("metric samples after prune = %v (err %v), want empty", gone, listErr)
	}

	// The rolled-up aggregate survives, byte-for-byte identical to pre-prune.
	after, err := repo.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll after prune: %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("aggregates after prune = %d, want 1 (must survive source pruning)", len(after))
	}
	if after[0] != pre {
		t.Errorf("aggregate changed across prune: before %+v, after %+v", pre, after[0])
	}
}
