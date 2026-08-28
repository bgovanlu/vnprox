// SPDX-License-Identifier: Apache-2.0

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

// MTUProvider is the subset of *mtuprobe.Service Engine needs (T-1306): a
// live, measured (DF-probe-verified) underlay path MTU per node, when the
// prober has reached it. *mtuprobe.Service satisfies this directly via its
// MeasuredUnderlayMTU method (no adapter needed — the same "small
// interface, real type satisfies it for free" seam LatMeshProvider/
// CorosyncProvider already establish). Nil (or a node with no fresh
// reading) falls back to checkVxlanUnderlayMTU's original T-803 behavior
// (observedUnderlayMTU's local NIC read) — never a regression, matching
// every other optional Config field's degraded-mode convention.
type MTUProvider interface {
	MeasuredUnderlayMTU(node string) (mtu int, ok bool)
}

// checkVxlanUnderlayMTU evaluates every vxlan/evpn SdnZone with an explicit
// MTU (mtu == 0 is not flagged, mirroring checkVxlanMTU's own skip — PVE
// applies its own sane default) against each member node's underlay path
// MTU. mtuProv (T-1306's internal/mtuprobe.Service, via the MTUProvider
// seam) supplies a *measured* reading — a live, end-to-end DF-probe result
// for a path this node has actually verified — when one exists; that is
// strictly tighter/more trustworthy than observedUnderlayMTU's local NIC
// MTU read, since a NIC can report a healthy MTU while some hop along the
// actual path still clamps it lower. A nil mtuProv or a node mtuProv has no
// fresh reading for falls back to observedUnderlayMTU exactly as before —
// never a regression for paths the prober hasn't reached yet (this task's
// card, AC3).
func checkVxlanUnderlayMTU(snap inventory.Snapshot, db *debouncer, mtuProv MTUProvider) []Finding {
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
			underlay, source, ok := underlayMTUFor(mtuProv, snap, node)
			if !ok {
				continue // no NIC MTU data observed and no probe reading for this node yet
			}

			key := zone.GetRef().String() + "|" + node
			live[key] = true
			breach := zone.MTU+change.VxlanOverhead > underlay
			active := db.Evaluate(key, breach, vxlanUnderlayMTURiseCycles, vxlanUnderlayMTUFallCycles)
			if !active {
				continue
			}

			detail := fmt.Sprintf(
				"SDN zone %s (%s) on node %s: configured mtu %d leaves no headroom for VXLAN's %d-byte encapsulation overhead over the %s %d-byte underlay path MTU — encapsulated traffic may be fragmented or dropped",
				zone.ID, zone.Type, node, zone.MTU, change.VxlanOverhead, source, underlay)
			f := newHealthFinding(CheckVxlanUnderlayMTU, SeverityWarning, detail, []string{node}, []string{zone.GetRef().String()})
			f.DocsLink = vxlanUnderlayMTUDocsLink
			out = append(out, f)
		}
	}

	db.Prune(live)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// underlayMTUFor resolves node's underlay path MTU: a fresh T-1306 measured
// reading when mtuProv has one (source "measured", the tightened branch),
// else observedUnderlayMTU's local-NIC-read fallback (source "observed",
// this check's original T-803 behavior) — the config-only evaluation stays
// the fallback exactly where no probe result exists yet.
func underlayMTUFor(mtuProv MTUProvider, snap inventory.Snapshot, node string) (mtu int, source string, ok bool) {
	if mtuProv != nil {
		if m, mok := mtuProv.MeasuredUnderlayMTU(node); mok {
			return m, "measured", true
		}
	}
	m, ok := observedUnderlayMTU(snap, node)
	return m, "observed", ok
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
