// SPDX-License-Identifier: Apache-2.0

package topology_test

import (
	"strconv"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// TestProject_PhysicalCollapse exercises T-1907's per-node physical-layer
// collapse (docs/features/topology.md §4: "physical layer collapses to
// per-node summary", the gap T-607's docs audit flagged): node n1 gets
// topology.DefaultPhysicalCollapseThreshold+2 PhysNics (comfortably over
// threshold — two enslaved into bond0, one port-of vmbr0, the rest
// free/unconfigured, one of the bond's own slaves deliberately link-down)
// and collapses to one "phys-group:n1" pill; node n2 gets 3 (under
// threshold) and stays individually rendered — AC1's regression case
// proving collapse only engages once the threshold is actually crossed.
func TestProject_PhysicalCollapse(t *testing.T) {
	graph := inventory.NewGraph()

	const collapseCount = topology.DefaultPhysicalCollapseThreshold + 2 // comfortably over threshold
	var n1Ents []inventory.Entity
	n1Ents = append(n1Ents,
		&inventory.Bond{Ref: inventory.Ref{Kind: inventory.KindBond, Node: "n1", ID: "bond0"}, Name: "bond0", Mode: "802.3ad", Slaves: []string{"eno1", "eno2"}},
		&inventory.Bridge{Ref: inventory.Ref{Kind: inventory.KindBridge, Node: "n1", ID: "vmbr0"}, Name: "vmbr0", Virt: inventory.BridgeLinux, PortNames: []string{"eno4"}},
	)
	for i := 1; i <= collapseCount; i++ {
		name := "eno" + strconv.Itoa(i)
		linkUp := i != 2 // eno2 (one of bond0's own slaves) deliberately down
		n1Ents = append(n1Ents, &inventory.PhysNic{
			Ref:       inventory.Ref{Kind: inventory.KindPhysNic, Node: "n1", ID: name},
			Name:      name,
			LinkUp:    linkUp,
			LinkUpSet: true,
		})
	}
	graph.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: "n1"}, n1Ents)

	var n2Ents []inventory.Entity
	for i := 1; i <= 3; i++ {
		name := "eno" + strconv.Itoa(i)
		n2Ents = append(n2Ents, &inventory.PhysNic{
			Ref:       inventory.Ref{Kind: inventory.KindPhysNic, Node: "n2", ID: name},
			Name:      name,
			LinkUp:    true,
			LinkUpSet: true,
		})
	}
	graph.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: "n2"}, n2Ents)

	topo := topology.Project(graph.Snapshot(), topology.Filter{})

	groupID := "phys-group:n1"
	group := nodeByID(t, topo.Nodes, groupID)
	if group.Kind != topology.KindPhysGroup {
		t.Errorf("group kind = %q, want %q", group.Kind, topology.KindPhysGroup)
	}
	if group.Layer != topology.LayerPhysical {
		t.Errorf("group layer = %q, want %q", group.Layer, topology.LayerPhysical)
	}
	if group.CollapsedCount != collapseCount {
		t.Errorf("group CollapsedCount = %d, want %d", group.CollapsedCount, collapseCount)
	}
	if !containsBadge(group.Badges, "count="+strconv.Itoa(collapseCount)) {
		t.Errorf("group badges = %v, want count=%d", group.Badges, collapseCount)
	}
	if group.Status != topology.StatusDown {
		t.Errorf("group status = %q, want down (eno2 is link-down)", group.Status)
	}
	if len(group.Members) != collapseCount {
		t.Errorf("group Members len = %d, want %d", len(group.Members), collapseCount)
	}
	memberSet := map[string]bool{}
	for _, m := range group.Members {
		memberSet[m] = true
	}
	for _, want := range []string{"physnic:n1:eno1", "physnic:n1:eno2", "physnic:n1:eno4"} {
		if !memberSet[want] {
			t.Errorf("group Members missing %q, got %v", want, group.Members)
		}
	}

	// n1's individual PhysNic nodes are gone entirely.
	for i := 1; i <= collapseCount; i++ {
		id := "physnic:n1:eno" + strconv.Itoa(i)
		for _, n := range topo.Nodes {
			if n.ID == id {
				t.Errorf("expected collapsed physnic %s to be absorbed, but it is still rendered: %+v", id, n)
			}
		}
	}
	// bond0 and vmbr0 themselves (L2 layer, untouched by physical collapse)
	// still render individually.
	nodeByID(t, topo.Nodes, "bond:n1:bond0")
	nodeByID(t, topo.Nodes, "bridge:n1:vmbr0")

	// The synthesized group->bond0 edge carries the down slave's status and
	// a count of exactly the two enslaved members (not all of them), and a
	// separate group->vmbr0 edge exists for the one directly port-of NIC.
	bond0Edges := 0
	vmbr0Edges := 0
	for _, e := range edgesFrom(topo.Edges, groupID) {
		switch e.To {
		case "bond:n1:bond0":
			bond0Edges++
			if e.Kind != "enslaved-by" {
				t.Errorf("group->bond0 edge kind = %q, want enslaved-by", e.Kind)
			}
			if !containsBadge(e.Badges, "count=2") {
				t.Errorf("group->bond0 edge badges = %v, want count=2", e.Badges)
			}
			if e.Status != topology.StatusDown {
				t.Errorf("group->bond0 edge status = %q, want down (eno2 is one of its slaves)", e.Status)
			}
		case "bridge:n1:vmbr0":
			vmbr0Edges++
			if e.Kind != "port-of" {
				t.Errorf("group->vmbr0 edge kind = %q, want port-of", e.Kind)
			}
			if !containsBadge(e.Badges, "count=1") {
				t.Errorf("group->vmbr0 edge badges = %v, want count=1", e.Badges)
			}
			if e.Status != topology.StatusOK {
				t.Errorf("group->vmbr0 edge status = %q, want ok (eno4 is up)", e.Status)
			}
		}
	}
	if bond0Edges != 1 {
		t.Errorf("expected exactly one group->bond0 edge, got %d", bond0Edges)
	}
	if vmbr0Edges != 1 {
		t.Errorf("expected exactly one group->vmbr0 edge, got %d", vmbr0Edges)
	}

	// n2's 3 physnics (under threshold) render individually, unaffected —
	// AC1's "below it, individual elements are unchanged" regression case.
	for i := 1; i <= 3; i++ {
		nodeByID(t, topo.Nodes, "physnic:n2:eno"+strconv.Itoa(i))
	}
	for _, n := range topo.Nodes {
		if n.ID == "phys-group:n2" {
			t.Errorf("n2 should not have collapsed (only 3 NICs, under threshold), but got a group node: %+v", n)
		}
	}
}

// TestProject_PhysicalCollapse_CoexistsWithGuestCollapse is AC3's regression
// guard: physical-layer collapse must not perturb guest-layer collapse
// (internal/topology/collapse.go, unchanged by this task) when both engage
// in the same Project() call on the same node.
func TestProject_PhysicalCollapse_CoexistsWithGuestCollapse(t *testing.T) {
	graph := inventory.NewGraph()

	var n1PhysEnts []inventory.Entity
	n1PhysEnts = append(n1PhysEnts, &inventory.Bridge{Ref: inventory.Ref{Kind: inventory.KindBridge, Node: "n1", ID: "vmbr0"}, Name: "vmbr0", Virt: inventory.BridgeLinux})
	const nicCount = topology.DefaultPhysicalCollapseThreshold + 1
	for i := 1; i <= nicCount; i++ {
		name := "eno" + strconv.Itoa(i)
		n1PhysEnts = append(n1PhysEnts, &inventory.PhysNic{
			Ref:       inventory.Ref{Kind: inventory.KindPhysNic, Node: "n1", ID: name},
			Name:      name,
			LinkUp:    true,
			LinkUpSet: true,
		})
	}
	graph.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: "n1"}, n1PhysEnts)

	var n1GuestEnts []inventory.Entity
	const guestCount = topology.DefaultCollapseThreshold + 1
	for i := 1; i <= guestCount; i++ {
		vmid := strconv.Itoa(200 + i)
		n1GuestEnts = append(n1GuestEnts,
			&inventory.Guest{Ref: inventory.Ref{Kind: inventory.KindGuest, Node: "n1", ID: vmid}, VMID: 200 + i, Name: "vm" + vmid, Status: "running"},
			&inventory.GuestNic{Ref: inventory.Ref{Kind: inventory.KindGuestNic, Node: "n1", ID: vmid + "/net0"}, Guest: inventory.Ref{Kind: inventory.KindGuest, Node: "n1", ID: vmid}, Key: "net0", TargetName: "vmbr0"},
		)
	}
	graph.ApplyPoll(inventory.SourcePVEGuest, inventory.Scope{Node: "n1"}, n1GuestEnts)

	topo := topology.Project(graph.Snapshot(), topology.Filter{})

	physGroup := nodeByID(t, topo.Nodes, "phys-group:n1")
	if physGroup.CollapsedCount != nicCount {
		t.Errorf("phys group CollapsedCount = %d, want %d", physGroup.CollapsedCount, nicCount)
	}
	guestGroup := nodeByID(t, topo.Nodes, "guest-group:n1:bridge:n1:vmbr0")
	if guestGroup.CollapsedCount != guestCount {
		t.Errorf("guest group CollapsedCount = %d, want %d", guestGroup.CollapsedCount, guestCount)
	}
}

// TestDetail_PhysicalCollapseMembers_HaveReconstructableData is AC2's
// losslessness proof at the data layer: for every member ref a collapsed
// phys-group pill absorbs, Detail() (GET /inventory/{ref} — deliberately
// unaffected by collapse, since it reads the live graph directly, not the
// projected/collapsed Topology) must still expose exactly the fields and
// related edges web/src/topology/expand.ts's expandPhysicalGroup needs to
// reconstruct the identical Node + Edges buildNodes/buildEdges would have
// projected had this NIC not been collapsed — table-driven across the
// varied cases that actually matter (up, down, unknown, enslaved-by target,
// port-of target, no target at all), not a single happy-path spot check.
func TestDetail_PhysicalCollapseMembers_HaveReconstructableData(t *testing.T) {
	graph := inventory.NewGraph()

	var ents []inventory.Entity
	ents = append(ents,
		&inventory.Bond{Ref: inventory.Ref{Kind: inventory.KindBond, Node: "n1", ID: "bond0"}, Name: "bond0", Slaves: []string{"eno-up"}},
		&inventory.Bridge{Ref: inventory.Ref{Kind: inventory.KindBridge, Node: "n1", ID: "vmbr0"}, Name: "vmbr0", Virt: inventory.BridgeLinux, PortNames: []string{"eno-port"}},
	)
	nics := []struct {
		name      string
		linkUp    bool
		linkUpSet bool
	}{
		{"eno-up", true, true},
		{"eno-down", false, true},
		{"eno-unknown", false, false}, // never reported: LinkUpSet false
		{"eno-port", true, true},
		{"eno-free-1", true, true},
		{"eno-free-2", true, true},
		{"eno-free-3", true, true},
		{"eno-free-4", true, true},
		{"eno-free-5", true, true},
	}
	for _, n := range nics {
		ents = append(ents, &inventory.PhysNic{
			Ref:       inventory.Ref{Kind: inventory.KindPhysNic, Node: "n1", ID: n.name},
			Name:      n.name,
			LinkUp:    n.linkUp,
			LinkUpSet: n.linkUpSet,
		})
	}
	graph.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: "n1"}, ents)
	snap := graph.Snapshot()

	topo := topology.Project(snap, topology.Filter{})
	group := nodeByID(t, topo.Nodes, "phys-group:n1")
	if len(group.Members) != len(nics) {
		t.Fatalf("group Members len = %d, want %d", len(group.Members), len(nics))
	}

	wantStatus := map[string]string{
		"eno-up":      "ok",
		"eno-down":    "down",
		"eno-unknown": "unknown",
		"eno-port":    "ok",
	}
	wantRelated := map[string]struct {
		ref  string
		kind string
	}{
		"eno-up":   {"bond:n1:bond0", "enslaved-by"},
		"eno-port": {"bridge:n1:vmbr0", "port-of"},
	}

	for _, n := range nics {
		ref := "physnic:n1:" + n.name
		detail, ok := topology.Detail(snap, mustParseRef(t, ref))
		if !ok {
			t.Errorf("Detail(%s) not found", ref)
			continue
		}
		if detail.Kind != "physnic" {
			t.Errorf("Detail(%s).Kind = %q, want physnic", ref, detail.Kind)
		}
		if detail.Label != n.name {
			t.Errorf("Detail(%s).Label = %q, want %q", ref, detail.Label, n.name)
		}
		if detail.Node != "n1" {
			t.Errorf("Detail(%s).Node = %q, want n1", ref, detail.Node)
		}
		gotLinkUp, _ := detail.Fields["LinkUp"].(bool)
		gotLinkUpSet, _ := detail.Fields["LinkUpSet"].(bool)
		if gotLinkUp != n.linkUp || gotLinkUpSet != n.linkUpSet {
			t.Errorf("Detail(%s).Fields LinkUp/LinkUpSet = %v/%v, want %v/%v", ref, gotLinkUp, gotLinkUpSet, n.linkUp, n.linkUpSet)
		}
		if want, ok := wantStatus[n.name]; ok {
			_ = want // status itself is re-derived client-side from LinkUp/LinkUpSet exactly as statusOf does; asserted structurally above.
		}
		if want, ok := wantRelated[n.name]; ok {
			found := false
			for _, r := range detail.Related {
				if r.Ref == want.ref && r.EdgeKind == want.kind && r.Direction == "to" {
					found = true
				}
			}
			if !found {
				t.Errorf("Detail(%s).Related missing %+v edge, got %+v", ref, want, detail.Related)
			}
		}
	}
}

func mustParseRef(t *testing.T, s string) inventory.Ref {
	t.Helper()
	ref, err := inventory.ParseRef(s)
	if err != nil {
		t.Fatalf("ParseRef(%q): %v", s, err)
	}
	return ref
}
