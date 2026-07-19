package migration

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// Default tunables — see doc.go's "The migration network" and "Dirty-rate
// estimate" sections for why each exists and why it is necessarily a
// documented heuristic rather than a measurement.
const (
	// DefaultDirtyRateFraction is the fraction of a guest's configured RAM
	// assumed to re-dirty per second absent any live guest instrumentation
	// — a conservative, reasoned default (comparable to a moderately busy
	// database/cache workload), not a measurement.
	DefaultDirtyRateFraction = 0.01
	// DefaultAssumedCapacityMbps is the fallback link-capacity figure used
	// when no shared bridge with resolvable member-NIC speed exists
	// between the two nodes (capacity.go) — a conservative single-GbE-link
	// assumption.
	DefaultAssumedCapacityMbps = 1000

	// warnLossPct/warnRttMs mirror internal/findings.HealthThresholds'
	// LatLossWarnPct/LatRttWarnMs defaults (2%/80ms) — the same line T-1303's
	// own path_latency_degraded/path_loss findings already draw between
	// "ordinary LAN jitter" and "worth flagging". Crossing either alone
	// (without the dirty-rate math also failing) escalates this package's
	// verdict from ok to tight.
	warnLossPct = 2.0
	warnRttMs   = 80.0
	// severelyDegradedLossPct/severelyDegradedRttMs are this package's own,
	// wider threshold (4x the warn line) for a link degraded enough that no
	// headroom estimate over it should be trusted — escalates straight to
	// insufficient regardless of the raw Mbps arithmetic.
	severelyDegradedLossPct = 8.0
	severelyDegradedRttMs   = 320.0
	// tightHeadroomMultiplier: headroom under this many multiples of the
	// estimated dirty-page rate is "thin enough to flag" (tight) even
	// though it technically still exceeds the dirty rate (insufficient's
	// own, stricter threshold).
	tightHeadroomMultiplier = 2.0
)

// GuestConfigReader is the subset of *pve.Client this package needs: the
// guest's own raw config (its configured RAM size — PVE's own knowledge,
// read fresh on every Plan call, never a shadow copy vnprox keeps for
// itself). Mirrors internal/probe.PVEExecer's "small interface, real type
// satisfies it directly" seam over the same client package. This is the
// *only* PVE-facing interface this package defines — see doc.go's
// "Advisory only" section: a single read-only method cannot reach any
// migration-start/evacuate endpoint.
type GuestConfigReader interface {
	GetGuestConfig(ctx context.Context, node string, kind pve.GuestKind, vmid int) (map[string]string, error)
}

// MigrationTrafficProvider resolves the current T-1504-classified
// "migration" service-class traffic volume (Mbps) this daemon has
// recently observed on node's own flow_samples ring — the "current
// migration traffic" half of headroom's arithmetic (docs/api.md's
// Migration planner section: "headroom = link capacity minus current
// migration traffic"). cmd/vnproxd wires this from the same flow_samples
// store + *flow.Classifier serviceclassify.go already wires for
// service_traffic_on_wrong_network — no second flow reader. ok is false
// when no recent data is available (a fresh daemon, or a node with
// flow sampling not enabled) — Plan treats that as zero current
// utilization, flagged with a caveat, never an error.
type MigrationTrafficProvider interface {
	MigrationTrafficMbps(ctx context.Context, node string) (mbps float64, ok bool)
}

// GraphSnapshotter is the subset of *inventory.Graph Plan needs — a
// snapshot to resolve the guest and the shared-bridge capacity proxy
// against. The live *inventory.Graph satisfies it directly, mirroring
// SimulatorGraph's identical one-method seam (internal/api/simulate.go).
type GraphSnapshotter interface {
	Snapshot() inventory.Snapshot
}

// Config configures a Planner. Graph is required; GuestConfig/Mesh/Traffic
// are independently optional — a nil dependency degrades that half of the
// assessment to a flagged best-effort default rather than an error, the
// same nil-dependency degraded-mode convention every other Config in this
// codebase follows (e.g. internal/latmesh.Config's Store/Discoverer/
// Prober).
type Config struct {
	Graph       GraphSnapshotter
	GuestConfig GuestConfigReader
	Mesh        MeshProvider
	Traffic     MigrationTrafficProvider
	Logger      *slog.Logger
	// Now overrides time.Now for tests, mirroring every other
	// clock-injecting Config in this codebase.
	Now func() time.Time
	// DirtyRateFraction/AssumedCapacityMbps override the package defaults
	// above; zero uses the default.
	DirtyRateFraction   float64
	AssumedCapacityMbps float64
}

// Planner is T-1507's pre-flight assessment engine — see doc.go.
type Planner struct {
	cfg Config
}

// New builds a Planner from cfg, defaulting unset tunables.
func New(cfg Config) *Planner {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.DirtyRateFraction <= 0 {
		cfg.DirtyRateFraction = DefaultDirtyRateFraction
	}
	if cfg.AssumedCapacityMbps <= 0 {
		cfg.AssumedCapacityMbps = DefaultAssumedCapacityMbps
	}
	return &Planner{cfg: cfg}
}

// Plan computes T-1507's pre-flight assessment for migrating/evacuating
// guest to targetNode. It never returns a Go error: every failure mode
// (an unresolvable guest, missing mesh/traffic/capacity data) is folded
// into Assessment.Caveats instead, mirroring internal/probe.Run's and
// internal/latmesh's own "the attempt itself is the answer" honesty
// contract — a caller always has a complete, displayable Assessment. See
// doc.go's "Advisory only" section: this method never triggers, blocks, or
// otherwise touches an actual migration.
func (p *Planner) Plan(ctx context.Context, guest inventory.Ref, targetNode string) Assessment {
	caveats := []string{}

	if p.cfg.Graph == nil {
		return Assessment{Verdict: VerdictTight, BestEffort: true, Caveats: []string{
			"no inventory source is wired; this assessment is unverified",
		}}
	}
	snap := p.cfg.Graph.Snapshot()

	ent, ok := snap.Get(guest)
	g, isGuest := ent.(*inventory.Guest)
	if !ok || !isGuest {
		return Assessment{Verdict: VerdictTight, BestEffort: true, Caveats: []string{
			fmt.Sprintf("guest %s could not be resolved in inventory; this assessment is unverified", guest.String()),
		}}
	}

	sourceNode := g.Node
	targetNode = strings.TrimSpace(targetNode)
	if sourceNode == "" || targetNode == "" || sourceNode == targetNode {
		return Assessment{Verdict: VerdictTight, BestEffort: true, Caveats: []string{
			fmt.Sprintf("guest %s has no distinct resolvable source/target node pair (source=%q, target=%q); this assessment is unverified", guest.String(), sourceNode, targetNode),
		}}
	}

	memoryMB, memOk := p.guestMemoryMB(ctx, g)
	if !memOk {
		caveats = append(caveats, "guest RAM size could not be read from PVE's own guest config; dirty-rate and transfer-time estimates use 0 MB and are not meaningful")
	}

	capacityMbps := p.cfg.AssumedCapacityMbps
	if cap, ok := resolveLinkCapacityMbps(snap, sourceNode, targetNode); ok {
		capacityMbps = cap.Mbps
	} else {
		caveats = append(caveats, fmt.Sprintf(
			"no shared bridge with resolvable NIC speed found between %s and %s; assuming a conservative %.0f Mbps link (no live reader of PVE's own configured migration network exists in this arc)",
			sourceNode, targetNode, p.cfg.AssumedCapacityMbps))
	}

	sig, sigOk, meshCaveat := p.meshSignal(ctx, sourceNode, targetNode)
	if meshCaveat != "" {
		caveats = append(caveats, meshCaveat)
	}

	effectiveCapacityMbps := capacityMbps
	if sigOk {
		effectiveCapacityMbps = capacityMbps * (1 - sig.LossPct/100)
	}
	if effectiveCapacityMbps < 0 {
		effectiveCapacityMbps = 0
	}

	utilizationMbps := 0.0
	if p.cfg.Traffic != nil {
		if v, ok := p.cfg.Traffic.MigrationTrafficMbps(ctx, sourceNode); ok {
			utilizationMbps = v
		} else {
			caveats = append(caveats, "current migration-traffic utilization on this link could not be determined; headroom may be optimistic")
		}
	} else {
		caveats = append(caveats, "no migration-traffic volume source is wired; headroom may be optimistic")
	}

	headroomMbps := effectiveCapacityMbps - utilizationMbps
	if headroomMbps < 0 {
		headroomMbps = 0
	}

	dirtyRateMbps := float64(memoryMB) * 8 * p.cfg.DirtyRateFraction
	caveats = append(caveats, fmt.Sprintf(
		"the dirty-page rate (%.0f Mbps) is a best-effort estimate — %.1f%% of the guest's configured RAM assumed dirtied per second — derived only from PVE's own guest config, not live guest instrumentation",
		dirtyRateMbps, p.cfg.DirtyRateFraction*100))

	estimatedTransferSec := -1.0
	if headroomMbps > 0 {
		estimatedTransferSec = float64(memoryMB) * 8 / headroomMbps
	}

	verdict := VerdictOK
	switch {
	case headroomMbps <= 0:
		verdict = VerdictInsufficient
		caveats = append(caveats, "no bandwidth headroom remains on this link after accounting for current migration traffic")
	case dirtyRateMbps >= headroomMbps:
		verdict = VerdictInsufficient
		caveats = append(caveats, "the estimated guest dirty-page rate would exceed the available headroom; live migration may not converge")
	case sigOk && (sig.LossPct >= severelyDegradedLossPct || sig.RttMs >= severelyDegradedRttMs):
		verdict = VerdictInsufficient
		caveats = append(caveats, fmt.Sprintf(
			"the %s link between %s and %s is severely degraded (%.1f%% loss, %.0fms RTT) — do not trust this headroom estimate for a large transfer",
			sig.Fabric, sourceNode, targetNode, sig.LossPct, sig.RttMs))
	case headroomMbps < tightHeadroomMultiplier*dirtyRateMbps:
		verdict = VerdictTight
	case sigOk && (sig.LossPct >= warnLossPct || sig.RttMs >= warnRttMs):
		verdict = VerdictTight
		caveats = append(caveats, fmt.Sprintf(
			"the %s link between %s and %s shows elevated latency/loss (%.1f%% loss, %.0fms RTT)",
			sig.Fabric, sourceNode, targetNode, sig.LossPct, sig.RttMs))
	}

	// If the guest's RAM couldn't be read, dirtyRateMbps is 0 and the switch
	// above can't have flagged a dirty-rate problem — so the machine-readable
	// verdict would otherwise report "ok" on a meaningless estimate. Downstream
	// consumers (T-1604 failure-impact sim, T-1103 scheduler) branch on this
	// field, so degrade an otherwise-clean verdict to "tight": the caveat
	// already explains why, and "ok" here would be false confidence
	// (review-T-1507).
	if !memOk && verdict == VerdictOK {
		verdict = VerdictTight
	}

	return Assessment{
		HeadroomMbps:         round2(headroomMbps),
		EstimatedTransferSec: round2(estimatedTransferSec),
		Verdict:              verdict,
		BestEffort:           true,
		Caveats:              caveats,
	}
}

// guestMemoryMB reads g's configured RAM size (MiB) from PVE's own guest
// config via GuestConfigReader — "memory" is documented as returned in a
// mix of wire types by real PVE (internal/pve.stringifyConfigValue's own
// doc comment), always normalized to a string by that client, so this
// parses it tolerantly rather than assuming a fixed representation.
func (p *Planner) guestMemoryMB(ctx context.Context, g *inventory.Guest) (int64, bool) {
	if p.cfg.GuestConfig == nil {
		return 0, false
	}
	kind := pve.GuestQemu
	if g.Type == string(pve.GuestLXC) {
		kind = pve.GuestLXC
	}
	cfg, err := p.cfg.GuestConfig.GetGuestConfig(ctx, g.Node, kind, g.VMID)
	if err != nil {
		p.cfg.Logger.Warn("migration: reading guest config for RAM size", "guest", g.GetRef().String(), "error", err)
		return 0, false
	}
	raw, ok := cfg["memory"]
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || f <= 0 {
		return 0, false
	}
	return int64(f), true
}

// meshSignal reads T-1303's mesh (when wired) for the (fromNode,toNode)
// pair — see mesh.go's selectMeshLink. Returns ok=false (with an
// explanatory caveat) whenever no reading is available, so callers derate
// nothing rather than guessing.
func (p *Planner) meshSignal(ctx context.Context, fromNode, toNode string) (meshSignal, bool, string) {
	if p.cfg.Mesh == nil {
		return meshSignal{}, false, "no latency-mesh data source is wired; loss/latency derating was not applied to this estimate"
	}
	links, err := p.cfg.Mesh.Heatmap(ctx)
	if err != nil {
		p.cfg.Logger.Warn("migration: reading latency-mesh heatmap", "error", err)
		return meshSignal{}, false, "latency-mesh data could not be read; loss/latency derating was not applied to this estimate"
	}
	sig, ok := selectMeshLink(links, fromNode, toNode)
	if !ok {
		return meshSignal{}, false, fmt.Sprintf("no latency-mesh data for the %s-%s link; loss/latency derating was not applied to this estimate", fromNode, toNode)
	}
	if sig.Reversed {
		return sig, true, fmt.Sprintf("using the reverse-direction %s-fabric reading for %s->%s (this node has no outbound probe data for that pair)", sig.Fabric, fromNode, toNode)
	}
	return sig, true, ""
}

// round2 rounds to 2 decimal places — Assessment's numeric fields are
// human-displayed Mbps/seconds, not exact machine-comparison values, so a
// stable, hand-verifiable rounding avoids float-noise in golden tests.
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
