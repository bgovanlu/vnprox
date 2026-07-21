package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/baseline"
	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/flow"
	"github.com/bgovanlu/vnprox/internal/store"
)

// baseline.go wires T-1601's flow-baselining subsystem into this daemon: the
// scheduled learn job that (re)computes per-Ref traffic baselines from the
// retained flow_samples window, the retention prune loop that keeps
// baseline_profiles bounded, and the findings.BaselineProvider adapter whose
// RecentAnomalies() runs internal/baseline.Detect over the stored profiles +
// a recent flow window each findings cycle (backing the new_port/volume_spike/
// new_subnet findings, source "baseline"). Findings flow through the existing
// findings engine — this file adds no second findings path.
//
// The learn window is nominally [baseline].learn window days but is capped in
// practice by whatever flow_samples actually retains ([flows]
// retention_minutes, default 60) — a learned baseline is a durable summary of
// however much flow history exists, and deliberately outlives the raw flows
// once they prune (baseline_profiles' own [baseline] profile_retention_days,
// default 90).

const (
	// baselineLearnWindowDays is the nominal learning window Learn summarizes.
	baselineLearnWindowDays = baseline.DefaultLearnWindowDays
	// baselineRecentLookbackSeconds bounds the recent flow window
	// RecentAnomalies replays against each learned baseline — a small, recent
	// slice is enough to catch a currently-ongoing deviation (the finding is
	// hysteresis-debounced over several Engine cycles anyway), mirroring
	// serviceclassify.go's flowClassifyAdapter's bounded read rather than
	// re-scanning flow_samples' full retained window every findings cycle.
	baselineRecentLookbackSeconds = 7200
	// baselinePageLimit / baselineLearnRowCap / baselineRecentRowCap bound the
	// paginated flow_samples scans, so neither the learn job nor a findings
	// cycle can be dragged unbounded by a large ring.
	baselinePageLimit    = 5000
	baselineLearnRowCap  = 500_000
	baselineRecentRowCap = 50_000
	// baselinePruneInterval is how often the baseline_profiles retention prune
	// runs — coarse (once a day is plenty for a 90-day window), the same
	// "grows slowly, prune lazily" treatment the snapshot retention loop gets.
	baselinePruneInterval = 6 * time.Hour
	// baselineDetectTimeout caps one RecentAnomalies flow-scan (called from the
	// findings cycle, which has no ctx of its own).
	baselineDetectTimeout = 30 * time.Second
)

// baselineService owns the baseline_profiles repo and a lazily-set
// *store.FlowSampleRepo (server.go builds the findings engine — and thus this
// provider — before setupFlows creates flowRepo, the same two-step wiring
// flowClassifyAdapter uses). Safe for concurrent use.
type baselineService struct {
	profiles        *store.BaselineProfileRepo
	logger          *slog.Logger
	now             func() time.Time
	flowRepo        *store.FlowSampleRepo
	learnWindowDays int
	mu              sync.Mutex
}

// newBaselineService constructs a baselineService with its flow source unset
// (filled in by set() once setupFlows returns).
func newBaselineService(profiles *store.BaselineProfileRepo, cfg config.BaselineConfig, logger *slog.Logger) *baselineService {
	days := baselineLearnWindowDays
	_ = cfg // learn-window days is a package constant today; [baseline] carries retention/cadence only
	return &baselineService{
		profiles:        profiles,
		logger:          logger,
		now:             time.Now,
		learnWindowDays: days,
	}
}

// set points the service at the real recent-samples source, once flowRepo
// exists. Called from server.go's startup sequence, always before the
// findings RunLoop or the learn loop actually runs.
func (s *baselineService) set(flowRepo *store.FlowSampleRepo) {
	s.mu.Lock()
	s.flowRepo = flowRepo
	s.mu.Unlock()
}

func (s *baselineService) repo() *store.FlowSampleRepo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flowRepo
}

// RunLearnLoop runs one learn pass immediately, then every interval until ctx
// is cancelled — the same run-once-then-tick shape findings.Engine.RunLoop and
// every prune loop use. A single failed pass is logged and the loop
// continues; it never takes down the process.
func (s *baselineService) RunLearnLoop(ctx context.Context, interval time.Duration) error {
	if err := s.learnOnce(ctx); err != nil {
		s.logger.Error("baseline: learn pass failed", "error", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := s.learnOnce(ctx); err != nil {
				s.logger.Error("baseline: learn pass failed", "error", err)
			}
		}
	}
}

// learnOnce (re)learns a baseline for every Ref with flows in the current
// window and upserts it, skipping Refs whose window is empty (cold-start
// stays silent — no profile row, so no anomalies, T-1601 AC5).
func (s *baselineService) learnOnce(ctx context.Context) error {
	repo := s.repo()
	if repo == nil {
		return nil
	}
	now := s.now()
	window := baseline.Window{
		Start: now.AddDate(0, 0, -s.learnWindowDays).Unix(),
		End:   now.Unix(),
	}
	records, err := scanFlowRecords(ctx, repo, window.Start, baselineLearnRowCap)
	if err != nil {
		return fmt.Errorf("cmd/vnproxd: scanning flows for baseline learn: %w", err)
	}
	byRef := groupRecordsByRef(records)

	learned := 0
	for ref, recs := range byRef {
		prof := baseline.Learn(recs, ref, window)
		if prof.Empty() {
			continue
		}
		js, err := baseline.Marshal(prof)
		if err != nil {
			s.logger.Error("baseline: marshaling learned profile", "ref", ref, "error", err)
			continue
		}
		if err := s.profiles.Put(ctx, store.BaselineProfile{
			Ref:         ref,
			ProfileJSON: js,
			WindowStart: window.Start,
			WindowEnd:   window.End,
			UpdatedAt:   now.Unix(),
		}); err != nil {
			s.logger.Error("baseline: storing learned profile", "ref", ref, "error", err)
			continue
		}
		learned++
	}
	s.logger.Info("baseline: learn pass complete", "refs_learned", learned, "window_days", s.learnWindowDays)
	return nil
}

// RecentAnomalies implements findings.BaselineProvider: for every stored
// baseline, replays the recent flow window against it and returns the
// deviations. Returns (nil, nil) — no anomalies, not an error — if called
// before set() (cannot happen once the daemon is serving).
func (s *baselineService) RecentAnomalies() ([]baseline.Anomaly, error) {
	repo := s.repo()
	if repo == nil {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), baselineDetectTimeout)
	defer cancel()

	stored, err := s.profiles.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("cmd/vnproxd: listing baseline profiles: %w", err)
	}
	if len(stored) == 0 {
		return nil, nil
	}

	since := s.now().Add(-baselineRecentLookbackSeconds * time.Second).Unix()
	records, err := scanFlowRecords(ctx, repo, since, baselineRecentRowCap)
	if err != nil {
		return nil, fmt.Errorf("cmd/vnproxd: scanning recent flows for baseline detect: %w", err)
	}
	byRef := groupRecordsByRef(records)

	cfg := baseline.DefaultDetectConfig()
	var out []baseline.Anomaly
	for _, row := range stored {
		prof, err := baseline.Unmarshal(row.ProfileJSON)
		if err != nil {
			s.logger.Error("baseline: unmarshaling stored profile", "ref", row.Ref, "error", err)
			continue
		}
		out = append(out, baseline.Detect(prof, byRef[row.Ref], cfg)...)
	}
	return out, nil
}

// scanFlowRecords pages flow_samples from fromTs (newest-first) up to rowCap
// rows, converting each to a flow.Record.
func scanFlowRecords(ctx context.Context, repo *store.FlowSampleRepo, fromTs int64, rowCap int) ([]flow.Record, error) {
	var out []flow.Record
	cursor := ""
	for len(out) < rowCap {
		page, next, err := repo.Query(ctx, store.FlowFilter{FromTs: fromTs}, cursor, baselinePageLimit)
		if err != nil {
			return nil, err
		}
		for _, sm := range page {
			out = append(out, flowSampleToRecord(sm))
		}
		if next == "" {
			break
		}
		cursor = next
	}
	return out, nil
}

// flowSampleToRecord maps a stored flow_samples row to the normalized
// flow.Record internal/baseline consumes (including src_ref/dst_ref, unlike
// serviceclassify.go's classification adapter which needs only the tuple).
func flowSampleToRecord(sm store.FlowSample) flow.Record {
	return flow.Record{
		Node:    sm.Node,
		SrcIP:   sm.SrcIP,
		DstIP:   sm.DstIP,
		SrcRef:  sm.SrcRef,
		DstRef:  sm.DstRef,
		At:      sm.At,
		Bytes:   sm.Bytes,
		Packets: sm.Packets,
		SrcPort: sm.SrcPort,
		DstPort: sm.DstPort,
		Proto:   sm.Proto,
		VLAN:    sm.VLAN,
	}
}

// groupRecordsByRef buckets records by every inventory Ref they involve (a
// record with both src_ref and dst_ref set contributes to both buckets), so a
// per-Ref Learn/Detect sees exactly the flows touching that Ref.
func groupRecordsByRef(records []flow.Record) map[string][]flow.Record {
	byRef := map[string][]flow.Record{}
	for _, rec := range records {
		if rec.SrcRef != "" {
			byRef[rec.SrcRef] = append(byRef[rec.SrcRef], rec)
		}
		if rec.DstRef != "" && rec.DstRef != rec.SrcRef {
			byRef[rec.DstRef] = append(byRef[rec.DstRef], rec)
		}
	}
	return byRef
}
