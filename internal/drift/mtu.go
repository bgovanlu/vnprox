// SPDX-License-Identifier: Apache-2.0

// mtu.go implements docs/features/topology.md §6's second check family:
// "MTU consistency along each L2 path (NIC->bond->bridge->VNet)". Two
// sub-checks share the CheckMTUConsistency name:
//
//   - within-node path consistency: a bridge's own MTU vs. the MTU of each
//     of its ports (physical NICs directly, or transitively through an
//     enslaving bond) — jumbo frames configured on a bridge but not its
//     uplink NIC silently fragment/drop, a classic misconfiguration this
//     catches. Detection only (no computable fix: raising the port's MTU
//     vs. lowering the bridge's MTU are both plausible intents, so which
//     direction is "correct" cannot be inferred safely). This half stays in
//     this package — it is not a cross-node comparison and has no second
//     consumer to share with.
//   - cross-node consistency: the same-named bridge's MTU differing across
//     cluster nodes. This one *is* fixable ("MTU alignment") and is the
//     comparison shared with internal/change's T-801 validator class via
//     internal/xnode (CrossNodeMTU) — this file only adapts its result into
//     a drift Finding.

package drift

import (
	"fmt"
	"sort"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/xnode"
)

// checkMTUConsistency is the CheckMTUConsistency family.
func checkMTUConsistency(snap inventory.Snapshot) []Finding {
	var out []Finding
	out = append(out, mtuPathFindings(snap)...)
	out = append(out, driftFindings(xnode.CrossNodeMTU(snap))...)
	return out
}

// mtuPathFindings walks every bridge's resolved ports (inventory.linkAll
// already resolved Bridge.Ports preferring live/netlink membership — see
// link.go) and flags a bridge/port MTU mismatch, recursing one level
// through an enslaving bond to its slaves for the NIC->bond->bridge chain.
func mtuPathFindings(snap inventory.Snapshot) []Finding {
	var out []Finding
	for _, e := range snap.All() {
		br, ok := e.(*inventory.Bridge)
		if !ok {
			continue
		}
		bridgeMTU, bridgeOK := xnode.EffectiveMTU(br.MTU, br.MTUDeclared)
		if !bridgeOK {
			continue
		}
		for _, portRef := range br.Ports {
			out = append(out, pathMismatches(snap, br, bridgeMTU, portRef)...)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// pathMismatches compares bridgeMTU against portRef's own MTU (and, if
// portRef is a bond, each of the bond's slaves' MTU in turn).
func pathMismatches(snap inventory.Snapshot, br *inventory.Bridge, bridgeMTU int, portRef inventory.Ref) []Finding {
	var out []Finding
	if mtu, ok := xnode.PortMTU(snap, portRef); ok && mtu != bridgeMTU {
		out = append(out, pathMismatchFinding(br, bridgeMTU, portRef, mtu))
	}
	bond, ok := snap.Get(portRef)
	if !ok {
		return out
	}
	b, ok := bond.(*inventory.Bond)
	if !ok {
		return out
	}
	slaves := b.Slaves
	if len(slaves) == 0 {
		slaves = b.DeclaredSlaves
	}
	for _, sname := range slaves {
		sref := inventory.Ref{Kind: inventory.KindPhysNic, Node: portRef.Node, ID: sname}
		if mtu, ok := xnode.PortMTU(snap, sref); ok && mtu != bridgeMTU {
			out = append(out, pathMismatchFinding(br, bridgeMTU, sref, mtu))
		}
	}
	return out
}

func pathMismatchFinding(br *inventory.Bridge, bridgeMTU int, portRef inventory.Ref, portMTU int) Finding {
	detail := fmt.Sprintf("bridge %s on node %s has MTU %d but its path member %s has MTU %d — jumbo frames may fragment or drop along this L2 path",
		br.Name, br.GetRef().Node, bridgeMTU, portRef.ID, portMTU)
	return newFinding(CheckMTUConsistency, SeverityWarning, detail,
		[]string{br.GetRef().Node}, []string{br.GetRef().String(), portRef.String()})
}
