package topology_test

import (
	"sort"
	"testing"

	"github.com/bgovanlu/vnprox/internal/topology"
)

func nodeByID(t *testing.T, nodes []topology.Node, id string) topology.Node {
	t.Helper()
	for _, n := range nodes {
		if n.ID == id {
			return n
		}
	}
	t.Fatalf("node %q not found among %d nodes", id, len(nodes))
	return topology.Node{}
}

func edgesFrom(edges []topology.Edge, from string) []topology.Edge {
	var out []topology.Edge
	for _, e := range edges {
		if e.From == from {
			out = append(out, e)
		}
	}
	return out
}

func hasEdge(edges []topology.Edge, from, to, kind string) bool {
	for _, e := range edges {
		if e.From == from && e.To == to && e.Kind == kind {
			return true
		}
	}
	return false
}

// TestProject_SingleNode is the golden-structure test for the single-node
// fixture (T-106 acceptance criterion 1): one physical NIC, one bridge, one
// LLDP neighbor, two guests each with one NIC, no bonds/VLANs/SDN.
func TestProject_SingleNode(t *testing.T) {
	graph, _, _ := buildGraph(t, fixtureSingleNode)
	topo := topology.Project(graph.Snapshot(), topology.Filter{})

	if got, want := topo.Layers, topology.AllLayers; len(got) != len(want) {
		t.Fatalf("Layers = %v, want %v", got, want)
	}
	if topo.GeneratedAt <= 0 {
		t.Errorf("GeneratedAt = %d, want > 0", topo.GeneratedAt)
	}

	wantNodeIDs := []string{
		"physnic:pve1:eno1",
		"bridge:pve1:vmbr0",
		"guest:pve1:100",
		"guest:pve1:101",
		"guest-nic:pve1:100/net0",
		"guest-nic:pve1:101/net0",
	}
	gotIDs := make(map[string]bool, len(topo.Nodes))
	for _, n := range topo.Nodes {
		gotIDs[n.ID] = true
	}
	for _, id := range wantNodeIDs {
		if !gotIDs[id] {
			t.Errorf("missing expected node %q; got nodes: %v", id, nodeIDs(topo.Nodes))
		}
	}
	// Exactly one LLDP neighbor node (physical layer), id contains the
	// documented "local-iface/chassis-id/port-id" scheme.
	var lldpCount int
	for _, n := range topo.Nodes {
		if n.Kind == "lldp-neighbor" {
			lldpCount++
			if n.Layer != topology.LayerPhysical {
				t.Errorf("lldp-neighbor layer = %q, want %q", n.Layer, topology.LayerPhysical)
			}
			if n.Label != "sw-access-01" {
				t.Errorf("lldp-neighbor label = %q, want sw-access-01", n.Label)
			}
		}
	}
	if lldpCount != 1 {
		t.Errorf("lldp-neighbor node count = %d, want 1", lldpCount)
	}

	// No bonds, no VLAN sub-interfaces, no SDN in this fixture.
	for _, n := range topo.Nodes {
		if n.Kind == "bond" || n.Kind == "vlan" || n.Layer == topology.LayerSDN {
			t.Errorf("unexpected node in single-node fixture: %+v", n)
		}
	}

	eno1 := nodeByID(t, topo.Nodes, "physnic:pve1:eno1")
	if eno1.Status != topology.StatusOK {
		t.Errorf("eno1 status = %q, want ok", eno1.Status)
	}
	if eno1.Layer != topology.LayerPhysical {
		t.Errorf("eno1 layer = %q, want phys", eno1.Layer)
	}
	if eno1.NodeGroup != "pve1" {
		t.Errorf("eno1 nodeGroup = %q, want pve1", eno1.NodeGroup)
	}
	if eno1.Label != "eno1" {
		t.Errorf("eno1 label = %q, want eno1", eno1.Label)
	}

	bridge := nodeByID(t, topo.Nodes, "bridge:pve1:vmbr0")
	if bridge.Status != topology.StatusOK {
		t.Errorf("vmbr0 status = %q, want ok", bridge.Status)
	}
	if len(bridge.Badges) != 0 {
		t.Errorf("vmbr0 badges = %v, want none (not VLAN-aware)", bridge.Badges)
	}

	web01Nic := nodeByID(t, topo.Nodes, "guest-nic:pve1:100/net0")
	if web01Nic.Label != "web01/net0" {
		t.Errorf("web01 nic label = %q, want web01/net0", web01Nic.Label)
	}

	// Edges: eno1 -[port-of]-> vmbr0; eno1 -[lldp-adjacent]-> neighbor;
	// each guest nic -[attached-to]-> vmbr0.
	if !hasEdge(topo.Edges, "physnic:pve1:eno1", "bridge:pve1:vmbr0", "port-of") {
		t.Errorf("missing port-of edge eno1->vmbr0; edges: %+v", topo.Edges)
	}
	if !hasEdge(topo.Edges, "guest-nic:pve1:100/net0", "bridge:pve1:vmbr0", "attached-to") {
		t.Errorf("missing attached-to edge 100/net0->vmbr0")
	}
	if !hasEdge(topo.Edges, "guest-nic:pve1:101/net0", "bridge:pve1:vmbr0", "attached-to") {
		t.Errorf("missing attached-to edge 101/net0->vmbr0")
	}
	lldpEdges := edgesFrom(topo.Edges, "physnic:pve1:eno1")
	var sawLldpAdjacent bool
	for _, e := range lldpEdges {
		if e.Kind == "lldp-adjacent" {
			sawLldpAdjacent = true
		}
	}
	if !sawLldpAdjacent {
		t.Errorf("missing lldp-adjacent edge from eno1; edges from eno1: %+v", lldpEdges)
	}

	if len(topo.Nodes) != 7 {
		t.Errorf("total nodes = %d, want 7 (got %v)", len(topo.Nodes), nodeIDs(topo.Nodes))
	}
	if len(topo.Edges) != 4 {
		t.Errorf("total edges = %d, want 4 (got %+v)", len(topo.Edges), topo.Edges)
	}
}

// TestProject_ThreeNodeVlan is the golden-structure test for the
// three-node-vlan fixture (T-106 acceptance criterion 1): bonded,
// VLAN-aware bridges, a VLAN sub-interface per node, and a cluster-scoped
// SDN vlan zone with two VNets/subnets.
//
// One collector-pipeline fact this test pins down deliberately: a single
// vnproxd's collector only runs host-netlink/LLDP polls against its own
// "local" node (internal/collect; pvemock always marks fixture node index
// 0 — pve1 here — local). pve2 and pve3's PhysNic/Bond entities still
// exist (PVE's own GET /nodes/{node}/network cross-check reaches every
// node), but carry no live linkUp/slaves data — this package's statusOf
// reports "unknown" rather than guessing "down"/"degraded" from a field no
// source ever set (see project.go's doc comment on statusOf/bondStatus).
// Multi-node clusters only get full physical-layer fidelity once a future
// task wires peer-node host polling over docs/api.md's peer API; flagged
// in the T-106 completion report.
func TestProject_ThreeNodeVlan(t *testing.T) {
	graph, _, _ := buildGraph(t, fixtureThreeNodeVlan)
	topo := topology.Project(graph.Snapshot(), topology.Filter{})

	// 3 nodes x (2 physnic + bond + bridge + vlan) = 15
	// + 2 lldp-neighbors (pve1 only - see doc comment above) = 2
	// + 2 guests x (guest + guest-nic) = 4
	// + SDN: zone + 2 vnets + 2 subnets = 5
	if len(topo.Nodes) != 26 {
		t.Fatalf("total nodes = %d, want 26 (got %v)", len(topo.Nodes), nodeIDs(topo.Nodes))
	}
	// 6 enslaved-by (2 slaves x 3 nodes) + 3 port-of + 3 tagged-on
	// + 2 lldp-adjacent (pve1 only) + 2 guest attached-to
	// + 6 realizes (2 vnets x 3 nodes)
	if len(topo.Edges) != 22 {
		t.Fatalf("total edges = %d, want 22 (got %+v)", len(topo.Edges), topo.Edges)
	}

	bond := nodeByID(t, topo.Nodes, "bond:pve1:bond0")
	if bond.Status != topology.StatusOK {
		t.Errorf("bond0 status = %q, want ok (fixture's link is up on both slaves)", bond.Status)
	}
	if !containsBadge(bond.Badges, "mode=802.3ad") {
		t.Errorf("bond0 badges = %v, want to contain mode=802.3ad", bond.Badges)
	}

	// pve2/pve3 lack live host-netlink data in this single-collector test
	// setup (see the test's doc comment) — their bonds/physnics render
	// "unknown", not a false "down"/"degraded".
	for _, node := range []string{"pve2", "pve3"} {
		peerBond := nodeByID(t, topo.Nodes, "bond:"+node+":bond0")
		if peerBond.Status != topology.StatusUnknown {
			t.Errorf("%s bond0 status = %q, want unknown (no live slave data)", node, peerBond.Status)
		}
		peerNic := nodeByID(t, topo.Nodes, "physnic:"+node+":eno1")
		if peerNic.Status != topology.StatusUnknown {
			t.Errorf("%s eno1 status = %q, want unknown (no live link data)", node, peerNic.Status)
		}
	}

	if !hasEdge(topo.Edges, "physnic:pve1:eno1", "bond:pve1:bond0", "enslaved-by") {
		t.Errorf("missing enslaved-by edge eno1->bond0")
	}
	enslaved := edgesFrom(topo.Edges, "physnic:pve1:eno1")
	var sawActive bool
	for _, e := range enslaved {
		if e.Kind == "enslaved-by" && containsBadge(e.Badges, "active") {
			sawActive = true
		}
	}
	if !sawActive {
		t.Errorf("expected an 'active' badge on eno1's enslaved-by edge; got %+v", enslaved)
	}

	if !hasEdge(topo.Edges, "bond:pve1:bond0", "bridge:pve1:vmbr0", "port-of") {
		t.Errorf("missing port-of edge bond0->vmbr0")
	}

	vlan := nodeByID(t, topo.Nodes, "vlan:pve1:vmbr0.20")
	if !containsBadge(vlan.Badges, "vid=20") {
		t.Errorf("vmbr0.20 badges = %v, want vid=20", vlan.Badges)
	}
	if !hasEdge(topo.Edges, "vlan:pve1:vmbr0.20", "bridge:pve1:vmbr0", "tagged-on") {
		t.Errorf("missing tagged-on edge vmbr0.20->vmbr0")
	}

	guestNic := nodeByID(t, topo.Nodes, "guest-nic:pve1:200/net0")
	if !containsBadge(guestNic.Badges, "vid=100") {
		t.Errorf("app01 nic badges = %v, want vid=100 (guest tag rides the VLAN-aware trunk)", guestNic.Badges)
	}

	zone := nodeByID(t, topo.Nodes, "sdn-zone::vlanz")
	if zone.Status != topology.StatusOK {
		t.Errorf("sdn zone status = %q, want ok", zone.Status)
	}
	if zone.NodeGroup != "" {
		t.Errorf("sdn zone nodeGroup = %q, want empty (cluster-scoped band)", zone.NodeGroup)
	}

	vnet100 := nodeByID(t, topo.Nodes, "sdn-vnet::vlanz/vnet100")
	if !containsBadge(vnet100.Badges, "tag=100") {
		t.Errorf("vnet100 badges = %v, want tag=100", vnet100.Badges)
	}
	if !hasEdge(topo.Edges, "sdn-vnet::vlanz/vnet100", "bridge:pve1:vmbr0", "realizes") {
		t.Errorf("missing realizes edge vnet100->pve1's vmbr0")
	}
	if !hasEdge(topo.Edges, "sdn-vnet::vlanz/vnet100", "bridge:pve2:vmbr0", "realizes") {
		t.Errorf("missing realizes edge vnet100->pve2's vmbr0")
	}
	if !hasEdge(topo.Edges, "sdn-vnet::vlanz/vnet100", "bridge:pve3:vmbr0", "realizes") {
		t.Errorf("missing realizes edge vnet100->pve3's vmbr0")
	}
}

// TestProject_VLANFilter is T-106 acceptance criterion 2: ?vlan=20 against
// the three-node-vlan fixture returns only the entities that carry VLAN 20
// (the vmbr0.20 sub-interface on each node) plus the bridge each one tags
// onto (kept so the edge has both endpoints, per restrictToVLAN's doc
// comment) — none of the VLAN-100/200 guest traffic or SDN VNets appear.
func TestProject_VLANFilter(t *testing.T) {
	graph, _, _ := buildGraph(t, fixtureThreeNodeVlan)
	topo := topology.Project(graph.Snapshot(), topology.Filter{VLAN: 20})

	if len(topo.Nodes) != 6 {
		t.Fatalf("vlan=20 nodes = %d, want 6 (got %v)", len(topo.Nodes), nodeIDs(topo.Nodes))
	}
	if len(topo.Edges) != 3 {
		t.Fatalf("vlan=20 edges = %d, want 3 (got %+v)", len(topo.Edges), topo.Edges)
	}
	for _, node := range []string{"pve1", "pve2", "pve3"} {
		vlanID := "vlan:" + node + ":vmbr0.20"
		bridgeID := "bridge:" + node + ":vmbr0"
		nodeByID(t, topo.Nodes, vlanID)
		nodeByID(t, topo.Nodes, bridgeID)
		if !hasEdge(topo.Edges, vlanID, bridgeID, "tagged-on") {
			t.Errorf("missing tagged-on edge %s->%s", vlanID, bridgeID)
		}
	}
	for _, n := range topo.Nodes {
		if n.Kind == "bond" || n.Kind == "physnic" || n.Layer == topology.LayerSDN || n.Layer == topology.LayerGuest {
			t.Errorf("vlan=20 filter leaked a non-VLAN-20 entity: %+v", n)
		}
	}
}

func nodeIDs(nodes []topology.Node) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.ID
	}
	sort.Strings(out)
	return out
}

func containsBadge(badges []string, want string) bool {
	for _, b := range badges {
		if b == want {
			return true
		}
	}
	return false
}
