package failsim

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// sharedSwitchCluster builds an n-node cluster with dual corosync rings
// (ring0 on vmbr0/eno1, ring1 on vmbr1/eno2). When shareSwitch is true every
// node's eno1 AND eno2 face the same switch "sw-core" — the classic
// false-redundancy SPOF where "one switch takes down both corosync links".
func sharedSwitchCluster(nodes int, shareSwitch bool) (inventory.Snapshot, *host.CorosyncConfig) {
	w := newWorld()
	var cor host.CorosyncConfig
	for i := 1; i <= nodes; i++ {
		n := "pve" + itoa(i)
		ring0 := "10.10.0." + itoa(i)
		ring1 := "10.20.0." + itoa(i)
		w.node(n, "10.0.0."+itoa(i))
		w.physnic(n, "eno1", true).physnic(n, "eno2", true)
		w.bridge(n, "vmbr0", []string{ring0 + "/24"}, "eno1")
		w.bridge(n, "vmbr1", []string{ring1 + "/24"}, "eno2")
		if shareSwitch {
			w.lldp(n, "eno1", "sw-core").lldp(n, "eno2", "sw-core")
		}
		cor.Nodes = append(cor.Nodes, host.CorosyncNode{Name: n, RingAddrs: []string{ring0, ring1}, NodeID: i})
	}
	return w.build(), &cor
}

// TestSimulate_QuorumRisk covers AC2's positive case: removing the shared
// switch strands every node's corosync rings, dropping reachable voters below
// floor(N/2)+1.
func TestSimulate_QuorumRisk_SharedSwitch(t *testing.T) {
	snap, cor := sharedSwitchCluster(3, true)
	in := Input{Snapshot: snap, Corosync: cor}

	im := Simulate(in, inventory.Ref{Kind: inventory.KindSwitchPort, ID: "sw-core"})
	if !im.QuorumRisk {
		t.Fatalf("removing the shared switch must set QuorumRisk (voters drop to 0 of needed %d)", 3/2+1)
	}
	if im.Severity != SeverityCritical {
		t.Errorf("Severity = %q, want critical", im.Severity)
	}
}

// TestSimulate_QuorumSafe covers AC2's negative case: removing one node from a
// 5-node cluster with redundant rings keeps quorum (4 >= 3).
func TestSimulate_QuorumSafe_NodeRemoval(t *testing.T) {
	snap, cor := sharedSwitchCluster(5, false)
	in := Input{Snapshot: snap, Corosync: cor}

	im := Simulate(in, nodeRef("pve1"))
	if im.QuorumRisk {
		t.Fatalf("removing 1 of 5 nodes must not risk quorum (4 reachable >= %d needed)", 5/2+1)
	}
	// A single-ring loss (one bond/NIC) likewise leaves the node reachable via
	// its other ring.
	im2 := Simulate(in, inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve2", ID: "eno1"})
	if im2.QuorumRisk {
		t.Error("losing one ring NIC on a dual-ring node must not risk quorum")
	}
}

// TestSimulate_QuorumNotEvaluated covers the honesty degrade: a voting node
// whose ring address resolves to no interface makes quorum unknowable, so the
// dimension is reported not-evaluated rather than a confident boolean.
func TestSimulate_QuorumNotEvaluated_UnresolvableRing(t *testing.T) {
	snap, _ := sharedSwitchCluster(3, false)
	// Corosync names a ring address that matches no interface in the snapshot.
	cor := &host.CorosyncConfig{Nodes: []host.CorosyncNode{
		{Name: "pve1", RingAddrs: []string{"192.168.99.1"}, NodeID: 1},
		{Name: "pve2", RingAddrs: []string{"192.168.99.2"}, NodeID: 2},
		{Name: "pve3", RingAddrs: []string{"192.168.99.3"}, NodeID: 3},
	}}
	im := Simulate(Input{Snapshot: snap, Corosync: cor}, nodeRef("pve1"))
	if im.QuorumRisk {
		t.Error("QuorumRisk must not be a confident true when ring addresses are unresolvable")
	}
	if !hasStr(im.NotEvaluated, DimQuorum) {
		t.Errorf("NotEvaluated = %v, want to contain %q (unresolvable ring)", im.NotEvaluated, DimQuorum)
	}
}
