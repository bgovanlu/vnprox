package latmesh

import (
	"fmt"
	"sort"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/xnode"
)

// Discoverer produces the current set of pairs a node's Scheduler should
// probe on this tick — re-invoked every tick (not cached) so cluster
// membership/bridge changes are picked up without a daemon restart.
type Discoverer interface {
	Pairs() []Pair
}

// DiscovererFunc adapts a plain function to Discoverer, mirroring the
// codebase's other func-to-interface adapter shims (e.g.
// http.HandlerFunc).
type DiscovererFunc func() []Pair

func (f DiscovererFunc) Pairs() []Pair { return f() }

// DiscoverPairs computes localNode's own outbound probe pairs from the
// current inventory snapshot (guest fabric: shared bridge names) and
// corosync config (corosync fabric: shared ring addresses) — see doc.go's
// "Fabric discovery scope" and "Cluster scope" notes for why exactly these
// two fabrics and exactly this one direction. Deterministically ordered
// (sorted by LinkID) so repeated calls against unchanged input always
// produce byte-identical output, and so a table test can assert an exact
// expected slice rather than a set.
func DiscoverPairs(snap inventory.Snapshot, coro *host.CorosyncConfig, localNode string) []Pair {
	var out []Pair
	out = append(out, discoverCorosyncPairs(coro, localNode)...)
	out = append(out, discoverGuestPairs(snap, localNode)...)

	for i := range out {
		out[i].LinkID = ComputeLinkID(out[i].Fabric, out[i].Label, out[i].FromNode, out[i].ToNode)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LinkID < out[j].LinkID })
	return out
}

// discoverCorosyncPairs pairs localNode with every other node sharing each
// configured corosync ring index (ring0, ring1, ...) — a node missing a
// given ring (e.g. a single-ring cluster's absent ring1) simply isn't
// paired on that ring, never an error.
func discoverCorosyncPairs(coro *host.CorosyncConfig, localNode string) []Pair {
	if coro == nil {
		return nil
	}
	local, ok := coro.NodeByName(localNode)
	if !ok {
		return nil
	}

	var out []Pair
	for ring, fromAddr := range local.RingAddrs {
		if fromAddr == "" {
			continue
		}
		var peers []host.CorosyncNode
		for _, n := range coro.Nodes {
			if n.Name == localNode {
				continue
			}
			if ring < len(n.RingAddrs) && n.RingAddrs[ring] != "" {
				peers = append(peers, n)
			}
		}
		sort.Slice(peers, func(i, j int) bool { return peers[i].Name < peers[j].Name })
		for _, peer := range peers {
			out = append(out, Pair{
				Fabric: FabricCorosync, Label: fmt.Sprintf("ring%d", ring),
				FromNode: localNode, ToNode: peer.Name,
				FromAddr: fromAddr, ToAddr: peer.RingAddrs[ring],
			})
		}
	}
	return out
}

// discoverGuestPairs pairs localNode with every other node that shares a
// same-named bridge with it (internal/xnode.BridgesByName's existing
// cross-node grouping, reused rather than re-walking inventory.Snapshot's
// entities a second time) — one Pair per (bridge name, other node).
func discoverGuestPairs(snap inventory.Snapshot, localNode string) []Pair {
	byName := xnode.BridgesByName(snap)

	var out []Pair
	for _, name := range xnode.SortedBridgeNames(byName) {
		byNode := byName[name]
		localBr, ok := byNode[localNode]
		if !ok {
			continue // this bridge doesn't exist on the local node at all
		}

		var others []string
		for node := range byNode {
			if node != localNode {
				others = append(others, node)
			}
		}
		sort.Strings(others)

		for _, other := range others {
			out = append(out, Pair{
				Fabric: FabricGuest, Label: name,
				FromNode: localNode, ToNode: other,
				FromAddr: firstAddr(localBr.Addresses), ToAddr: firstAddr(byNode[other].Addresses),
			})
		}
	}
	return out
}

func firstAddr(addrs []string) string {
	if len(addrs) == 0 {
		return ""
	}
	return addrs[0]
}
