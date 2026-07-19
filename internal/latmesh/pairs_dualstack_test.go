package latmesh_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/latmesh"
)

// newGraphWithAddressedBridge builds a two-node graph with one bridge
// carrying addrs (CIDR strings) on both nodes — for T-1404's per-family
// pairing tests, which need real addresses to exercise (newGraphWithBridges
// above deliberately leaves bridges address-less, covering the fallback
// path instead).
func newGraphWithAddressedBridge(t *testing.T, bridgeName string, addrsByNode map[string][]string) *inventory.Graph {
	t.Helper()
	g := inventory.NewGraph()
	var nodeEntities []inventory.Entity
	for node := range addrsByNode {
		nodeEntities = append(nodeEntities, &inventory.Node{
			Ref: inventory.Ref{Kind: inventory.KindNode, Node: node, ID: node}, Name: node, Status: "online",
		})
	}
	g.ApplyPoll(inventory.SourcePVECluster, inventory.Scope{}, nodeEntities)
	for node, addrs := range addrsByNode {
		br := &inventory.Bridge{
			Ref:  inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: bridgeName},
			Name: bridgeName, Virt: inventory.BridgeLinux, Addresses: addrs,
		}
		g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: node, Kinds: []inventory.Kind{inventory.KindBridge}}, []inventory.Entity{br})
	}
	return g
}

// TestDiscoverPairs_DualStackBridge_TwoIndependentLinkIDs is T-1404
// acceptance criterion 4's discovery half: a bridge carrying both a v4
// and a v6 address on both nodes produces two distinct LinkIDs (one per
// family), not one merged pair.
func TestDiscoverPairs_DualStackBridge_TwoIndependentLinkIDs(t *testing.T) {
	g := newGraphWithAddressedBridge(t, "vmbr0", map[string][]string{
		"pve1": {"10.20.0.1/24", "2001:db8:20::1/64"},
		"pve2": {"10.20.0.2/24", "2001:db8:20::2/64"},
	})

	pairs := latmesh.DiscoverPairs(g.Snapshot(), nil, "pve1")
	if len(pairs) != 2 {
		t.Fatalf("got %d pairs, want 2 (one v4, one v6): %+v", len(pairs), pairs)
	}

	byFamily := map[latmesh.Family]latmesh.Pair{}
	for _, p := range pairs {
		byFamily[p.Family] = p
	}
	v4, ok4 := byFamily[latmesh.FamilyV4]
	v6, ok6 := byFamily[latmesh.FamilyV6]
	if !ok4 || !ok6 {
		t.Fatalf("expected one v4 and one v6 pair, got families: %+v", pairs)
	}
	if v4.LinkID == v6.LinkID {
		t.Fatalf("v4 and v6 pairs share the same LinkID %q — must be independent", v4.LinkID)
	}
	if v4.FromAddr != "10.20.0.1" || v4.ToAddr != "10.20.0.2" {
		t.Errorf("v4 pair addresses = %+v", v4)
	}
	if v6.FromAddr != "2001:db8:20::1" || v6.ToAddr != "2001:db8:20::2" {
		t.Errorf("v6 pair addresses = %+v", v6)
	}
}

// TestDiscoverPairs_SingleFamilyBridge_UnsuffixedLabel is a regression
// guard: a bridge carrying only one family keeps the pre-T-1404 bare
// label (no "-v4"/"-v6" suffix), so an existing single-family deployment's
// LinkID never changes shape.
func TestDiscoverPairs_SingleFamilyBridge_UnsuffixedLabel(t *testing.T) {
	g := newGraphWithAddressedBridge(t, "vmbr0", map[string][]string{
		"pve1": {"10.20.0.1/24"},
		"pve2": {"10.20.0.2/24"},
	})
	pairs := latmesh.DiscoverPairs(g.Snapshot(), nil, "pve1")
	if len(pairs) != 1 || pairs[0].LinkID != "guest:vmbr0|pve1->pve2" {
		t.Fatalf("single-family bridge pairs = %+v, want exactly one bare-labeled guest:vmbr0 pair", pairs)
	}
}
