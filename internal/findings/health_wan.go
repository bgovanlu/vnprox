// health_wan.go implements T-1405's wan_degraded health check (source
// "wan"): one node's one configured reference target (an uplink→host pair)
// whose *rolling* loss — not a single noisy probe — crosses
// HealthThresholds.WanLossWarnPct and holds, hysteresis-debounced the same
// way health_latmesh.go's path_latency_degraded/path_loss checks debounce
// their own rolling-window crossings (AC1: "a single missed probe doesn't
// fire"). This is deliberately one finding covering both the "elevated
// loss" and "fully unreachable" cases (100% loss is just the loss-percent
// axis's own extreme, not a separate check) — the card names a single
// finding, wan_degraded, not a split pair the way T-1303's LAN mesh check
// is split into two.

package findings

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/latmesh"
)

const CheckWanDegraded = "wan_degraded"

const wanDocsLink = "docs/api.md#wan--upstream-health"

// wanRiseCycles/wanFallCycles: the same 3-rise/2-fall window
// checkPathLatencyDegraded/checkPathLoss use for a comparable live-runtime-
// derived, continuously-noisy signal.
const (
	wanRiseCycles = 3
	wanFallCycles = 2
)

// WanProvider is the findings engine's seam onto internal/wan's continuous
// probe of operator-configured external reference targets
// (*wan.Service.WanHeatmap satisfies this directly, the same "small
// interface, real type satisfies it for free" seam LatMeshProvider/
// WGProvider already establish). Nil skips the check entirely, the same
// degradation as every other optional producer in this package.
type WanProvider interface {
	WanHeatmap() ([]latmesh.LinkHeat, error)
}

// checkWanDegraded evaluates every configured target's rolling loss against
// th.WanLossWarnPct.
func checkWanDegraded(prov WanProvider, db *debouncer, th HealthThresholds) []Finding {
	links, ok := wanLinks(prov)
	if !ok {
		return nil
	}

	var out []Finding
	live := map[string]bool{}
	for _, l := range links {
		live[l.LinkID] = true
		breach := l.RollingLossPct > th.WanLossWarnPct
		active := db.Evaluate(l.LinkID, breach, wanRiseCycles, wanFallCycles)
		if !active {
			continue
		}
		uplink := wanUplinkFromLinkID(l.LinkID)
		verb := "elevated loss toward"
		if l.RollingLossPct >= 100 {
			verb = "cannot reach"
		}
		detail := fmt.Sprintf(
			"node %s uplink %s %s reference target %s: rolling loss %.1f%% exceeds the %.1f%% threshold — likely an ISP/upstream issue, not the cluster",
			l.FromNode, uplink, verb, l.ToNode, l.RollingLossPct, th.WanLossWarnPct)
		out = append(out, wanFinding(l, uplink, detail))
	}

	db.Prune(live)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// wanLinks fetches prov's current heatmap, tolerating a nil provider or a
// read error by reporting ok=false — detection-only, quietly-absent
// degradation matching every other optional producer input in this
// package.
func wanLinks(prov WanProvider) ([]latmesh.LinkHeat, bool) {
	if prov == nil {
		return nil, false
	}
	links, err := prov.WanHeatmap()
	if err != nil {
		return nil, false
	}
	sort.Slice(links, func(i, j int) bool { return links[i].LinkID < links[j].LinkID })
	return links, true
}

// wanFinding builds a link-keyed Finding directly (not via
// newHealthFinding, mirroring latMeshFinding's own reasoning: a WAN link
// has no inventory.Ref of its own).
func wanFinding(l latmesh.LinkHeat, uplink, detail string) Finding {
	return Finding{
		ID:       "wan:" + CheckWanDegraded + "|" + l.LinkID,
		Source:   SourceWan,
		Check:    CheckWanDegraded,
		Severity: SeverityWarning,
		Detail:   detail,
		Nodes:    sortedUnique([]string{l.FromNode}),
		DocsLink: wanDocsLink,
	}
}

// wanUplinkFromLinkID extracts a WAN Pair's uplink label back out of its
// LinkID ("wan:<uplink>|<fromNode>-><toNode>") — the same three-line parser
// internal/wan.Service and internal/store.WanProbeSampleRepo each keep
// their own unexported copy of (a pure string operation, not worth a shared
// cross-package export).
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
