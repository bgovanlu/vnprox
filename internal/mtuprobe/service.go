package mtuprobe

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/latmesh"
)

// DefaultProbeIntervalSec is this package's default probe cadence — far
// coarser than internal/latmesh.DefaultProbeIntervalSec (10s): MTU rarely
// changes, so there's no need to hammer every path every few seconds (this
// task's card, docs/features/monitoring.md §5).
const DefaultProbeIntervalSec = 300

// Config configures a Service. Discoverer/Prober are independently
// optional: a nil Discoverer or Prober makes Tick a no-op, the same
// nil-dependency degraded-mode convention internal/latmesh.Config follows.
// Discoverer is internal/latmesh.Discoverer itself (see doc.go) — in
// production, the same *latmesh.GraphDiscoverer instance latmesh.Service
// uses, so this package probes exactly the paths the mesh already knows
// about.
type Config struct {
	Discoverer latmesh.Discoverer
	Prober     Prober
	Logger     *slog.Logger
	// Now overrides time.Now for tests, mirroring latmesh.Config's own
	// clock-injection seam.
	Now              func() time.Time
	ProbeIntervalSec int
}

// Result is one link's most recently verified (measured) path MTU —
// Service's current-state store, keyed by LinkID (see doc.go: "current
// state, not a ring").
type Result struct {
	LinkID     string
	Fabric     latmesh.Fabric
	FromNode   string
	ToNode     string
	MTU        int
	At         int64 // unix seconds of the probe that produced this reading
	ProbeCount int
}

// Service is T-1306's scheduler + current-state store: Tick/RunLoop probe
// every pair latmesh's Discoverer names on ProbeIntervalSec and hold the
// latest verified MTU per link; Results/Result/MeasuredUnderlayMTU serve
// GET /mtuprobe/results, the map-edge annotation, and
// internal/findings.MTUProvider respectively.
type Service struct {
	logger  *slog.Logger
	now     func() time.Time
	results map[string]Result
	cfg     Config
	mu      sync.RWMutex
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
	return &Service{cfg: cfg, logger: cfg.Logger, now: cfg.Now, results: map[string]Result{}}
}

// Tick discovers this node's current pair set (via the shared latmesh
// Discoverer) and probes each exactly once, replacing that link's stored
// Result on success. Unlike latmesh.Service.Tick's "record as full loss"
// stance on a probe error, a failed attempt here simply leaves the prior
// reading (if any) in place — MTU has no meaningful "loss" analog, and a
// transient probe failure overwriting a known-good MTU with a false
// zero/absence would be a regression AC2 explicitly rules out ("no probe
// result yet shows no verified badge, not a stale/zero value" — a
// momentary failure is not "no probe result yet", it's "the last known
// good reading", so it is kept, not cleared).
func (s *Service) Tick(ctx context.Context) {
	if s.cfg.Discoverer == nil || s.cfg.Prober == nil {
		return
	}
	pairs := s.cfg.Discoverer.Pairs()
	if len(pairs) == 0 {
		return
	}

	now := s.now().Unix()
	for _, p := range pairs {
		mtu, probeCount, err := s.cfg.Prober.ProbeMTU(ctx, p)
		if err != nil {
			s.logger.Warn("mtuprobe: probe attempt failed, keeping prior reading", "linkId", p.LinkID, "error", err)
			continue
		}
		if mtu <= 0 {
			s.logger.Warn("mtuprobe: path could not carry even the minimum MTU, keeping prior reading", "linkId", p.LinkID, "minMtu", MinMTU)
			continue
		}
		s.mu.Lock()
		s.results[p.LinkID] = Result{
			LinkID: p.LinkID, Fabric: p.Fabric, FromNode: p.FromNode, ToNode: p.ToNode,
			MTU: mtu, At: now, ProbeCount: probeCount,
		}
		s.mu.Unlock()
	}
}

// RunLoop drives the periodic probe cycle on ProbeIntervalSec until ctx is
// cancelled — delegates to latmesh.RunTicker, the exact shared scheduler
// primitive internal/latmesh.Service.RunLoop itself uses (doc.go's "reuses
// T-1303's infrastructure, does not duplicate it"; AC5). cmd/vnproxd
// registers this as its own owned run-group actor, on this package's own
// coarser interval — a second owned goroutine, not a second scheduler
// implementation.
func (s *Service) RunLoop(ctx context.Context) error {
	interval := time.Duration(s.cfg.ProbeIntervalSec) * time.Second
	return latmesh.RunTicker(ctx, interval, s.Tick)
}

// Results returns every currently-known verified MTU reading, sorted by
// LinkID — GET /mtuprobe/results' data source and the map-edge annotation's
// input (web/src/topology/mtuOverlay.ts's computeMTUOverlayEdges). A link
// this node has never successfully probed simply has no entry — never a
// synthesized zero/stale placeholder (AC2).
func (s *Service) Results() []Result {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Result, 0, len(s.results))
	for _, r := range s.results {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LinkID < out[j].LinkID })
	return out
}

// Result looks up one link's verified MTU by LinkID. ok is false when the
// prober hasn't reached that path yet.
func (s *Service) Result(linkID string) (Result, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.results[linkID]
	return r, ok
}

// MeasuredUnderlayMTU implements internal/findings.MTUProvider: the minimum
// verified MTU across every link this node has successfully probed
// outbound from node — mirroring health_vxlanmtu.go's observedUnderlayMTU
// per-node min-across-NICs shape, but sourced from a live, measured,
// end-to-end DF-probe reading instead of a local NIC MTU read (the tighter
// input AC3's vxlan_underlay_mtu upgrade consumes). ok is false when this
// node has no verified reading for any outbound link yet.
func (s *Service) MeasuredUnderlayMTU(node string) (mtu int, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, r := range s.results {
		if r.FromNode != node {
			continue
		}
		if !ok || r.MTU < mtu {
			mtu, ok = r.MTU, true
		}
	}
	return mtu, ok
}
