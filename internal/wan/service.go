// SPDX-License-Identifier: Apache-2.0

package wan

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/latmesh"
	"github.com/bgovanlu/vnprox/internal/store"
)

// wanUplinkFromLinkID extracts a WAN Pair's uplink label back out of its
// LinkID ("wan:<uplink>|<fromNode>-><toNode>") — the inverse of
// latmesh.ComputeLinkID(Fabric, uplink, fromNode, toNode), mirroring
// store.wanUplinkFromLinkID's identical (but unexported, package-private)
// parser; duplicated here rather than exported cross-package since it's a
// three-line pure string operation, not a shared stateful dependency.
func wanUplinkFromLinkID(linkID string) string {
	before, _, ok := strings.Cut(linkID, "|")
	if !ok {
		return ""
	}
	_, label, ok := strings.Cut(before, ":")
	if !ok {
		return ""
	}
	return label
}

// Default tunables — this task's own documented choice (the card names no
// specific numbers), mirroring internal/latmesh's own defaults: a WAN
// uplink is checked on the same 10s cadence a LAN link is (coarse enough
// that this probe carries no meaningful load on an upstream link), with the
// same 60-minute/500,000-row ring bound.
const (
	DefaultProbeIntervalSec = latmesh.DefaultProbeIntervalSec
	DefaultRetentionMinutes = latmesh.DefaultRetentionMinutes
	DefaultMaxRows          = latmesh.DefaultMaxRows
	DefaultRollingWindow    = latmesh.DefaultRollingWindow
	DefaultPruneInterval    = latmesh.DefaultPruneInterval
	// DefaultLossWarnPct is this package's own default breach threshold —
	// looser than internal/latmesh's 2% LAN threshold (health_thresholds.go)
	// since an ordinary WAN path's baseline jitter/loss is inherently higher
	// than a LAN/corosync link's; see internal/findings/health_wan.go.
	DefaultLossWarnPct = 20.0
)

// Config configures a Service. Store/Targets are independently optional in
// the same "nil dependency degrades quietly" sense every other Config in
// this codebase follows: a nil Store disables persistence and Heatmap/
// Status/Export all report "no data" rather than erroring; a nil Targets
// makes the probe loop a no-op (nothing configured to probe) and GET/PUT
// /wan/targets simply aren't mountable by the API layer (mirrors
// mountWanRoutes' own nil-Targets skip).
type Config struct {
	Store     latmesh.Ring
	Targets   TargetStore
	LocalNode func() string
	// Prober overrides the default latmesh.RealProber{} — tests substitute
	// a scripted fake here (the same seam latmesh.Config.Prober itself
	// exposes).
	Prober latmesh.Prober
	Logger *slog.Logger
	// Now overrides time.Now for tests, threaded straight into the
	// underlying latmesh.Service.
	Now              func() time.Time
	ProbeIntervalSec int
	RetentionMinutes int
	MaxRows          int64
	RollingWindow    time.Duration
	LossWarnPct      float64
}

// Service is T-1405's WAN health scheduler + query surface. Its probe loop
// is *internal/latmesh.Service itself (mesh field below) — see this
// package's doc comment for why this is literal scheduler reuse, not a
// second implementation.
type Service struct {
	mesh       *latmesh.Service
	targets    TargetStore
	targetRepo *store.WanProbeSampleRepo // optional, only set when Config.Store is one (Export's data source)
	logger     *slog.Logger
	lossWarn   float64
}

// New builds a Service from cfg, defaulting unset tunables.
func New(cfg Config) *Service {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	prober := cfg.Prober
	if prober == nil {
		prober = latmesh.RealProber{}
	}
	lossWarn := cfg.LossWarnPct
	if lossWarn <= 0 {
		lossWarn = DefaultLossWarnPct
	}

	discoverer := &TargetDiscoverer{Store: cfg.Targets, LocalNode: cfg.LocalNode, Logger: logger}
	mesh := latmesh.New(latmesh.Config{
		Store:            cfg.Store,
		Discoverer:       discoverer,
		Prober:           prober,
		Logger:           logger,
		Now:              cfg.Now,
		ProbeIntervalSec: cfg.ProbeIntervalSec,
		RetentionMinutes: cfg.RetentionMinutes,
		MaxRows:          cfg.MaxRows,
		RollingWindow:    cfg.RollingWindow,
	})

	svc := &Service{mesh: mesh, targets: cfg.Targets, logger: logger, lossWarn: lossWarn}
	if repo, ok := cfg.Store.(*store.WanProbeSampleRepo); ok {
		svc.targetRepo = repo
	}
	return svc
}

// RunLoop drives the periodic probe cycle until ctx is cancelled — a thin
// passthrough to the underlying *latmesh.Service, the same owned-goroutine/
// shutdown-path shape every other actor in this codebase follows.
func (s *Service) RunLoop(ctx context.Context) error { return s.mesh.RunLoop(ctx) }

// RunPruneLoop enforces this task's own ring bound (retention window AND
// hard row cap) — a thin passthrough to the underlying *latmesh.Service.
func (s *Service) RunPruneLoop(ctx context.Context, interval time.Duration) error {
	return s.mesh.RunPruneLoop(ctx, interval)
}

// Tick runs one probe cycle immediately (test/diagnostic seam, mirrors
// latmesh.Service.Tick).
func (s *Service) Tick(ctx context.Context) { s.mesh.Tick(ctx) }

// Heatmap returns every currently-known WAN link's current-plus-rolling
// status — the same shape GET /latmesh/heatmap serves, reused verbatim.
func (s *Service) Heatmap(ctx context.Context) ([]latmesh.LinkHeat, error) {
	return s.mesh.Heatmap(ctx)
}

// WanHeatmap adapts Heatmap to internal/findings.WanProvider's no-context
// method signature — the same synchronous-seam shape *latmesh.Service.
// LatMeshHeatmap already establishes for LatMeshProvider.
func (s *Service) WanHeatmap() ([]latmesh.LinkHeat, error) {
	return s.mesh.Heatmap(context.Background())
}

// History returns linkID's raw samples in [fromTs, toTs].
func (s *Service) History(ctx context.Context, linkID string, fromTs, toTs int64) ([]latmesh.Sample, error) {
	return s.mesh.History(ctx, linkID, fromTs, toTs)
}

// ListTargets returns node's currently configured reference targets.
func (s *Service) ListTargets(ctx context.Context, node string) ([]Target, error) {
	if s.targets == nil {
		return nil, nil
	}
	rows, err := s.targets.ListByNode(ctx, node)
	if err != nil {
		return nil, fmt.Errorf("wan: listing targets for node %s: %w", node, err)
	}
	out := make([]Target, len(rows))
	for i, r := range rows {
		out[i] = Target{Node: r.Node, Uplink: r.Uplink, Host: r.Host}
	}
	return out, nil
}

// ReplaceTargets replaces node's entire configured target list — PUT
// /wan/targets' full-set-replace semantics (this package's doc comment
// notes GET/PUT operate on the requesting node's own local store only).
func (s *Service) ReplaceTargets(ctx context.Context, node string, targets []Target, now int64) error {
	if s.targets == nil {
		return fmt.Errorf("wan: no target store configured")
	}
	rows := make([]store.WanTarget, len(targets))
	for i, t := range targets {
		rows[i] = store.WanTarget{Node: node, Uplink: t.Uplink, Host: t.Host}
	}
	if err := s.targets.ReplaceForNode(ctx, node, rows, now); err != nil {
		return fmt.Errorf("wan: replacing targets for node %s: %w", node, err)
	}
	return nil
}

// Status computes GET /wan/status: every currently-probed link grouped by
// (node, uplink) — T-1405 AC2's "each uplink's status independently".
func (s *Service) Status(ctx context.Context, now int64) (Status, error) {
	links, err := s.mesh.Heatmap(ctx)
	if err != nil {
		return Status{}, fmt.Errorf("wan: reading heatmap for status: %w", err)
	}

	type key struct{ node, uplink string }
	grouped := map[key][]latmesh.LinkHeat{}
	var order []key
	for _, l := range links {
		k := key{node: l.FromNode, uplink: wanUplinkFromLinkID(l.LinkID)}
		if _, seen := grouped[k]; !seen {
			order = append(order, k)
		}
		grouped[k] = append(grouped[k], l)
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].node != order[j].node {
			return order[i].node < order[j].node
		}
		return order[i].uplink < order[j].uplink
	})

	out := Status{GeneratedAt: now}
	for _, k := range order {
		out.Uplinks = append(out.Uplinks, buildUplinkStatus(k.node, k.uplink, grouped[k], s.lossWarn))
	}
	return out, nil
}

// buildUplinkStatus aggregates one (node, uplink)'s already-probed links
// (links is never empty — see Status' grouping loop, which only ever adds a
// key when Heatmap actually returned a reading for it) into one
// UplinkStatus. A configured target with no reading yet (no Tick has run
// since it was added via PUT /wan/targets) simply has no entry in
// Status.Uplinks at all — the same "no result yet -> no entry, never a
// misleadingly-cheerful placeholder" stance internal/mtuprobe's own map-edge
// annotation doc comment establishes for its own "not yet probed" case.
func buildUplinkStatus(node, uplink string, links []latmesh.LinkHeat, lossWarn float64) UplinkStatus {
	sort.Slice(links, func(i, j int) bool { return links[i].ToNode < links[j].ToNode })

	u := UplinkStatus{Node: node, Uplink: uplink}
	var rttSum, lossSum float64
	allUnreachable := true
	anyDegraded := false

	for _, l := range links {
		reachable := l.LossPct < 100
		u.Targets = append(u.Targets, TargetStatus{
			Host: l.ToNode, At: l.At, RttMs: l.RttMs, LossPct: l.LossPct,
			RollingRttMs: l.RollingRttMs, RollingLossPct: l.RollingLossPct, Reachable: reachable,
		})
		rttSum += l.RollingRttMs
		lossSum += l.RollingLossPct
		if l.RollingLossPct < 100 {
			allUnreachable = false
		}
		if l.RollingLossPct > lossWarn {
			anyDegraded = true
		}
	}

	switch {
	case allUnreachable:
		u.Status = UplinkUnreachable
	case anyDegraded:
		u.Status = UplinkDegraded
	default:
		u.Status = UplinkHealthy
	}

	n := float64(len(links))
	u.RttMs = rttSum / n
	u.LossPct = lossSum / n
	u.AvailabilityPct = 100 - u.LossPct
	if u.AvailabilityPct < 0 {
		u.AvailabilityPct = 0
	}
	return u
}

// Export returns every currently-retained WAN probe sample (already
// bounded by the ring's own retention-window/row-cap prune, T-1405 AC4),
// newest first, capped at limit. Returns (nil, nil) when this Service
// wasn't constructed with a *store.WanProbeSampleRepo (e.g. a nil-Store
// Config, or a test double substituted for latmesh.Ring) — the same
// degraded-mode convention every other optional query surface in this
// codebase follows.
func (s *Service) Export(ctx context.Context, limit int64) ([]store.WanProbeSample, error) {
	if s.targetRepo == nil {
		return nil, nil
	}
	rows, err := s.targetRepo.QueryAll(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("wan: exporting probe samples: %w", err)
	}
	return rows, nil
}
