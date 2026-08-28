// SPDX-License-Identifier: Apache-2.0

package topology_test

import (
	"strconv"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// TestProject_GuestCollapse exercises the guest-per-bridge collapse
// deliverable (docs/features/topology.md §1: "Collapsible per bridge
// ('23 guests' pill expands on click)") against a hand-built graph, since
// neither golden fixture has enough guests to cross
// topology.DefaultCollapseThreshold. 10 single-NIC guests attach to
// vmbr0 (over threshold, so they collapse into one pill and, since each
// has no other NIC, the Guest nodes themselves disappear too); one
// two-NIC guest has one NIC on vmbr0 (absorbed into the pill) and one on
// vmbr1 (under threshold, stays individually rendered) — proving a guest
// with a still-expanded NIC is not dropped.
func TestProject_GuestCollapse(t *testing.T) {
	graph := inventory.NewGraph()

	graph.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: "n1"}, []inventory.Entity{
		&inventory.Bridge{Ref: inventory.Ref{Kind: inventory.KindBridge, Node: "n1", ID: "vmbr0"}, Name: "vmbr0", Virt: inventory.BridgeLinux},
		&inventory.Bridge{Ref: inventory.Ref{Kind: inventory.KindBridge, Node: "n1", ID: "vmbr1"}, Name: "vmbr1", Virt: inventory.BridgeLinux},
	})

	var guests []inventory.Entity
	const collapseCount = topology.DefaultCollapseThreshold + 2 // comfortably over threshold
	for i := 1; i <= collapseCount; i++ {
		vmid := strconv.Itoa(100 + i)
		guests = append(guests,
			&inventory.Guest{Ref: inventory.Ref{Kind: inventory.KindGuest, Node: "n1", ID: vmid}, VMID: 100 + i, Name: "vm" + vmid, Status: "running"},
			&inventory.GuestNic{Ref: inventory.Ref{Kind: inventory.KindGuestNic, Node: "n1", ID: vmid + "/net0"}, Guest: inventory.Ref{Kind: inventory.KindGuest, Node: "n1", ID: vmid}, Key: "net0", TargetName: "vmbr0"},
		)
	}
	// The two-NIC guest: net0 collapses onto vmbr0, net1 stays expanded on
	// vmbr1 (only one guest there).
	guests = append(guests,
		&inventory.Guest{Ref: inventory.Ref{Kind: inventory.KindGuest, Node: "n1", ID: "999"}, VMID: 999, Name: "vm999", Status: "running"},
		&inventory.GuestNic{Ref: inventory.Ref{Kind: inventory.KindGuestNic, Node: "n1", ID: "999/net0"}, Guest: inventory.Ref{Kind: inventory.KindGuest, Node: "n1", ID: "999"}, Key: "net0", TargetName: "vmbr0"},
		&inventory.GuestNic{Ref: inventory.Ref{Kind: inventory.KindGuestNic, Node: "n1", ID: "999/net1"}, Guest: inventory.Ref{Kind: inventory.KindGuest, Node: "n1", ID: "999"}, Key: "net1", TargetName: "vmbr1"},
	)
	graph.ApplyPoll(inventory.SourcePVEGuest, inventory.Scope{Node: "n1"}, guests)

	topo := topology.Project(graph.Snapshot(), topology.Filter{})

	groupID := "guest-group:n1:bridge:n1:vmbr0"
	group := nodeByID(t, topo.Nodes, groupID)
	if group.Kind != "guest-group" {
		t.Errorf("group kind = %q, want guest-group", group.Kind)
	}
	if group.CollapsedCount != collapseCount+1 { // +1 for vm999's net0
		t.Errorf("group CollapsedCount = %d, want %d", group.CollapsedCount, collapseCount+1)
	}
	if !containsBadge(group.Badges, "count="+strconv.Itoa(collapseCount+1)) {
		t.Errorf("group badges = %v, want count=%d", group.Badges, collapseCount+1)
	}
	if !hasEdge(topo.Edges, groupID, "bridge:n1:vmbr0", "attached-to") {
		t.Errorf("missing collapsed group edge to vmbr0")
	}

	// The 10 single-NIC guests and their NICs are gone entirely.
	for i := 1; i <= collapseCount; i++ {
		vmid := strconv.Itoa(100 + i)
		for _, n := range topo.Nodes {
			if n.ID == "guest:n1:"+vmid || n.ID == "guest-nic:n1:"+vmid+"/net0" {
				t.Errorf("expected collapsed guest/nic %s to be absorbed, but it is still rendered: %+v", n.ID, n)
			}
		}
	}

	// vm999 still renders (its net1 didn't collapse), and its net1->vmbr1
	// edge/node survive individually.
	nodeByID(t, topo.Nodes, "guest:n1:999")
	nodeByID(t, topo.Nodes, "guest-nic:n1:999/net1")
	if !hasEdge(topo.Edges, "guest-nic:n1:999/net1", "bridge:n1:vmbr1", "attached-to") {
		t.Errorf("missing vm999 net1->vmbr1 edge")
	}
	for _, n := range topo.Nodes {
		if n.ID == "guest-nic:n1:999/net0" {
			t.Errorf("expected vm999's net0 to be absorbed into the collapsed group, still rendered: %+v", n)
		}
	}
}
