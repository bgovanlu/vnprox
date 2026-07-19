package ceph_test

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/ceph"
	"github.com/bgovanlu/vnprox/internal/host"
)

// TestFindings_CleanTopologyStaysSilent is T-1503 AC3's "silent against the
// clean fixture" half, exercised against all three checks at once.
func TestFindings_CleanTopologyStaysSilent(t *testing.T) {
	snap, status := buildSnapshotAndStatus(t, fixtureCephClean)
	overlay := ceph.Project(snap, status)

	// pve1's mgmt bridge (vmbr0, 10.10.0.11) carries corosync's ring0
	// address in this fixture's topology — clean.yaml deliberately keeps
	// that bond isolated from the Ceph cluster network's own dedicated
	// bond2, so this must stay silent.
	cor := &host.CorosyncConfig{Nodes: []host.CorosyncNode{
		{Name: "pve1", RingAddrs: []string{"10.10.0.11"}},
		{Name: "pve2", RingAddrs: []string{"10.10.0.12"}},
	}}

	if got := ceph.CorosyncSharedLink(snap, overlay, cor); len(got) != 0 {
		t.Errorf("CorosyncSharedLink on clean topology = %+v, want none", got)
	}
	if got := ceph.ClusterMTUMismatch(overlay); len(got) != 0 {
		t.Errorf("ClusterMTUMismatch on clean topology = %+v, want none", got)
	}
	if got := ceph.SingleNIC(overlay); len(got) != 0 {
		t.Errorf("SingleNIC on clean topology = %+v, want none", got)
	}
}

func TestFindings_CorosyncSharedLink(t *testing.T) {
	snap, status := buildSnapshotAndStatus(t, fixtureCephCorosyncSharedLink)
	overlay := ceph.Project(snap, status)

	cor := &host.CorosyncConfig{Nodes: []host.CorosyncNode{
		{Name: "pve1", RingAddrs: []string{"10.10.0.11"}},
		{Name: "pve2", RingAddrs: []string{"10.10.0.12"}},
	}}

	got := ceph.CorosyncSharedLink(snap, overlay, cor)
	if len(got) != 2 {
		t.Fatalf("CorosyncSharedLink = %d findings, want 2 (one per node): %+v", len(got), got)
	}
	for i, node := range []string{"pve1", "pve2"} {
		if got[i].Check != ceph.CheckCorosyncSharedLink {
			t.Errorf("finding[%d].Check = %s, want %s", i, got[i].Check, ceph.CheckCorosyncSharedLink)
		}
		if len(got[i].Nodes) != 1 || got[i].Nodes[0] != node {
			t.Errorf("finding[%d].Nodes = %v, want [%s]", i, got[i].Nodes, node)
		}
	}

	// The other two checks must stay silent against this fixture — this is
	// a single-footgun fixture.
	if got := ceph.ClusterMTUMismatch(overlay); len(got) != 0 {
		t.Errorf("ClusterMTUMismatch on corosync-shared-link topology = %+v, want none", got)
	}
	if got := ceph.SingleNIC(overlay); len(got) != 0 {
		t.Errorf("SingleNIC on corosync-shared-link topology = %+v, want none", got)
	}
}

// TestFindings_CorosyncSharedLink_NilConfigStaysSilent confirms a nil
// *host.CorosyncConfig (no readable corosync.conf) degrades to silence,
// never a false positive or a panic.
func TestFindings_CorosyncSharedLink_NilConfigStaysSilent(t *testing.T) {
	snap, status := buildSnapshotAndStatus(t, fixtureCephCorosyncSharedLink)
	overlay := ceph.Project(snap, status)
	if got := ceph.CorosyncSharedLink(snap, overlay, nil); len(got) != 0 {
		t.Errorf("CorosyncSharedLink(nil cor) = %+v, want none", got)
	}
}

func TestFindings_ClusterMTUMismatch(t *testing.T) {
	snap, status := buildSnapshotAndStatus(t, fixtureCephMTUMismatch)
	overlay := ceph.Project(snap, status)

	got := ceph.ClusterMTUMismatch(overlay)
	if len(got) != 1 {
		t.Fatalf("ClusterMTUMismatch = %d findings, want 1: %+v", len(got), got)
	}
	f := got[0]
	if f.Check != ceph.CheckClusterMTUMismatch {
		t.Errorf("Check = %s, want %s", f.Check, ceph.CheckClusterMTUMismatch)
	}
	if len(f.Nodes) != 2 {
		t.Errorf("Nodes = %v, want both pve1 and pve2", f.Nodes)
	}
	if !strings.Contains(f.Detail, "9000") || !strings.Contains(f.Detail, "1500") {
		t.Errorf("Detail = %q, want it to name both the 9000 and 1500 MTU values", f.Detail)
	}

	// Other checks stay silent against this single-footgun fixture.
	cor := &host.CorosyncConfig{Nodes: []host.CorosyncNode{
		{Name: "pve1", RingAddrs: []string{"10.10.0.11"}},
		{Name: "pve2", RingAddrs: []string{"10.10.0.12"}},
	}}
	if got := ceph.CorosyncSharedLink(snap, overlay, cor); len(got) != 0 {
		t.Errorf("CorosyncSharedLink on mtu-mismatch topology = %+v, want none", got)
	}
	if got := ceph.SingleNIC(overlay); len(got) != 0 {
		t.Errorf("SingleNIC on mtu-mismatch topology = %+v, want none", got)
	}
}

func TestFindings_SingleNIC(t *testing.T) {
	snap, status := buildSnapshotAndStatus(t, fixtureCephSingleNIC)
	overlay := ceph.Project(snap, status)

	got := ceph.SingleNIC(overlay)
	if len(got) != 1 {
		t.Fatalf("SingleNIC = %d findings, want 1: %+v", len(got), got)
	}
	f := got[0]
	if f.Check != ceph.CheckSingleNIC {
		t.Errorf("Check = %s, want %s", f.Check, ceph.CheckSingleNIC)
	}
	if len(f.Nodes) != 1 || f.Nodes[0] != "pve1" {
		t.Errorf("Nodes = %v, want [pve1] only (pve2 is cleanly bonded)", f.Nodes)
	}

	// Other checks stay silent against this single-footgun fixture.
	if got := ceph.ClusterMTUMismatch(overlay); len(got) != 0 {
		t.Errorf("ClusterMTUMismatch on single-nic topology = %+v, want none", got)
	}
}
