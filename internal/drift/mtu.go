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
//     direction is "correct" cannot be inferred safely).
//   - cross-node consistency: the same-named bridge's MTU differing across
//     cluster nodes (the same "same-named bridge" grouping bridge.go
//     uses). This one *is* fixable — "MTU alignment", the second of the
//     two computable-fix families the task card names explicitly — via a
//     bridge.update op aligning every outlier node to the majority MTU.

package drift

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// checkMTUConsistency is the CheckMTUConsistency family.
func checkMTUConsistency(snap inventory.Snapshot) []Finding {
	var out []Finding
	out = append(out, mtuPathFindings(snap)...)
	out = append(out, mtuCrossNodeFindings(snap)...)
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
		bridgeMTU, bridgeOK := effectiveMTU(br.MTU, br.MTUDeclared)
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
	if mtu, ok := portMTU(snap, portRef); ok && mtu != bridgeMTU {
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
		if mtu, ok := portMTU(snap, sref); ok && mtu != bridgeMTU {
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

// mtuCrossNodeFindings compares each same-named bridge's MTU across every
// cluster node that has it, producing a fixable finding when they disagree
// (docs/features/topology.md §6's "MTU alignment" fix family).
func mtuCrossNodeFindings(snap inventory.Snapshot) []Finding {
	var out []Finding
	byName := bridgesByName(snap)
	for _, name := range sortedBridgeNames(byName) {
		byNode := byName[name]
		if len(byNode) < 2 {
			continue
		}
		present := make([]string, 0, len(byNode))
		for node := range byNode {
			present = append(present, node)
		}
		sort.Strings(present)

		mtuByNode := map[string]int{}
		votes := map[int]int{}
		for _, n := range present {
			mtu, ok := effectiveMTU(byNode[n].MTU, byNode[n].MTUDeclared)
			if !ok {
				continue
			}
			mtuByNode[n] = mtu
			votes[mtu]++
		}
		if len(votes) < 2 {
			continue
		}
		canonical := majorityInt(votes)

		var refs, dissentNodes []string
		var ops []change.Op
		for _, n := range present {
			refs = append(refs, byNode[n].GetRef().String())
			mtu, ok := mtuByNode[n]
			if !ok || mtu == canonical {
				continue
			}
			dissentNodes = append(dissentNodes, fmt.Sprintf("%s=%d", n, mtu))
			m := canonical
			ops = append(ops, change.Op{
				Type:   change.OpBridgeUpdate,
				Target: byNode[n].GetRef(),
				Params: &change.BridgeUpdateParams{MTU: &m},
			})
		}
		if len(ops) == 0 {
			continue
		}
		detail := fmt.Sprintf("bridge %s MTU has drifted across the cluster: %s (canonical %d)",
			name, strings.Join(dissentNodes, ", "), canonical)
		f := newFinding(CheckMTUConsistency, SeverityWarning, detail, present, refs)
		f = f.withFix(fmt.Sprintf("drift: align bridge %s MTU to %d", name, canonical), ops)
		out = append(out, f)
	}
	return out
}

func majorityInt(votes map[int]int) int {
	bestVal, bestVotes := 0, -1
	vals := make([]int, 0, len(votes))
	for v := range votes {
		vals = append(vals, v)
	}
	sort.Ints(vals)
	for _, v := range vals {
		if votes[v] > bestVotes {
			bestVal, bestVotes = v, votes[v]
		}
	}
	return bestVal
}
