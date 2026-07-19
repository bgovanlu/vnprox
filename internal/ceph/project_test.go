package ceph_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/ceph"
)

// TestProject_CleanTopology is T-1503 AC1's golden projection test: against
// clean.yaml's dedicated, bonded public/cluster networks, Project resolves
// each OSD-hosting node's carrier, physical path, and "riding" bond
// correctly, and denormalizes that attribution onto every OSD.
func TestProject_CleanTopology(t *testing.T) {
	snap, status := buildSnapshotAndStatus(t, fixtureCephClean)

	if status.PublicNetwork != "10.20.0.0/24" || status.ClusterNetwork != "10.30.0.0/24" {
		t.Fatalf("unexpected discovered status: %+v", status)
	}
	if len(status.OSDs) != 4 {
		t.Fatalf("discovered %d OSDs, want 4", len(status.OSDs))
	}

	overlay := ceph.Project(snap, status)

	if overlay.PublicNetwork != "10.20.0.0/24" || overlay.ClusterNetwork != "10.30.0.0/24" {
		t.Fatalf("overlay network CIDRs = %q/%q, want 10.20.0.0/24 / 10.30.0.0/24", overlay.PublicNetwork, overlay.ClusterNetwork)
	}
	if len(overlay.Nodes) != 2 {
		t.Fatalf("overlay.Nodes = %d, want 2", len(overlay.Nodes))
	}

	byNode := map[string]ceph.NodeAttribution{}
	for _, na := range overlay.Nodes {
		byNode[na.Node] = na
	}

	for _, node := range []string{"pve1", "pve2"} {
		na, ok := byNode[node]
		if !ok {
			t.Fatalf("no NodeAttribution for %s", node)
		}
		if na.PublicCarrier.String() != "bridge:"+node+":vmbr1" {
			t.Errorf("%s PublicCarrier = %s, want bridge:%s:vmbr1", node, na.PublicCarrier, node)
		}
		if na.ClusterCarrier.String() != "bridge:"+node+":vmbr2" {
			t.Errorf("%s ClusterCarrier = %s, want bridge:%s:vmbr2", node, na.ClusterCarrier, node)
		}
		if na.PublicRidingOn.String() != "bond:"+node+":bond1" {
			t.Errorf("%s PublicRidingOn = %s, want bond:%s:bond1", node, na.PublicRidingOn, node)
		}
		if na.ClusterRidingOn.String() != "bond:"+node+":bond2" {
			t.Errorf("%s ClusterRidingOn = %s, want bond:%s:bond2", node, na.ClusterRidingOn, node)
		}
		if len(na.PublicNICs) != 2 || len(na.ClusterNICs) != 2 {
			t.Errorf("%s NIC counts = public %d, cluster %d, want 2/2", node, len(na.PublicNICs), len(na.ClusterNICs))
		}
		if !na.ClusterMTUKnown || na.ClusterMTU != 9000 {
			t.Errorf("%s ClusterMTU = %d (known=%v), want 9000/true", node, na.ClusterMTU, na.ClusterMTUKnown)
		}
	}

	// Every OSD carries its node's resolved bond attribution.
	if len(overlay.OSDs) != 4 {
		t.Fatalf("overlay.OSDs = %d, want 4", len(overlay.OSDs))
	}
	for _, oa := range overlay.OSDs {
		want := byNode[oa.OSD.Node]
		if oa.PublicBond != want.PublicRidingOn {
			t.Errorf("OSD %d (%s) PublicBond = %s, want %s", oa.OSD.ID, oa.OSD.Node, oa.PublicBond, want.PublicRidingOn)
		}
		if oa.ClusterBond != want.ClusterRidingOn {
			t.Errorf("OSD %d (%s) ClusterBond = %s, want %s", oa.OSD.ID, oa.OSD.Node, oa.ClusterBond, want.ClusterRidingOn)
		}
	}
}

// TestProject_SingleNICTopology confirms Project resolves pve1's bare,
// unbonded NIC correctly (both networks riding the same sole terminal
// PhysNic, no Bond in either path) while pve2's dedicated bonded setup
// resolves independently and correctly in the same overlay.
func TestProject_SingleNICTopology(t *testing.T) {
	snap, status := buildSnapshotAndStatus(t, fixtureCephSingleNIC)
	overlay := ceph.Project(snap, status)

	byNode := map[string]ceph.NodeAttribution{}
	for _, na := range overlay.Nodes {
		byNode[na.Node] = na
	}

	pve1 := byNode["pve1"]
	if len(pve1.PublicNICs) != 1 || len(pve1.ClusterNICs) != 1 {
		t.Fatalf("pve1 NIC counts = public %d, cluster %d, want 1/1", len(pve1.PublicNICs), len(pve1.ClusterNICs))
	}
	if pve1.PublicNICs[0] != pve1.ClusterNICs[0] {
		t.Errorf("pve1 public/cluster NIC = %s / %s, want identical", pve1.PublicNICs[0], pve1.ClusterNICs[0])
	}
	if pve1.PublicNICs[0].String() != "physnic:pve1:eno1" {
		t.Errorf("pve1 riding NIC = %s, want physnic:pve1:eno1", pve1.PublicNICs[0])
	}
	if pve1.PublicRidingOn != pve1.PublicNICs[0] {
		t.Errorf("pve1 PublicRidingOn = %s, want the bare NIC %s (no bond)", pve1.PublicRidingOn, pve1.PublicNICs[0])
	}

	pve2 := byNode["pve2"]
	if pve2.PublicRidingOn.String() != "bond:pve2:bond1" || pve2.ClusterRidingOn.String() != "bond:pve2:bond2" {
		t.Errorf("pve2 riding refs = %s / %s, want bond1/bond2", pve2.PublicRidingOn, pve2.ClusterRidingOn)
	}
}
