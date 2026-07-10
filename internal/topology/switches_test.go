package topology_test

import (
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// buildLLDPGraph constructs a small graph with PhysNics and LldpNeighbors
// wired by hand (no pvemock/host JSON round-trip needed — switch merging is
// a pure function of the inventory graph), for T-302 AC1's chassis-ID merge
// scenarios.
func buildLLDPGraph(t *testing.T, neighbors []*inventory.LldpNeighbor) inventory.Snapshot {
	t.Helper()
	g := inventory.NewGraph()
	byNode := map[string][]inventory.Entity{}
	for _, n := range neighbors {
		nicRef := inventory.Ref{Kind: inventory.KindPhysNic, Node: n.Node, ID: n.LocalIface}
		byNode[n.Node] = append(byNode[n.Node], &inventory.PhysNic{Ref: nicRef, Name: n.LocalIface, LinkUp: true, LinkUpSet: true})
	}
	for node, ents := range byNode {
		g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: node}, ents)
	}
	byLLDPNode := map[string][]inventory.Entity{}
	for _, n := range neighbors {
		byLLDPNode[n.Node] = append(byLLDPNode[n.Node], n)
	}
	for node, ents := range byLLDPNode {
		g.ApplyPoll(inventory.SourceHostLLDP, inventory.Scope{Node: node}, ents)
	}
	return g.Snapshot()
}

func switchNodes(nodes []topology.Node) []topology.Node {
	var out []topology.Node
	for _, n := range nodes {
		if n.Kind == topology.KindSwitch {
			out = append(out, n)
		}
	}
	return out
}

// TestSwitchMerge_SameChassisAcrossNodes is T-302 AC1's primary case: two
// (here, three) nodes seeing the same chassis ID merge into one switch
// entity carrying every contributing link.
func TestSwitchMerge_SameChassisAcrossNodes(t *testing.T) {
	now := time.Now()
	neighbors := []*inventory.LldpNeighbor{
		{Ref: inventory.Ref{Kind: inventory.KindLldpNeighbor, Node: "pve1", ID: "eno1/sw1/Te1"}, LocalIface: "eno1", Node: "pve1", ChassisID: "ac:1f:6b:01:00:01", ChassisName: "sw-core-01", PortID: "Te1/0/1", TTL: 120, LastSeen: now.Unix()},
		{Ref: inventory.Ref{Kind: inventory.KindLldpNeighbor, Node: "pve2", ID: "eno1/sw1/Te2"}, LocalIface: "eno1", Node: "pve2", ChassisID: "ac:1f:6b:01:00:01", ChassisName: "sw-core-01", PortID: "Te1/0/2", TTL: 120, LastSeen: now.Unix()},
		{Ref: inventory.Ref{Kind: inventory.KindLldpNeighbor, Node: "pve3", ID: "eno1/sw1/Te3"}, LocalIface: "eno1", Node: "pve3", ChassisID: "ac:1f:6b:01:00:01", ChassisName: "sw-core-01", PortID: "Te1/0/3", TTL: 120, LastSeen: now.Unix()},
	}
	snap := buildLLDPGraph(t, neighbors)
	topo := topology.Project(snap, topology.Filter{Now: now})

	switches := switchNodes(topo.Nodes)
	if len(switches) != 1 {
		t.Fatalf("got %d switch nodes, want 1 (merged): %+v", len(switches), switches)
	}
	sw := switches[0]
	if sw.Label != "sw-core-01" {
		t.Errorf("switch label = %q, want sw-core-01", sw.Label)
	}

	var portEdges []topology.Edge
	for _, e := range topo.Edges {
		if e.To == sw.ID && e.Kind == topology.EdgeLLDPPort {
			portEdges = append(portEdges, e)
		}
	}
	if len(portEdges) != 3 {
		t.Fatalf("got %d lldp-port edges into the merged switch, want 3 (one per node's link): %+v", len(portEdges), portEdges)
	}
	wantFrom := map[string]bool{
		"physnic:pve1:eno1": true, "physnic:pve2:eno1": true, "physnic:pve3:eno1": true,
	}
	for _, e := range portEdges {
		if !wantFrom[e.From] {
			t.Errorf("unexpected lldp-port edge from %q", e.From)
		}
		delete(wantFrom, e.From)
	}
	if len(wantFrom) != 0 {
		t.Errorf("missing lldp-port edges from: %v", wantFrom)
	}
}

// TestSwitchMerge_DistinctChassisStayDistinct is AC1's negative case,
// including the same-name/different-ID edge case: two neighbors that share
// a chassis *name* but have different chassis IDs must render as two
// distinct switches, not merge.
func TestSwitchMerge_DistinctChassisStayDistinct(t *testing.T) {
	now := time.Now()
	neighbors := []*inventory.LldpNeighbor{
		{Ref: inventory.Ref{Kind: inventory.KindLldpNeighbor, Node: "pve1", ID: "eno1/a/p1"}, LocalIface: "eno1", Node: "pve1", ChassisID: "aa:aa:aa:aa:aa:01", ChassisName: "access-switch", PortID: "Gi1/0/1", TTL: 120, LastSeen: now.Unix()},
		{Ref: inventory.Ref{Kind: inventory.KindLldpNeighbor, Node: "pve1", ID: "eno2/b/p1"}, LocalIface: "eno2", Node: "pve1", ChassisID: "aa:aa:aa:aa:aa:02", ChassisName: "access-switch", PortID: "Gi1/0/1", TTL: 120, LastSeen: now.Unix()},
	}
	snap := buildLLDPGraph(t, neighbors)
	topo := topology.Project(snap, topology.Filter{Now: now})

	switches := switchNodes(topo.Nodes)
	if len(switches) != 2 {
		t.Fatalf("got %d switch nodes, want 2 (same name, different chassis ID must stay distinct): %+v", len(switches), switches)
	}
	if switches[0].ID == switches[1].ID {
		t.Errorf("distinct chassis IDs produced the same switch node id %q", switches[0].ID)
	}
	for _, sw := range switches {
		if sw.Label != "access-switch" {
			t.Errorf("switch label = %q, want access-switch", sw.Label)
		}
	}
}

// TestSwitchMerge_ChassisIDCaseInsensitive checks that grouping normalizes
// chassis ID casing (vendors are inconsistent about MAC address case).
func TestSwitchMerge_ChassisIDCaseInsensitive(t *testing.T) {
	now := time.Now()
	neighbors := []*inventory.LldpNeighbor{
		{Ref: inventory.Ref{Kind: inventory.KindLldpNeighbor, Node: "pve1", ID: "eno1/x/p1"}, LocalIface: "eno1", Node: "pve1", ChassisID: "AC:1F:6B:01:00:01", ChassisName: "sw1", PortID: "p1", TTL: 120, LastSeen: now.Unix()},
		{Ref: inventory.Ref{Kind: inventory.KindLldpNeighbor, Node: "pve2", ID: "eno1/x/p2"}, LocalIface: "eno1", Node: "pve2", ChassisID: "ac:1f:6b:01:00:01", ChassisName: "sw1", PortID: "p2", TTL: 120, LastSeen: now.Unix()},
	}
	snap := buildLLDPGraph(t, neighbors)
	topo := topology.Project(snap, topology.Filter{Now: now})

	switches := switchNodes(topo.Nodes)
	if len(switches) != 1 {
		t.Fatalf("got %d switch nodes, want 1 (case-insensitive chassis ID merge): %+v", len(switches), switches)
	}
}
