// SPDX-License-Identifier: Apache-2.0

// health_latmesh.go implements docs/features/monitoring.md §5's
// "path_latency_degraded"/"path_loss" health checks (T-1303): one finding
// per node-to-node link (internal/latmesh.LinkHeat.LinkID) whose *rolling*
// RTT/loss — not a single noisy sample — crosses a configured threshold and
// holds. Comparing the rolling figure (already a mean over
// Config.RollingWindow, computed by internal/latmesh.Service.Heatmap) is
// deliberate: latmesh's own probe tick is already low-rate (default 10s),
// so this check's hysteresis debounces *rolling-window crossings*, not raw
// per-tick pings — two independent smoothing layers for the same "don't
// flap on noise" goal, matching this package's existing convention of
// layering Engine-cycle hysteresis on top of whatever smoothing the
// producer itself already does (see checkErrorDropRate's comparable
// stance on internal/metrics' own rate computation).

package findings

import (
	"fmt"
	"sort"

	"github.com/bgovanlu/vnprox/internal/latmesh"
)

const (
	CheckPathLatencyDegraded = "path_latency_degraded"
	CheckPathLoss            = "path_loss"
)

const latMeshDocsLink = "docs/features/monitoring.md#5-health-checks"

// latMeshRiseCycles/latMeshFallCycles: a link's rolling RTT/loss must be
// over (under) threshold this many consecutive Engine cycles before the
// finding fires (clears) — the same 3-rise/2-fall window checkErrorDropRate
// uses for a comparable live-runtime-derived, continuously-noisy signal.
const (
	latMeshRiseCycles = 3
	latMeshFallCycles = 2
)

// LatMeshProvider is the subset of *latmesh.Service Engine needs: the
// current per-link current+rolling status. *latmesh.Service satisfies this
// directly via its LatMeshHeatmap method (no adapter needed — the same
// "small interface, real type satisfies it for free" seam MetricsProvider/
// CorosyncProvider already establish). Nil skips both checks entirely, same
// degradation as every other optional Config field.
type LatMeshProvider interface {
	LatMeshHeatmap() ([]latmesh.LinkHeat, error)
}

// checkPathLatencyDegraded evaluates every link's rolling RTT against
// th.LatRttWarnMs.
func checkPathLatencyDegraded(prov LatMeshProvider, db *debouncer, th HealthThresholds) []Finding {
	links, ok := latMeshLinks(prov)
	if !ok {
		return nil
	}

	var out []Finding
	live := map[string]bool{}
	for _, l := range links {
		live[l.LinkID] = true
		breach := l.RollingRttMs > th.LatRttWarnMs
		active := db.Evaluate(l.LinkID, breach, latMeshRiseCycles, latMeshFallCycles)
		if !active {
			continue
		}
		detail := fmt.Sprintf(
			"path %s -> %s over the %s fabric: rolling latency %.1fms exceeds the %.1fms threshold",
			l.FromNode, l.ToNode, l.Fabric, l.RollingRttMs, th.LatRttWarnMs)
		out = append(out, latMeshFinding(CheckPathLatencyDegraded, l, detail))
	}

	db.Prune(live)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// checkPathLoss evaluates every link's rolling loss% against
// th.LatLossWarnPct.
func checkPathLoss(prov LatMeshProvider, db *debouncer, th HealthThresholds) []Finding {
	links, ok := latMeshLinks(prov)
	if !ok {
		return nil
	}

	var out []Finding
	live := map[string]bool{}
	for _, l := range links {
		live[l.LinkID] = true
		breach := l.RollingLossPct > th.LatLossWarnPct
		active := db.Evaluate(l.LinkID, breach, latMeshRiseCycles, latMeshFallCycles)
		if !active {
			continue
		}
		detail := fmt.Sprintf(
			"path %s -> %s over the %s fabric: rolling loss %.1f%% exceeds the %.1f%% threshold",
			l.FromNode, l.ToNode, l.Fabric, l.RollingLossPct, th.LatLossWarnPct)
		out = append(out, latMeshFinding(CheckPathLoss, l, detail))
	}

	db.Prune(live)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// latMeshLinks fetches prov's current heatmap, tolerating a nil provider or
// a read error by reporting ok=false — detection-only, quietly-absent
// degradation matching every other optional producer input in this
// package.
func latMeshLinks(prov LatMeshProvider) ([]latmesh.LinkHeat, bool) {
	if prov == nil {
		return nil, false
	}
	links, err := prov.LatMeshHeatmap()
	if err != nil {
		return nil, false
	}
	sort.Slice(links, func(i, j int) bool { return links[i].LinkID < links[j].LinkID })
	return links, true
}

// latMeshFinding builds a link-keyed Finding directly (not via
// newHealthFinding, which derives its key from Refs/Nodes — a latency-mesh
// link has no inventory.Ref of its own, and two different fabrics can
// legitimately share the same (fromNode,toNode) pair, e.g. a corosync ring
// AND a shared bridge between the same two nodes, so keying on Nodes alone
// would collide them onto one id — the same reasoning checkCorosyncLinkDegraded's
// doc comment already gives for its own (node,ring) composite key).
func latMeshFinding(check string, l latmesh.LinkHeat, detail string) Finding {
	return Finding{
		ID:       "health:" + check + "|" + l.LinkID,
		Source:   SourceHealth,
		Check:    check,
		Severity: SeverityWarning,
		Detail:   detail,
		Nodes:    sortedUnique([]string{l.FromNode, l.ToNode}),
		DocsLink: latMeshDocsLink,
	}
}
