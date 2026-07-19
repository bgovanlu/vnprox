package migration

import (
	"context"

	"github.com/bgovanlu/vnprox/internal/latmesh"
)

// MeshProvider is the subset of *latmesh.Service this package needs:
// T-1303's current heatmap. *latmesh.Service satisfies this directly — the
// same "small interface, real type satisfies it structurally, no adapter
// needed" seam internal/findings.LatMeshProvider already establishes for
// the identical method (internal/findings/health_latmesh.go).
type MeshProvider interface {
	Heatmap(ctx context.Context) ([]latmesh.LinkHeat, error)
}

// meshSignal is the congestion reading this package derives from T-1303's
// mesh for one node pair — see selectMeshLink.
type meshSignal struct {
	Fabric  latmesh.Fabric
	LossPct float64
	RttMs   float64
	// Reversed is true when only the reverse-direction sample (ToNode ==
	// fromNode, FromNode == toNode) was available — T-1303's mesh is
	// node-local/outbound-only (docs/api.md's Latency mesh section), so a
	// plan requested for a link this node has no outbound probe for still
	// gets a reading when the daemon happens to have the reverse
	// direction's own sample (e.g. it also probes back), flagged as a
	// lower-confidence substitute.
	Reversed bool
}

// selectMeshLink picks the most relevant — and, within a fabric/direction
// tier, the worst (highest loss, then highest RTT) — LinkHeat T-1303's
// mesh has for (fromNode,toNode): corosync fabric forward direction first
// (the shared-link risk this card exists to warn about — see doc.go for
// why no dedicated migration fabric is modeled), then corosync reverse,
// then guest forward, then guest reverse. Picking the worst match within a
// tier (rather than an arbitrary one) matters because a dual-stack guest
// bridge or multiple shared bridges can produce more than one LinkHeat for
// the same node pair/fabric (T-1404's per-family LinkIDs) — this package
// is a warning system, so it always surfaces the most concerning reading
// available rather than an average or an arbitrary pick. ok is false when
// none of the four tiers has any data for this pair.
func selectMeshLink(links []latmesh.LinkHeat, fromNode, toNode string) (meshSignal, bool) {
	var corosyncFwd, corosyncRev, guestFwd, guestRev *latmesh.LinkHeat

	for i := range links {
		l := &links[i]
		switch {
		case l.FromNode == fromNode && l.ToNode == toNode && l.Fabric == latmesh.FabricCorosync:
			corosyncFwd = worseOf(corosyncFwd, l)
		case l.FromNode == toNode && l.ToNode == fromNode && l.Fabric == latmesh.FabricCorosync:
			corosyncRev = worseOf(corosyncRev, l)
		case l.FromNode == fromNode && l.ToNode == toNode && l.Fabric == latmesh.FabricGuest:
			guestFwd = worseOf(guestFwd, l)
		case l.FromNode == toNode && l.ToNode == fromNode && l.Fabric == latmesh.FabricGuest:
			guestRev = worseOf(guestRev, l)
		}
	}

	switch {
	case corosyncFwd != nil:
		return meshSignal{Fabric: latmesh.FabricCorosync, LossPct: corosyncFwd.RollingLossPct, RttMs: corosyncFwd.RollingRttMs}, true
	case corosyncRev != nil:
		return meshSignal{Fabric: latmesh.FabricCorosync, LossPct: corosyncRev.RollingLossPct, RttMs: corosyncRev.RollingRttMs, Reversed: true}, true
	case guestFwd != nil:
		return meshSignal{Fabric: latmesh.FabricGuest, LossPct: guestFwd.RollingLossPct, RttMs: guestFwd.RollingRttMs}, true
	case guestRev != nil:
		return meshSignal{Fabric: latmesh.FabricGuest, LossPct: guestRev.RollingLossPct, RttMs: guestRev.RollingRttMs, Reversed: true}, true
	default:
		return meshSignal{}, false
	}
}

// worseOf returns whichever of cur/cand has the higher RollingLossPct
// (ties broken by the higher RollingRttMs) — nil is "no reading yet",
// always beaten by any real reading.
func worseOf(cur, cand *latmesh.LinkHeat) *latmesh.LinkHeat {
	if cur == nil {
		return cand
	}
	if cand.RollingLossPct > cur.RollingLossPct {
		return cand
	}
	if cand.RollingLossPct == cur.RollingLossPct && cand.RollingRttMs > cur.RollingRttMs {
		return cand
	}
	return cur
}
