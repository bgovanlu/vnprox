// SPDX-License-Identifier: Apache-2.0

package topology

import (
	"slices"
	"testing"
)

// T-3504. The shapes come from pvecube, PVE 9.2.4 — see
// planning/reports/evidence/pve-9.2.4-firewall-bridges.txt for the transcript
// these names and the fold/keep rule are read from, rather than from
// internal/pvemock or from documentation.
func TestFirewallBridgeOwner(t *testing.T) {
	// Field order is fieldalignment's, not the reading order: the strings and
	// the Node pack ahead of the bool.
	tests := []struct {
		name      string
		wantOwner string
		node      Node
		wantOK    bool
	}{
		{
			name:      "the reference node's running LXC firewall bridge",
			node:      Node{Kind: "bridge", Label: "fwbr103i0", NodeGroup: "pvecube"},
			wantOwner: "guest-nic:pvecube:103/net0",
			wantOK:    true,
		},
		{
			name:      "a second NIC on the same guest",
			node:      Node{Kind: "bridge", Label: "fwbr100i1", NodeGroup: "pvecube"},
			wantOwner: "guest-nic:pvecube:100/net1",
			wantOK:    true,
		},
		{
			// The whole point of anchoring the pattern: an operator's own
			// bridge that merely starts with the same four letters must never
			// be swallowed. This is the case that would be invisible if it
			// regressed — the bridge would simply stop being drawn.
			name:   "a hand-made bridge whose name starts with fwbr",
			node:   Node{Kind: "bridge", Label: "fwbr-dmz", NodeGroup: "pvecube"},
			wantOK: false,
		},
		{
			name:   "an ordinary bridge",
			node:   Node{Kind: "bridge", Label: "vmbr0", NodeGroup: "pvecube"},
			wantOK: false,
		},
		{
			name:   "the fwln leg is not a bridge and is never modelled anyway",
			node:   Node{Kind: "physnic", Label: "fwln103i0", NodeGroup: "pvecube"},
			wantOK: false,
		},
		{
			// A cluster-scoped node has no node name to build a guest-nic Ref
			// from. No fwbr is ever cluster-scoped, but the guard is what keeps
			// a malformed Ref off the wire if one ever were.
			name:   "no node group",
			node:   Node{Kind: "bridge", Label: "fwbr103i0", NodeGroup: ""},
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, ok := firewallBridgeOwner(tt.node)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (owner %q)", ok, tt.wantOK, owner)
			}
			if ok && owner != tt.wantOwner {
				t.Errorf("owner = %q, want %q", owner, tt.wantOwner)
			}
		})
	}
}

// pvecubeFirewallNodes reproduces the reference node's relevant shape: two
// running LXC guests (103, 104) whose NICs carry firewall=1 and therefore have
// an fwbr each, both attached to vmbr0.
func pvecubeFirewallNodes() ([]Node, []Edge) {
	nodes := []Node{
		{ID: "bridge:pvecube:vmbr0", Kind: "bridge", Label: "vmbr0", NodeGroup: "pvecube", Badges: []string{}},
		{ID: "bridge:pvecube:fwbr103i0", Kind: "bridge", Label: "fwbr103i0", NodeGroup: "pvecube", Badges: []string{}},
		{ID: "bridge:pvecube:fwbr104i0", Kind: "bridge", Label: "fwbr104i0", NodeGroup: "pvecube", Badges: []string{}},
		{ID: "guest-nic:pvecube:103/net0", Kind: "guest-nic", Label: "librenms/net0", NodeGroup: "pvecube", Badges: []string{}},
		{ID: "guest-nic:pvecube:104/net0", Kind: "guest-nic", Label: "powerdns/net0", NodeGroup: "pvecube", Badges: []string{}},
	}
	edges := []Edge{
		{From: "guest-nic:pvecube:103/net0", To: "bridge:pvecube:vmbr0", Kind: "attached-to"},
		{From: "guest-nic:pvecube:104/net0", To: "bridge:pvecube:vmbr0", Kind: "attached-to"},
	}
	return nodes, edges
}

func nodeByID(nodes []Node, id string) (Node, bool) {
	for _, n := range nodes {
		if n.ID == id {
			return n, true
		}
	}
	return Node{}, false
}

func TestFoldFirewallBridges_ReferenceNode(t *testing.T) {
	nodes, edges := pvecubeFirewallNodes()
	got, gotEdges := foldFirewallBridges(nodes, edges)

	for _, id := range []string{"bridge:pvecube:fwbr103i0", "bridge:pvecube:fwbr104i0"} {
		if _, ok := nodeByID(got, id); ok {
			t.Errorf("%s is still drawn as its own bridge", id)
		}
	}
	if _, ok := nodeByID(got, "bridge:pvecube:vmbr0"); !ok {
		t.Error("vmbr0 was removed; only fwbr bridges may fold")
	}

	// The relationship survives on the guest NIC, naming the bridge — T-3504
	// AC2: an operator must still be able to find these.
	nic, ok := nodeByID(got, "guest-nic:pvecube:103/net0")
	if !ok {
		t.Fatal("guest NIC 103/net0 disappeared")
	}
	if !slices.Contains(nic.Badges, "firewall=fwbr103i0") {
		t.Errorf("103/net0 badges = %v, want a firewall=fwbr103i0 badge", nic.Badges)
	}
	// ...and it is the *right* bridge, not just any of them.
	if slices.Contains(nic.Badges, "firewall=fwbr104i0") {
		t.Errorf("103/net0 badges = %v, want no reference to 104's firewall bridge", nic.Badges)
	}

	if len(gotEdges) != len(edges) {
		t.Errorf("got %d edges, want %d — no edge in this fixture touches an fwbr", len(gotEdges), len(edges))
	}
}

func TestFoldFirewallBridges_OrphanStaysVisible(t *testing.T) {
	// A firewall bridge whose guest NIC is not in this projection: the guest
	// stopped and pve-firewall's teardown did not run, or a ?node=/?layers=
	// filter excluded the NIC. Either way this is not chrome the operator can
	// be told to ignore — it is a real interface with no owner, and folding it
	// into a NIC that isn't there would delete it from the map entirely.
	nodes, edges := pvecubeFirewallNodes()
	nodes = slices.DeleteFunc(nodes, func(n Node) bool { return n.ID == "guest-nic:pvecube:103/net0" })

	got, _ := foldFirewallBridges(nodes, edges)
	if _, ok := nodeByID(got, "bridge:pvecube:fwbr103i0"); !ok {
		t.Error("an orphaned fwbr was folded away with nothing to fold it into")
	}
	// The one that still has its NIC folds as usual, so the orphan rule is
	// per-bridge and not an all-or-nothing bail-out.
	if _, ok := nodeByID(got, "bridge:pvecube:fwbr104i0"); ok {
		t.Error("fwbr104i0 still has its guest NIC and should have folded")
	}
}

func TestFoldFirewallBridges_DropsEdgesTouchingAFoldedBridge(t *testing.T) {
	nodes, edges := pvecubeFirewallNodes()
	// A hypothetical edge into a folded bridge. Nothing in the current
	// projection produces one (the guest NIC attaches to the logical bridge),
	// but an edge whose endpoint no longer exists is a dangling edge, and the
	// frontend renders those as lines to nowhere.
	edges = append(edges, Edge{From: "guest-nic:pvecube:104/net0", To: "bridge:pvecube:fwbr104i0", Kind: "attached-to"})

	got, gotEdges := foldFirewallBridges(nodes, edges)
	ids := map[string]bool{}
	for _, n := range got {
		ids[n.ID] = true
	}
	for _, e := range gotEdges {
		if !ids[e.From] || !ids[e.To] {
			t.Errorf("dangling edge %s -> %s survived the fold", e.From, e.To)
		}
	}
}

func TestFoldFirewallBridges_NoFirewallBridgesIsANoOp(t *testing.T) {
	nodes := []Node{
		{ID: "bridge:pve1:vmbr0", Kind: "bridge", Label: "vmbr0", NodeGroup: "pve1", Badges: []string{}},
		{ID: "guest-nic:pve1:100/net0", Kind: "guest-nic", Label: "app/net0", NodeGroup: "pve1", Badges: []string{}},
	}
	edges := []Edge{{From: "guest-nic:pve1:100/net0", To: "bridge:pve1:vmbr0", Kind: "attached-to"}}

	got, gotEdges := foldFirewallBridges(nodes, edges)
	if len(got) != len(nodes) || len(gotEdges) != len(edges) {
		t.Fatalf("got %d nodes / %d edges, want %d / %d", len(got), len(gotEdges), len(nodes), len(edges))
	}
	for _, n := range got {
		for _, b := range n.Badges {
			if len(b) >= 9 && b[:9] == "firewall=" {
				t.Errorf("%s gained %q with no firewall bridge anywhere", n.ID, b)
			}
		}
	}
}
