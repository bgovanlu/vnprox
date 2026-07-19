package latmesh

import (
	"fmt"
	"net/netip"
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
				Family: addrFamily(fromAddr),
			})
		}
	}
	return out
}

// discoverGuestPairs pairs localNode with every other node that shares a
// same-named bridge with it (internal/xnode.BridgesByName's existing
// cross-node grouping, reused rather than re-walking inventory.Snapshot's
// entities a second time) — one Pair *per address family* the bridge
// carries on both ends (T-1404: "v4 and v6 probes run independently on any
// dual-stack-capable segment"), not one Pair per (bridge name, other
// node) regardless of family as before this task.
//
// Family lives in Label (appended as "<bridge>-v4"/"<bridge>-v6") rather
// than as a new ComputeLinkID parameter: a bridge carrying only one
// family (the overwhelmingly common case, pre-T-1404) gets a bare
// "<bridge>" label exactly as before — LinkID format is unchanged for a
// single-family link, so no existing store row / API caller's stable-ID
// assumption breaks. A bridge carrying both families gets two distinct,
// independently-probed, independently-retained LinkIDs.
func discoverGuestPairs(snap inventory.Snapshot, localNode string) []Pair {
	byName := xnode.BridgesByName(snap)

	var out []Pair
	for _, name := range xnode.SortedBridgeNames(byName) {
		byNode := byName[name]
		localBr, ok := byNode[localNode]
		if !ok {
			continue // this bridge doesn't exist on the local node at all
		}
		localV4, localV6 := addrsByFamily(localBr.Addresses)
		// dualStack: the local bridge itself carries both families —
		// only then are the two families' labels suffixed ("-v4"/"-v6")
		// to stay distinct; a single-family bridge keeps the bare
		// pre-T-1404 label (at most one Pair is emitted for it anyway).
		dualStack := localV4 != "" && localV6 != ""

		var others []string
		for node := range byNode {
			if node != localNode {
				others = append(others, node)
			}
		}
		sort.Strings(others)

		for _, other := range others {
			otherV4, otherV6 := addrsByFamily(byNode[other].Addresses)
			matched := 0
			if localV4 != "" && otherV4 != "" {
				matched++
				out = append(out, Pair{
					Fabric: FabricGuest, Label: guestFamilyLabel(name, dualStack, FamilyV4),
					FromNode: localNode, ToNode: other,
					FromAddr: localV4, ToAddr: otherV4, Family: FamilyV4,
				})
			}
			if localV6 != "" && otherV6 != "" {
				matched++
				out = append(out, Pair{
					Fabric: FabricGuest, Label: guestFamilyLabel(name, dualStack, FamilyV6),
					FromNode: localNode, ToNode: other,
					FromAddr: localV6, ToAddr: otherV6, Family: FamilyV6,
				})
			}
			// Fallback (pre-T-1404 behavior, preserved): a bridge with no
			// address known on either side at all (the common
			// fixture/early-poll case — Pair's own doc comment already
			// documents "either may be empty ... dial ToNode by name") or
			// with only mismatched families across the two nodes still
			// gets exactly one address-less pair under the bare label, so
			// this fabric's mesh coverage never silently shrinks to zero
			// just because address data isn't available yet.
			if matched == 0 {
				out = append(out, Pair{
					Fabric: FabricGuest, Label: name,
					FromNode: localNode, ToNode: other,
				})
			}
		}
	}
	return out
}

// guestFamilyLabel builds discoverGuestPairs' Label: bare bridge name when
// the local bridge is single-family (dualStack == false — the pre-T-1404
// label shape, unchanged), else the family-suffixed form
// ("<bridge>-v4"/"<bridge>-v6") that keeps a dual-stack bridge's two links
// distinct.
func guestFamilyLabel(bridgeName string, dualStack bool, family Family) string {
	if !dualStack {
		return bridgeName
	}
	return bridgeName + "-" + string(family)
}

// addrFamily reports addr's own family ("" when it doesn't parse as an
// IP at all — a corosync ring address is always a plain IP, never a
// hostname, but this stays defensive rather than guessing).
func addrFamily(addr string) Family {
	a, err := netip.ParseAddr(addr)
	if err != nil {
		return ""
	}
	if a.Is4() {
		return FamilyV4
	}
	return FamilyV6
}

// addrsByFamily returns the first v4 and first v6 address (host part only,
// CIDR prefix length stripped) among addrs — LinkState.Addresses' own CIDR
// form ("10.0.0.1/24", "2001:db8::1/64"). An address that doesn't parse is
// skipped, never guessed at.
func addrsByFamily(addrs []string) (v4, v6 string) {
	for _, a := range addrs {
		pfx, err := netip.ParsePrefix(a)
		if err != nil {
			if addr, err2 := netip.ParseAddr(a); err2 == nil {
				pfx = netip.PrefixFrom(addr, addr.BitLen())
			} else {
				continue
			}
		}
		host := pfx.Addr().String()
		if pfx.Addr().Is4() {
			if v4 == "" {
				v4 = host
			}
		} else if v6 == "" {
			v6 = host
		}
	}
	return v4, v6
}
