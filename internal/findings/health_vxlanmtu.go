// health_vxlanmtu.go implements docs/features/monitoring.md §5's
// "vxlan_underlay_mtu" health check (T-803).
//
// Scoping note (from the task card, kept here for the next reader): plain
// L2 NIC->bond->bridge MTU mismatches — docs/roadmap-next.md's "path-MTU
// asymmetry" item — already ship via drift.CheckMTUConsistency (both
// within-node and, since T-801, cross-node pre-apply) and are NOT
// reimplemented here; doing so would be exactly the "two names, one
// problem" failure T-801 exists to prevent. This check instead promotes the
// one genuinely uncovered MTU gap: internal/change/validate_advisory.go's
// checkVxlanMTU only runs at changeset-validate time against an *assumed*
// default underlay MTU (change.underlayMTU, 1500) — nothing re-checks it
// continuously, so a physical underlay MTU that degrades *after* apply (no
// changeset involved — a switch port renegotiates, a NIC gets
// reconfigured outside vnprox) goes undetected. This check reuses the exact
// same encapsulation-overhead constant (change.VxlanOverhead) but compares
// against the *observed* underlay path MTU (real PhysNic MTU from
// inventory) instead of the assumed default.

package findings

import (
	"fmt"
	"sort"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/xnode"
)

const CheckVxlanUnderlayMTU = "vxlan_underlay_mtu"

const vxlanUnderlayMTUDocsLink = "docs/features/monitoring.md#5-health-checks"

// vxlanUnderlayMTURiseCycles/FallCycles: light hysteresis against a single
// transient MTU misreport (e.g. a NIC read racing a reconfiguration),
// matching bond_slave_down/bridge_no_carrier's own window for a comparable
// live-runtime-derived fact.
const (
	vxlanUnderlayMTURiseCycles = 2
	vxlanUnderlayMTUFallCycles = 2
)

// checkVxlanUnderlayMTU evaluates every vxlan/evpn SdnZone with an explicit
// MTU (mtu == 0 is not flagged, mirroring checkVxlanMTU's own skip — PVE
// applies its own sane default) against each member node's observed
// underlay path MTU: the minimum effective MTU among that node's physical
// NICs, the strictest real constraint on what can actually traverse the
// wire off that node (a routing-table walk to the zone's specific peer
// addresses is out of scope — see this task's completion report).
func checkVxlanUnderlayMTU(snap inventory.Snapshot, db *debouncer) []Finding {
	var out []Finding
	live := map[string]bool{}

	for _, e := range snap.All() {
		zone, ok := e.(*inventory.SdnZone)
		if !ok {
			continue
		}
		if zone.Type != "vxlan" && zone.Type != "evpn" {
			continue
		}
		if zone.MTU == 0 {
			continue
		}

		for _, node := range sortedUnique(zone.Nodes) {
			underlay, ok := observedUnderlayMTU(snap, node)
			if !ok {
				continue // no NIC MTU data observed for this node yet
			}

			key := zone.GetRef().String() + "|" + node
			live[key] = true
			breach := zone.MTU+change.VxlanOverhead > underlay
			active := db.Evaluate(key, breach, vxlanUnderlayMTURiseCycles, vxlanUnderlayMTUFallCycles)
			if !active {
				continue
			}

			detail := fmt.Sprintf(
				"SDN zone %s (%s) on node %s: configured mtu %d leaves no headroom for VXLAN's %d-byte encapsulation overhead over the observed %d-byte underlay path MTU — encapsulated traffic may be fragmented or dropped",
				zone.ID, zone.Type, node, zone.MTU, change.VxlanOverhead, underlay)
			f := newHealthFinding(CheckVxlanUnderlayMTU, SeverityWarning, detail, []string{node}, []string{zone.GetRef().String()})
			f.DocsLink = vxlanUnderlayMTUDocsLink
			out = append(out, f)
		}
	}

	db.Prune(live)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// observedUnderlayMTU returns the minimum effective MTU (xnode.EffectiveMTU:
// runtime, falling back to declared) among node's physical NICs, or
// ok=false when none report an MTU yet.
func observedUnderlayMTU(snap inventory.Snapshot, node string) (mtu int, ok bool) {
	found := false
	min := 0
	for _, e := range snap.All() {
		nic, isNic := e.(*inventory.PhysNic)
		if !isNic || nic.GetRef().Node != node {
			continue
		}
		m, mok := xnode.EffectiveMTU(nic.MTU, nic.MTUDeclared)
		if !mok {
			continue
		}
		if !found || m < min {
			min, found = m, true
		}
	}
	return min, found
}
