package topology_test

// Golden tests for T-702's shared management-path resolver
// (internal/topology/mgmtpath.go), run against the same real
// pvemock->collect->inventory.Graph pipeline project_test.go's golden
// projection tests use (buildGraph). Role classification (which ref is
// "mgmt" vs "corosync") is internal/change's job — that package's own tests
// cover DetectProtectedRoles against Node.IP/corosync.conf; here the input
// MgmtRoleRef sets are hand-built to isolate this package's actual
// responsibility: physical-path walking, redundancy, and badge painting.

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

func ref(kind inventory.Kind, node, id string) inventory.Ref {
	return inventory.Ref{Kind: kind, Node: node, ID: id}
}

// TestResolveMgmtPaths_SingleNode is AC1's single-node case: vmbr0 (the
// mgmt carrier) resolves a path of exactly [eno1], not redundant (only one
// link-up NIC in the path) — and does NOT pull in eno2, the fixture's spare,
// unattached NIC.
func TestResolveMgmtPaths_SingleNode(t *testing.T) {
	graph, _, _ := buildGraph(t, fixtureSingleNode)
	snap := graph.Snapshot()

	vmbr0 := ref(inventory.KindBridge, "pve1", "vmbr0")
	roleRefs := map[string][]topology.MgmtRoleRef{
		"pve1": {{Ref: vmbr0, Roles: []topology.MgmtRole{topology.MgmtRoleMgmt}}},
	}

	paths := topology.ResolveMgmtPaths(snap, roleRefs)
	got := paths["pve1"]
	if len(got) != 1 {
		t.Fatalf("paths[pve1] = %+v, want exactly 1 entry", got)
	}
	p := got[0]
	if p.Ref != vmbr0 {
		t.Errorf("Ref = %v, want %v", p.Ref, vmbr0)
	}
	if len(p.Roles) != 1 || p.Roles[0] != topology.MgmtRoleMgmt {
		t.Errorf("Roles = %v, want [mgmt]", p.Roles)
	}
	wantPath := []inventory.Ref{ref(inventory.KindPhysNic, "pve1", "eno1")}
	if len(p.Path) != 1 || p.Path[0] != wantPath[0] {
		t.Errorf("Path = %v, want %v (eno2 must not appear — it isn't in vmbr0's path)", p.Path, wantPath)
	}
	if p.Redundant {
		t.Errorf("Redundant = true, want false (single NIC path)")
	}

	// Badge painting: vmbr0 badges "mgmt", eno1 badges "mgmt-path", eno2
	// (the spare NIC) gets neither.
	topo := topology.Project(snap, topology.Filter{})
	topology.ApplyMgmtBadges(&topo, paths)

	bridge := nodeByID(t, topo.Nodes, "bridge:pve1:vmbr0")
	if !containsBadge(bridge.Badges, "mgmt") {
		t.Errorf("vmbr0 badges = %v, want to contain mgmt", bridge.Badges)
	}
	eno1 := nodeByID(t, topo.Nodes, "physnic:pve1:eno1")
	if !containsBadge(eno1.Badges, "mgmt-path") {
		t.Errorf("eno1 badges = %v, want to contain mgmt-path", eno1.Badges)
	}
	eno2 := nodeByID(t, topo.Nodes, "physnic:pve1:eno2")
	if containsBadge(eno2.Badges, "mgmt-path") || containsBadge(eno2.Badges, "mgmt") {
		t.Errorf("eno2 badges = %v, want neither mgmt nor mgmt-path (it's not in vmbr0's path)", eno2.Badges)
	}
}

// TestResolveMgmtPaths_ThreeNodeVlan is AC1's three-node-vlan case: every
// vmbr0 resolves a path of [bond0, eno1, eno2]; with a corosync role
// additionally supplied (rings match the mgmt IP on this fixture), vmbr0
// badges both mgmt and corosync. Redundancy is only asserted for pve1: this
// package's own documented gap (project_test.go's TestProject_ThreeNodeVlan
// doc comment) is that a single vnproxd's collector only host-polls its own
// local node, so pve2/pve3's PhysNic.LinkUpSet is never true in this
// pipeline — countLinkUp correctly reports "not confirmed redundant" rather
// than guessing, exactly like statusOf's tri-state link handling.
func TestResolveMgmtPaths_ThreeNodeVlan(t *testing.T) {
	graph, _, _ := buildGraph(t, fixtureThreeNodeVlan)
	snap := graph.Snapshot()

	roleRefs := map[string][]topology.MgmtRoleRef{}
	for _, node := range []string{"pve1", "pve2", "pve3"} {
		roleRefs[node] = []topology.MgmtRoleRef{{
			Ref:   ref(inventory.KindBridge, node, "vmbr0"),
			Roles: []topology.MgmtRole{topology.MgmtRoleMgmt, topology.MgmtRoleCorosync},
		}}
	}

	paths := topology.ResolveMgmtPaths(snap, roleRefs)
	for _, node := range []string{"pve1", "pve2", "pve3"} {
		got := paths[node]
		if len(got) != 1 {
			t.Fatalf("paths[%s] = %+v, want exactly 1 entry", node, got)
		}
		p := got[0]
		if node == "pve1" && !p.Redundant {
			t.Errorf("%s: Redundant = false, want true (bond0 has 2 link-up slaves)", node)
		}
		wantPath := map[inventory.Ref]bool{
			ref(inventory.KindBond, node, "bond0"):   true,
			ref(inventory.KindPhysNic, node, "eno1"): true,
			ref(inventory.KindPhysNic, node, "eno2"): true,
		}
		if len(p.Path) != len(wantPath) {
			t.Fatalf("%s: Path = %v, want exactly bond0+eno1+eno2", node, p.Path)
		}
		for _, r := range p.Path {
			if !wantPath[r] {
				t.Errorf("%s: unexpected path member %v", node, r)
			}
		}
	}

	topo := topology.Project(snap, topology.Filter{})
	topology.ApplyMgmtBadges(&topo, paths)

	for _, node := range []string{"pve1", "pve2", "pve3"} {
		bridge := nodeByID(t, topo.Nodes, "bridge:"+node+":vmbr0")
		if !containsBadge(bridge.Badges, "mgmt") || !containsBadge(bridge.Badges, "corosync") {
			t.Errorf("%s vmbr0 badges = %v, want both mgmt and corosync", node, bridge.Badges)
		}
		bond := nodeByID(t, topo.Nodes, "bond:"+node+":bond0")
		if !containsBadge(bond.Badges, "mgmt-path") {
			t.Errorf("%s bond0 badges = %v, want mgmt-path", node, bond.Badges)
		}
		for _, nic := range []string{"eno1", "eno2"} {
			n := nodeByID(t, topo.Nodes, "physnic:"+node+":"+nic)
			if !containsBadge(n.Badges, "mgmt-path") {
				t.Errorf("%s %s badges = %v, want mgmt-path", node, nic, n.Badges)
			}
		}
	}
}

// TestResolveMgmtPaths_VlanCarrier is AC1's "VLAN-carrier fixture" case: the
// mgmt IP lives on vmbr0.30 (a VLAN sub-interface), so the carrier itself
// badges mgmt and the walk continues through its parent bridge vmbr0 down
// to eno1 — vmbr0.30 is NOT part of its own Path (only entities beyond the
// carrier are "mgmt-path" members).
func TestResolveMgmtPaths_VlanCarrier(t *testing.T) {
	graph, _, _ := buildGraph(t, fixtureVlanMgmt)
	snap := graph.Snapshot()

	carrier := ref(inventory.KindVlan, "pve1", "vmbr0.30")
	roleRefs := map[string][]topology.MgmtRoleRef{
		"pve1": {{Ref: carrier, Roles: []topology.MgmtRole{topology.MgmtRoleMgmt}}},
	}

	paths := topology.ResolveMgmtPaths(snap, roleRefs)
	got := paths["pve1"]
	if len(got) != 1 {
		t.Fatalf("paths[pve1] = %+v, want exactly 1 entry", got)
	}
	p := got[0]
	wantPath := map[inventory.Ref]bool{
		ref(inventory.KindBridge, "pve1", "vmbr0"): true,
		ref(inventory.KindPhysNic, "pve1", "eno1"): true,
	}
	if len(p.Path) != len(wantPath) {
		t.Fatalf("Path = %v, want exactly vmbr0+eno1", p.Path)
	}
	for _, r := range p.Path {
		if !wantPath[r] {
			t.Errorf("unexpected path member %v", r)
		}
		if r == carrier {
			t.Errorf("carrier %v must not appear in its own Path", carrier)
		}
	}
	if p.Redundant {
		t.Errorf("Redundant = true, want false (single NIC path)")
	}

	topo := topology.Project(snap, topology.Filter{})
	topology.ApplyMgmtBadges(&topo, paths)

	vlanNode := nodeByID(t, topo.Nodes, "vlan:pve1:vmbr0.30")
	if !containsBadge(vlanNode.Badges, "mgmt") {
		t.Errorf("vmbr0.30 badges = %v, want mgmt", vlanNode.Badges)
	}
	if containsBadge(vlanNode.Badges, "mgmt-path") {
		t.Errorf("vmbr0.30 badges = %v, want no mgmt-path (it's the carrier, not a path member)", vlanNode.Badges)
	}
	bridge := nodeByID(t, topo.Nodes, "bridge:pve1:vmbr0")
	if !containsBadge(bridge.Badges, "mgmt-path") {
		t.Errorf("vmbr0 badges = %v, want mgmt-path", bridge.Badges)
	}
	if containsBadge(bridge.Badges, "mgmt") {
		t.Errorf("vmbr0 badges = %v, want no mgmt badge (only the carrier gets role badges)", bridge.Badges)
	}
	eno1 := nodeByID(t, topo.Nodes, "physnic:pve1:eno1")
	if !containsBadge(eno1.Badges, "mgmt-path") {
		t.Errorf("eno1 badges = %v, want mgmt-path", eno1.Badges)
	}
}

func TestResolveMgmtPaths_Empty(t *testing.T) {
	graph, _, _ := buildGraph(t, fixtureSingleNode)
	if got := topology.ResolveMgmtPaths(graph.Snapshot(), nil); got != nil {
		t.Errorf("ResolveMgmtPaths(nil) = %v, want nil", got)
	}
}

// TestApplyMgmtBadges_Property is AC2's "property test: every ref with a
// role badge appears in status and vice versa" — checked against the
// three-node-vlan fixture's real topology, with a hand-built (not just
// single-carrier) MgmtPath set exercising multiple nodes, multiple roles,
// and overlapping paths, so the property holds generally, not just for the
// single-ref cases the other tests above already cover in detail.
func TestApplyMgmtBadges_Property(t *testing.T) {
	graph, _, _ := buildGraph(t, fixtureThreeNodeVlan)
	snap := graph.Snapshot()
	topo := topology.Project(snap, topology.Filter{})

	paths := map[string][]topology.MgmtPath{
		"pve1": {{
			Ref:   ref(inventory.KindBridge, "pve1", "vmbr0"),
			Roles: []topology.MgmtRole{topology.MgmtRoleMgmt, topology.MgmtRoleCorosync},
			Path: []inventory.Ref{
				ref(inventory.KindBond, "pve1", "bond0"),
				ref(inventory.KindPhysNic, "pve1", "eno1"),
				ref(inventory.KindPhysNic, "pve1", "eno2"),
			},
			Redundant: true,
		}},
		"pve2": {{
			Ref:       ref(inventory.KindBridge, "pve2", "vmbr0"),
			Roles:     []topology.MgmtRole{topology.MgmtRoleMgmt},
			Path:      []inventory.Ref{ref(inventory.KindBond, "pve2", "bond0")},
			Redundant: false,
		}},
	}

	topology.ApplyMgmtBadges(&topo, paths)

	// Forward direction: every (ref, role) named in paths shows up as a
	// badge on that exact node.
	wantRoleBadge := map[string]map[string]bool{} // ref -> badge -> want
	wantPathBadge := map[string]bool{}
	for _, list := range paths {
		for _, p := range list {
			id := p.Ref.String()
			if wantRoleBadge[id] == nil {
				wantRoleBadge[id] = map[string]bool{}
			}
			for _, r := range p.Roles {
				wantRoleBadge[id][string(r)] = true
			}
			for _, pathRef := range p.Path {
				wantPathBadge[pathRef.String()] = true
			}
		}
	}

	byID := map[string]topology.Node{}
	for _, n := range topo.Nodes {
		byID[n.ID] = n
	}

	for id, roles := range wantRoleBadge {
		n, ok := byID[id]
		if !ok {
			t.Fatalf("carrier %s not found among rendered nodes", id)
		}
		for role := range roles {
			if !containsBadge(n.Badges, role) {
				t.Errorf("%s badges = %v, want %q (named as a role in the resolved status)", id, n.Badges, role)
			}
		}
	}
	for id := range wantPathBadge {
		n, ok := byID[id]
		if !ok {
			t.Fatalf("path member %s not found among rendered nodes", id)
		}
		if !containsBadge(n.Badges, "mgmt-path") {
			t.Errorf("%s badges = %v, want mgmt-path (named as a path member in the resolved status)", id, n.Badges)
		}
	}

	// Reverse direction: no node anywhere in the topology carries a
	// mgmt/corosync/mgmt-path badge unless it was named above — i.e. this
	// resolved status is the *only* source of those three badge values
	// (project.go's badgesOf never emits them itself).
	for _, n := range topo.Nodes {
		for _, b := range n.Badges {
			switch b {
			case "mgmt", "corosync":
				if !wantRoleBadge[n.ID][b] {
					t.Errorf("%s unexpectedly carries badge %q, not named as that role in the resolved status", n.ID, b)
				}
			case "mgmt-path":
				if !wantPathBadge[n.ID] {
					t.Errorf("%s unexpectedly carries badge mgmt-path, not named as a path member in the resolved status", n.ID)
				}
			}
		}
	}
}
