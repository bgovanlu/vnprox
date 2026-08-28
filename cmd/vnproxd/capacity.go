// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/bgovanlu/vnprox/internal/capacity"
	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/ipam"
	"github.com/bgovanlu/vnprox/internal/store"
)

// capacityDocsLink is the remediation pointer for a capacity forecast finding
// — a forecast is a heads-up to plan ahead, not something with a computable
// config patch, so (like the other non-fixable producers) it links to the
// docs (docs/features/monitoring.md §5's "remediation ... docs link
// otherwise").
const capacityDocsLink = "docs/features/monitoring.md#2-metrics--history"

// capacitySink adapts *store.CapacityAggregateRepo to capacity.Sink, mapping
// the domain capacity.Aggregate into the store row shape and stamping
// created_at from the injected clock. This composition-root conversion keeps
// internal/capacity free of any internal/store import.
type capacitySink struct {
	repo *store.CapacityAggregateRepo
	now  func() time.Time
}

func (s capacitySink) Upsert(ctx context.Context, a capacity.Aggregate) error {
	return s.repo.Upsert(ctx, store.CapacityAggregate{
		Ref:            a.Ref,
		Kind:           string(a.Kind),
		BucketAt:       a.BucketAt.Unix(),
		AvgUtilization: a.AvgUtil,
		MaxUtilization: a.MaxUtil,
		CreatedAt:      s.now().Unix(),
	})
}

// capacityBucketSource computes one UTC day's utilization aggregates: link
// utilization from that day's metric_samples counter deltas against the live
// graph's link speeds, and IPAM pool consumption from a point-in-time
// allocation-count read. It is the concrete capacity.BucketSource the rollup
// job drives.
//
// Only PhysNic-kind links are rolled up: they are the uplink capacity a
// forecast cares about, and they carry a negotiated SpeedMbps directly.
// Bond-aggregate speed (sum of active slaves) is a deliberate future
// refinement, noted in this task's report rather than approximated silently.
type capacityBucketSource struct {
	metrics *store.MetricSampleRepo
	graph   *inventory.Graph
	ipam    *ipam.Service
	logger  *slog.Logger
}

func (s capacityBucketSource) DayAggregates(ctx context.Context, dayStart, dayEnd time.Time) ([]capacity.Aggregate, error) {
	out := s.linkAggregates(ctx, dayStart, dayEnd)
	out = append(out, s.poolAggregates(ctx)...)
	return out, nil
}

// linkAggregates rolls up each speed-bearing physical NIC's daily utilization
// from its metric_samples counter history.
func (s capacityBucketSource) linkAggregates(ctx context.Context, dayStart, dayEnd time.Time) []capacity.Aggregate {
	if s.graph == nil || s.metrics == nil {
		return nil
	}
	snap := s.graph.Snapshot()
	var out []capacity.Aggregate
	for _, e := range snap.All() {
		nic, ok := e.(*inventory.PhysNic)
		if !ok || nic.SpeedMbps <= 0 {
			continue
		}
		refStr := nic.String()
		rows, err := s.metrics.List(ctx, refStr, dayStart.Unix(), dayEnd.Unix()-1)
		if err != nil {
			s.logger.Warn("capacity: reading metric samples for link rollup", "ref", refStr, "error", err)
			continue
		}
		samples := make([]capacity.CounterSample, 0, len(rows))
		for _, r := range rows {
			samples = append(samples, capacity.CounterSample{
				At:      time.Unix(r.At, 0).UTC(),
				RxBytes: uint64(nonNegative(r.RxBytes.Int64)),
				TxBytes: uint64(nonNegative(r.TxBytes.Int64)),
			})
		}
		avg, max, ok := capacity.LinkDailyUtil(samples, nic.SpeedMbps)
		if !ok {
			continue
		}
		out = append(out, capacity.Aggregate{
			Ref:     refStr,
			Kind:    capacity.KindLink,
			AvgUtil: avg,
			MaxUtil: max,
		})
	}
	return out
}

// poolAggregates snapshots each subnet's current allocation as a percentage of
// its total — a single daily reading, so avg == max for the pool bucket.
func (s capacityBucketSource) poolAggregates(ctx context.Context) []capacity.Aggregate {
	if s.ipam == nil {
		return nil
	}
	resp, err := s.ipam.Subnets(ctx)
	if err != nil {
		s.logger.Warn("capacity: reading IPAM subnets for pool rollup", "error", err)
		return nil
	}
	var out []capacity.Aggregate
	for _, sub := range resp.Items {
		if sub.Total <= 0 {
			continue
		}
		util := capacity.PoolUtil(sub.Allocated, sub.Total)
		out = append(out, capacity.Aggregate{
			Ref:     sub.CIDR,
			Kind:    capacity.KindIPAMPool,
			AvgUtil: util,
			MaxUtil: util,
		})
	}
	return out
}

func nonNegative(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

// capacityFindingsAdapter adapts the rolled-up capacity_aggregates history
// into the unified findings stream (findings.CapacityProvider): it reads every
// aggregate, runs internal/capacity.Analyze to fit a trend and project a
// crossing, and converts each ForecastFinding into a Finding (source
// capacity). This composition-root conversion keeps internal/capacity from
// importing internal/findings (the same decoupling ipamFindingsAdapter
// provides for internal/ipam). Nil-safe: a nil repo contributes no findings.
type capacityFindingsAdapter struct {
	baseCtx     context.Context //nolint:containedctx // daemon shutdown ctx — see ipamFindingsAdapter.baseCtx
	repo        *store.CapacityAggregateRepo
	logger      *slog.Logger
	horizonDays int
}

func (a capacityFindingsAdapter) Findings() []findings.Finding {
	if a.repo == nil {
		return nil
	}
	ctx, cancel := findingsAdapterCtx(a.baseCtx)
	defer cancel()
	rows, err := a.repo.ListAll(ctx)
	if err != nil {
		a.logger.Warn("findings: listing capacity aggregates", "error", err)
		return nil
	}
	aggs := make([]capacity.Aggregate, 0, len(rows))
	for _, r := range rows {
		aggs = append(aggs, capacity.Aggregate{
			BucketAt: time.Unix(r.BucketAt, 0).UTC(),
			Ref:      r.Ref,
			Kind:     capacity.Kind(r.Kind),
			AvgUtil:  r.AvgUtilization,
			MaxUtil:  r.MaxUtilization,
		})
	}
	forecasts := capacity.Analyze(aggs, a.horizonDays)
	out := make([]findings.Finding, 0, len(forecasts))
	for _, f := range forecasts {
		out = append(out, capacityForecastToFinding(f))
	}
	return out
}

// capacityForecastToFinding maps one capacity.ForecastFinding to the unified
// Finding shape: source capacity, check capacity_link_forecast/
// capacity_ipam_forecast, a content-derived stable id ("capacity:check|ref")
// so re-evaluating unchanged aggregates reproduces byte-identical ids (the
// findings engine's change/notify tracking depends on it). Never fixable — a
// forecast is a heads-up, not a config patch — so it carries a docs link.
func capacityForecastToFinding(f capacity.ForecastFinding) findings.Finding {
	// Nodes is always a non-nil slice ([]  serializes as JSON [], never null,
	// which would crash web/src/findings/filters.ts — the same hazard
	// ipamConflictToFinding documents). A link ref carries its node; a pool
	// ref is a CIDR with no node, so its Nodes is legitimately empty.
	nodes := []string{}
	if f.Kind == capacity.KindLink {
		if ref, err := inventory.ParseRef(f.Ref); err == nil && ref.Node != "" {
			nodes = []string{ref.Node}
		}
	}
	return findings.Finding{
		ID:       "capacity:" + f.Check + "|" + f.Ref,
		Source:   findings.SourceCapacity,
		Check:    f.Check,
		Severity: findings.SeverityWarning,
		Detail:   f.Detail,
		Nodes:    nodes,
		Refs:     []string{f.Ref},
		DocsLink: capacityDocsLink,
	}
}

// capacityExportService adapts *store.CapacityAggregateRepo to
// api.CapacityService, applying the configured retention window as the export
// bound. The store-backed ListByRefSince plus this RetentionDays is what makes
// GET /capacity/export honor "never a row older than aggregate_retention_days"
// even between prune ticks (T-1606 AC4).
type capacityExportService struct {
	repo          *store.CapacityAggregateRepo
	retentionDays int
}

func (s capacityExportService) ExportHistory(ctx context.Context, ref, kind string, since int64) ([]store.CapacityAggregate, error) {
	return s.repo.ListByRefSince(ctx, ref, kind, since)
}

func (s capacityExportService) RetentionDays() int { return s.retentionDays }

// setupCapacity wires T-1606's capacity forecasting: the daily-rollup job (its
// BucketSource reads metric_samples + the live graph for links and
// internal/ipam for pools; its Sink is the capacity_aggregates repo), the
// findings provider that trends those aggregates, the retention-bounded export
// service, and the aggregate prune loop. metricSamples/graph are always
// present; ipamConcrete may be nil (degraded mode, no PVE client), in which
// case pool rollups are simply absent — the same "nil dependency, that half
// quietly contributes nothing" degradation every other producer uses.
func setupCapacity(ctx context.Context, cfg *config.Config, db *store.DB, graph *inventory.Graph, metricSamples *store.MetricSampleRepo, ipamConcrete *ipam.Service, logger *slog.Logger) (rollup, prune func(context.Context) error, provider findings.CapacityProvider, export capacityExportService) {
	repo := store.NewCapacityAggregateRepo(db)
	retentionDays := cfg.Capacity.AggregateRetentionDays
	horizonDays := cfg.Capacity.ForecastHorizonDays

	src := capacityBucketSource{metrics: metricSamples, graph: graph, ipam: ipamConcrete, logger: logger}
	sink := capacitySink{repo: repo, now: time.Now}
	job := capacity.NewRollupJob(src, sink, time.Now, logger)

	rollup = job.Run
	prune = func(ctx context.Context) error {
		return repo.RunPruneLoop(ctx, capacityPruneInterval, retentionDays, func(err error) {
			logger.Error("store: capacity_aggregates prune failed", "error", err)
		})
	}
	provider = capacityFindingsAdapter{baseCtx: ctx, repo: repo, logger: logger, horizonDays: horizonDays}
	export = capacityExportService{repo: repo, retentionDays: retentionDays}
	return rollup, prune, provider, export
}
