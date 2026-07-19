package latmesh

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"
)

// Default tunables (docs/features/monitoring.md §1's "deliberately coarse,
// this is a mesh not a flood" framing for probing; retention numbers are
// this task's own documented choice, mirroring internal/flow's shape at a
// smaller scale since a probe tick is far cheaper than a flow record).
const (
	DefaultProbeIntervalSec = 10
	DefaultRetentionMinutes = 60
	DefaultMaxRows          = 500_000
	DefaultRollingWindow    = 5 * time.Minute
	DefaultPruneInterval    = time.Minute
)

// Config configures a Service. Store/Discoverer/Prober are independently
// optional: a nil Discoverer or Prober makes Tick a no-op (nothing to
// probe with); a nil Store disables persistence and every query method
// (Heatmap/History/Baseline all degrade to "no data", never an error) —
// the same nil-dependency degraded-mode convention every other Config in
// this codebase follows.
type Config struct {
	Store      Ring
	Discoverer Discoverer
	Prober     Prober
	Logger     *slog.Logger
	// Now overrides time.Now for tests (prune-tick/probe-tick time-travel),
	// mirroring every other clock-injecting Config in this codebase.
	Now              func() time.Time
	ProbeIntervalSec int
	RetentionMinutes int
	MaxRows          int64
	RollingWindow    time.Duration
}

// Service is T-1303's scheduler + bounded-ring store + query surface:
// Tick/RunLoop probe every pair Discoverer names on ProbeIntervalSec and
// persist the readings; RunPruneLoop enforces the documented ring bound
// (retention window AND hard row cap, whichever is smaller prunes first);
// Heatmap/History/Baseline serve GET /latmesh/heatmap, GET /latmesh/history,
// and T-806's verify-live baseline handoff respectively.
type Service struct {
	logger *slog.Logger
	now    func() time.Time
	cfg    Config
}

// New builds a Service from cfg, defaulting unset tunables.
func New(cfg Config) *Service {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.ProbeIntervalSec <= 0 {
		cfg.ProbeIntervalSec = DefaultProbeIntervalSec
	}
	if cfg.RetentionMinutes <= 0 {
		cfg.RetentionMinutes = DefaultRetentionMinutes
	}
	if cfg.MaxRows <= 0 {
		cfg.MaxRows = DefaultMaxRows
	}
	if cfg.RollingWindow <= 0 {
		cfg.RollingWindow = DefaultRollingWindow
	}
	return &Service{cfg: cfg, logger: cfg.Logger, now: cfg.Now}
}

// Tick discovers this node's current pair set and probes each exactly
// once, persisting the resulting readings as one batch. A Probe error
// (the probe could not even be attempted — see Prober's doc comment) is
// logged and recorded as a 100%-loss reading rather than dropping that
// pair's sample entirely, so a persistently-unreachable link still shows up
// in the ring (and can still fire path_loss) instead of silently vanishing
// from history.
func (s *Service) Tick(ctx context.Context) {
	if s.cfg.Discoverer == nil || s.cfg.Prober == nil {
		return
	}
	pairs := s.cfg.Discoverer.Pairs()
	if len(pairs) == 0 {
		return
	}

	now := s.now().Unix()
	samples := make([]Sample, 0, len(pairs))
	for _, p := range pairs {
		reading, err := s.cfg.Prober.Probe(ctx, p)
		if err != nil {
			s.logger.Warn("latmesh: probe attempt failed, recording as full loss", "linkId", p.LinkID, "error", err)
			reading = Reading{LossPct: 100}
		}
		samples = append(samples, Sample{
			LinkID: p.LinkID, Fabric: p.Fabric, FromNode: p.FromNode, ToNode: p.ToNode,
			At: now, RttMs: reading.RttMs, LossPct: reading.LossPct,
		})
	}

	if s.cfg.Store == nil {
		return
	}
	if err := s.cfg.Store.InsertBatch(ctx, toStoreSamples(samples)); err != nil {
		s.logger.Error("latmesh: persisting probe samples", "count", len(samples), "error", err)
	}
}

// RunLoop drives the periodic probe cycle on ProbeIntervalSec until ctx is
// cancelled — the same owned-goroutine/ticker shape internal/flow.Service.
// RunPruneLoop and internal/findings.Engine.RunLoop both use, per
// docs/development.md's "every goroutine has an owner and a shutdown path".
// Delegates to RunTicker (this package's shared scheduler primitive) rather
// than hand-rolling its own ticker loop, so a sibling package built on this
// one (internal/mtuprobe, T-1306) can reuse the exact same mechanism.
func (s *Service) RunLoop(ctx context.Context) error {
	interval := time.Duration(s.cfg.ProbeIntervalSec) * time.Second
	return RunTicker(ctx, interval, s.Tick)
}

// RunPruneLoop enforces this package's documented ring bound (retention
// window AND hard row cap) every interval until ctx is cancelled — mirrors
// internal/flow.Service.RunPruneLoop exactly. A nil Store makes this an
// immediate no-op.
func (s *Service) RunPruneLoop(ctx context.Context, interval time.Duration) error {
	if s.cfg.Store == nil {
		return nil
	}
	if interval <= 0 {
		interval = DefaultPruneInterval
	}
	return RunTicker(ctx, interval, s.prune)
}

func (s *Service) prune(ctx context.Context) {
	cutoff := s.now().Add(-time.Duration(s.cfg.RetentionMinutes) * time.Minute).Unix()
	if _, err := s.cfg.Store.PruneOlderThan(ctx, cutoff); err != nil {
		s.logger.Error("latmesh: pruning latency_samples by retention window", "error", err)
	}
	if _, err := s.cfg.Store.PruneToCap(ctx, s.cfg.MaxRows); err != nil {
		s.logger.Error("latmesh: pruning latency_samples to the hard row cap", "error", err)
	}
}

// Heatmap returns GET /latmesh/heatmap's per-link current-plus-rolling
// status: the most recent sample for every link this node has ever probed,
// paired with a rolling mean over Config.RollingWindow. A nil Store returns
// (nil, nil) — no data, not an error, matching every other query method's
// degraded-mode convention.
func (s *Service) Heatmap(ctx context.Context) ([]LinkHeat, error) {
	if s.cfg.Store == nil {
		return nil, nil
	}
	latest, err := s.cfg.Store.LatestPerLink(ctx)
	if err != nil {
		return nil, fmt.Errorf("latmesh: reading latest-per-link samples: %w", err)
	}

	now := s.now().Unix()
	windowSec := int64(s.cfg.RollingWindow.Seconds())
	out := make([]LinkHeat, 0, len(latest))
	for _, l := range latest {
		rollRtt, rollLoss, n, err := s.rollingStats(ctx, l.LinkID, now-windowSec, now)
		if err != nil {
			return nil, err
		}
		out = append(out, LinkHeat{
			LinkID: l.LinkID, Fabric: Fabric(l.Fabric), FromNode: l.FromNode, ToNode: l.ToNode,
			At: l.At, RttMs: l.RttMs, LossPct: l.LossPct,
			RollingRttMs: rollRtt, RollingLossPct: rollLoss, SampleCount: n,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LinkID < out[j].LinkID })
	return out, nil
}

// LatMeshHeatmap adapts Heatmap to internal/findings.LatMeshProvider's
// no-context method signature (the same synchronous-seam shape
// CorosyncProvider.CorosyncStatus/MetricsProvider.Live already use) —
// *Service satisfies that interface structurally with no adapter type
// needed, exactly like *metrics.Sampler satisfies MetricsProvider.
func (s *Service) LatMeshHeatmap() ([]LinkHeat, error) {
	return s.Heatmap(context.Background())
}

func (s *Service) rollingStats(ctx context.Context, linkID string, fromTs, toTs int64) (rttMs, lossPct float64, n int, err error) {
	samples, err := s.cfg.Store.QueryRange(ctx, linkID, fromTs, toTs)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("latmesh: querying rolling window for %s: %w", linkID, err)
	}
	if len(samples) == 0 {
		return 0, 0, 0, nil
	}
	var rttSum, lossSum float64
	for _, sm := range samples {
		rttSum += sm.RttMs
		lossSum += sm.LossPct
	}
	n = len(samples)
	return rttSum / float64(n), lossSum / float64(n), n, nil
}

// History returns linkID's raw samples in [fromTs, toTs] — GET /latmesh/
// history's response body (internal/api/latmesh.go) and the inspector
// sparkline's data source (T-908 pattern). A nil Store returns (nil, nil).
func (s *Service) History(ctx context.Context, linkID string, fromTs, toTs int64) ([]Sample, error) {
	if s.cfg.Store == nil {
		return nil, nil
	}
	rows, err := s.cfg.Store.QueryRange(ctx, linkID, fromTs, toTs)
	if err != nil {
		return nil, fmt.Errorf("latmesh: querying history for %s: %w", linkID, err)
	}
	out := make([]Sample, len(rows))
	for i, r := range rows {
		out[i] = fromStoreSample(r)
	}
	return out, nil
}

// Baseline computes linkID's historical baseline (p50/p95 RTT, mean loss%)
// over every retained sample for that link — the consumable function
// docs/features/monitoring.md §5 promises T-806's verify-live UX (wiring
// that caller in is a documented follow-up, not this card's own work — see
// this task's completion report). ok is false when linkID has no retained
// samples at all (a link this node has never probed, or one that hasn't
// produced a reading yet); a nil Store also reports ok=false, never an
// error, matching every other query method's degraded-mode convention.
func (s *Service) Baseline(ctx context.Context, linkID string) (p50Ms, p95Ms, lossPct float64, ok bool, err error) {
	if s.cfg.Store == nil {
		return 0, 0, 0, false, nil
	}
	rows, err := s.cfg.Store.QueryRange(ctx, linkID, 0, s.now().Unix())
	if err != nil {
		return 0, 0, 0, false, fmt.Errorf("latmesh: querying baseline samples for %s: %w", linkID, err)
	}
	readings := make([]Reading, len(rows))
	for i, r := range rows {
		readings[i] = Reading{RttMs: r.RttMs, LossPct: r.LossPct}
	}
	p50, p95, loss, ok := Baseline(readings)
	return p50, p95, loss, ok, nil
}
