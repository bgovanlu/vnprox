package migration

import (
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/xnode"
)

// linkCapacity is one candidate shared-bridge capacity reading between two
// nodes — see resolveLinkCapacityMbps.
type linkCapacity struct {
	BridgeName string
	Mbps       float64
}

// resolveLinkCapacityMbps finds the physical capacity of the
// highest-capacity bridge fromNode and toNode carry in common
// (internal/xnode.BridgesByName, the exact shared-bridge grouping T-1303's
// own guest-fabric Discoverer already uses to find shared L2 segments) —
// see doc.go's "The migration network — a proxy, not a live PVE reader"
// section for why this is the best available proxy rather than a
// dedicated migration-network reader. A bridge's own capacity is the sum
// of its member PhysNic/Bond SpeedMbps on that node; the pair's capacity
// is the lesser of the two nodes' figures (the bottleneck end). Among
// every shared bridge with a resolvable reading on both ends, the
// highest-capacity one is returned (the most favorable real path an
// operator/PVE would plausibly route migration traffic over). ok is false
// when no shared bridge with a resolvable member-NIC speed on both nodes
// exists for this pair.
func resolveLinkCapacityMbps(snap inventory.Snapshot, fromNode, toNode string) (linkCapacity, bool) {
	byName := xnode.BridgesByName(snap)

	var best linkCapacity
	found := false
	for _, name := range xnode.SortedBridgeNames(byName) {
		byNode := byName[name]
		fromBr, ok := byNode[fromNode]
		if !ok {
			continue
		}
		toBr, ok := byNode[toNode]
		if !ok {
			continue
		}
		fromCap, ok := bridgeCapacityMbps(snap, fromNode, fromBr)
		if !ok {
			continue
		}
		toCap, ok := bridgeCapacityMbps(snap, toNode, toBr)
		if !ok {
			continue
		}
		pairCap := fromCap
		if toCap < pairCap {
			pairCap = toCap
		}
		if !found || pairCap > best.Mbps {
			best = linkCapacity{BridgeName: name, Mbps: pairCap}
			found = true
		}
	}
	return best, found
}

// bridgeCapacityMbps sums the resolvable member-NIC speed of br's ports on
// node. ok is false when none of br's ports resolve to a PhysNic/Bond with
// a known speed (an SDN vnet port, an unresolved ref, or a NIC that has
// never reported a link speed).
func bridgeCapacityMbps(snap inventory.Snapshot, node string, br *inventory.Bridge) (float64, bool) {
	var total float64
	any := false
	for _, port := range br.Ports {
		if mbps, ok := portCapacityMbps(snap, node, port); ok {
			total += mbps
			any = true
		}
	}
	return total, any
}

// portCapacityMbps resolves one bridge port ref to its own member-NIC
// speed: a PhysNic's own SpeedMbps directly, or a Bond's summed slave
// speed (bondCapacityMbps). Any other referenced kind (a VLAN
// sub-interface, an SDN vnet) reports ok=false — this package only claims
// a capacity figure it can trace to real physical NIC hardware.
func portCapacityMbps(snap inventory.Snapshot, node string, ref inventory.Ref) (float64, bool) {
	e, ok := snap.Get(ref)
	if !ok {
		return 0, false
	}
	switch v := e.(type) {
	case *inventory.PhysNic:
		if v.SpeedMbps > 0 {
			return float64(v.SpeedMbps), true
		}
	case *inventory.Bond:
		return bondCapacityMbps(snap, node, v)
	}
	return 0, false
}

// bondCapacityMbps sums the SpeedMbps of b's own slave PhysNics on node,
// matched by interface name (Bond.Slaves carries plain names, not Refs —
// the same shape internal/topology's own bond-slave badge logic reads).
// ok is false when none of the named slaves resolve to a PhysNic with a
// known speed.
func bondCapacityMbps(snap inventory.Snapshot, node string, b *inventory.Bond) (float64, bool) {
	if len(b.Slaves) == 0 {
		return 0, false
	}
	slaveSet := make(map[string]bool, len(b.Slaves))
	for _, s := range b.Slaves {
		slaveSet[s] = true
	}

	var total float64
	any := false
	for _, e := range snap.All() {
		nic, ok := e.(*inventory.PhysNic)
		if !ok || nic.GetRef().Node != node || !slaveSet[nic.Name] {
			continue
		}
		if nic.SpeedMbps > 0 {
			total += float64(nic.SpeedMbps)
			any = true
		}
	}
	return total, any
}
