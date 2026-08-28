// SPDX-License-Identifier: Apache-2.0

package latmesh_test

import (
	"reflect"
	"testing"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/latmesh"
)

func newGraphWithBridges(bridges map[string][]string) *inventory.Graph {
	g := inventory.NewGraph()
	var nodeNames []string
	seen := map[string]bool{}
	for _, nodes := range bridges {
		for _, n := range nodes {
			if !seen[n] {
				seen[n] = true
				nodeNames = append(nodeNames, n)
			}
		}
	}
	var nodeEntities []inventory.Entity
	for _, n := range nodeNames {
		nodeEntities = append(nodeEntities, &inventory.Node{
			Ref: inventory.Ref{Kind: inventory.KindNode, Node: n, ID: n}, Name: n, Status: "online",
		})
	}
	g.ApplyPoll(inventory.SourcePVECluster, inventory.Scope{}, nodeEntities)

	for name, nodes := range bridges {
		for _, node := range nodes {
			br := &inventory.Bridge{
				Ref:  inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: name},
				Name: name, Virt: inventory.BridgeLinux,
			}
			g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: node, Kinds: []inventory.Kind{inventory.KindBridge}}, []inventory.Entity{br})
		}
	}
	return g
}

// threeNodeVlanCorosync mirrors the three-node-vlan fixture's cluster shape
// (pve1/pve2/pve3, one corosync ring) closely enough for this package's
// pure pairing logic — the full pvemock fixture carries no latency-mesh-
// relevant data this test needs beyond node names and ring addresses.
func threeNodeVlanCorosync() *host.CorosyncConfig {
	return &host.CorosyncConfig{Nodes: []host.CorosyncNode{
		{Name: "pve1", NodeID: 1, RingAddrs: []string{"10.10.0.11"}},
		{Name: "pve2", NodeID: 2, RingAddrs: []string{"10.10.0.12"}},
		{Name: "pve3", NodeID: 3, RingAddrs: []string{"10.10.0.13"}},
	}}
}

// TestDiscoverPairs_ThreeNodeVlan: AC1's "against three-node-vlan, probes
// run ... across every shared fabric pair; no probe pair is duplicated or
// skipped" — a table test asserting the exact expected pair set for
// localNode "pve1" sharing one corosync ring and one bridge (vmbr0) with
// both other nodes.
func TestDiscoverPairs_ThreeNodeVlan(t *testing.T) {
	g := newGraphWithBridges(map[string][]string{"vmbr0": {"pve1", "pve2", "pve3"}})
	coro := threeNodeVlanCorosync()

	pairs := latmesh.DiscoverPairs(g.Snapshot(), coro, "pve1")

	wantLinkIDs := []string{
		"corosync:ring0|pve1->pve2",
		"corosync:ring0|pve1->pve3",
		"guest:vmbr0|pve1->pve2",
		"guest:vmbr0|pve1->pve3",
	}
	gotLinkIDs := make([]string, len(pairs))
	for i, p := range pairs {
		gotLinkIDs[i] = p.LinkID
	}
	if !reflect.DeepEqual(gotLinkIDs, wantLinkIDs) {
		t.Fatalf("DiscoverPairs link IDs = %v, want %v", gotLinkIDs, wantLinkIDs)
	}

	// No pair ever names localNode as its own ToNode, and FromNode is
	// always localNode (this node's own outbound-only scope, doc.go).
	for _, p := range pairs {
		if p.FromNode != "pve1" {
			t.Errorf("pair %s: FromNode = %q, want pve1", p.LinkID, p.FromNode)
		}
		if p.ToNode == "pve1" {
			t.Errorf("pair %s: ToNode is localNode itself", p.LinkID)
		}
	}
}

// TestDiscoverPairs_Deterministic: repeated calls against unchanged input
// produce byte-identical output (no duplicate/skip nondeterminism across
// calls, the other half of AC1's requirement, exercised the way
// Scheduler.Tick would call this every probe interval).
func TestDiscoverPairs_Deterministic(t *testing.T) {
	g := newGraphWithBridges(map[string][]string{"vmbr0": {"pve1", "pve2", "pve3"}})
	coro := threeNodeVlanCorosync()

	first := latmesh.DiscoverPairs(g.Snapshot(), coro, "pve1")
	for i := 0; i < 5; i++ {
		again := latmesh.DiscoverPairs(g.Snapshot(), coro, "pve1")
		if !reflect.DeepEqual(first, again) {
			t.Fatalf("call %d: DiscoverPairs is not deterministic: %+v vs %+v", i, again, first)
		}
	}
}

// TestDiscoverPairs_PartialRing: a node missing a ring index it's not
// configured for is simply not paired on that ring — never an error, never
// a skipped/duplicated pairing on the rings that DO line up.
func TestDiscoverPairs_PartialRing(t *testing.T) {
	coro := &host.CorosyncConfig{Nodes: []host.CorosyncNode{
		{Name: "pve1", RingAddrs: []string{"10.10.0.11", "10.10.1.11"}},
		{Name: "pve2", RingAddrs: []string{"10.10.0.12", "10.10.1.12"}},
		{Name: "pve3", RingAddrs: []string{"10.10.0.13"}}, // no ring1
	}}
	g := inventory.NewGraph() // no bridges: corosync-only fabric for this test

	pairs := latmesh.DiscoverPairs(g.Snapshot(), coro, "pve1")
	wantLinkIDs := []string{
		"corosync:ring0|pve1->pve2",
		"corosync:ring0|pve1->pve3",
		"corosync:ring1|pve1->pve2",
	}
	gotLinkIDs := make([]string, len(pairs))
	for i, p := range pairs {
		gotLinkIDs[i] = p.LinkID
	}
	if !reflect.DeepEqual(gotLinkIDs, wantLinkIDs) {
		t.Fatalf("DiscoverPairs link IDs = %v, want %v", gotLinkIDs, wantLinkIDs)
	}
}

// TestDiscoverPairs_NilCorosync: a node with no corosync config at all
// (not yet clustered) still gets its guest-fabric pairs — corosync being
// absent never blocks the other fabric.
func TestDiscoverPairs_NilCorosync(t *testing.T) {
	g := newGraphWithBridges(map[string][]string{"vmbr0": {"pve1", "pve2"}})
	pairs := latmesh.DiscoverPairs(g.Snapshot(), nil, "pve1")
	if len(pairs) != 1 || pairs[0].LinkID != "guest:vmbr0|pve1->pve2" {
		t.Fatalf("DiscoverPairs with nil corosync = %+v, want exactly the guest:vmbr0 pair", pairs)
	}
}

// TestDiscoverPairs_LocalNodeNotInBridgeGroup: a bridge that doesn't exist
// on localNode at all contributes no pair (nothing for this node to
// originate a probe from).
func TestDiscoverPairs_LocalNodeNotInBridgeGroup(t *testing.T) {
	g := newGraphWithBridges(map[string][]string{"vmbr1": {"pve2", "pve3"}})
	pairs := latmesh.DiscoverPairs(g.Snapshot(), nil, "pve1")
	if len(pairs) != 0 {
		t.Fatalf("DiscoverPairs = %+v, want none (pve1 has no vmbr1)", pairs)
	}
}
