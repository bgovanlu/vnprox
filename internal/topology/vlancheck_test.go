package topology_test

import (
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// buildVlanCheckGraph builds a one-node graph with a VLAN-aware bridge
// (expecting vids 10-30) with one port NIC carrying the given LldpNeighbor
// (the switch's advertised VLANs on that port).
func buildVlanCheckGraph(t *testing.T, n *inventory.LldpNeighbor) inventory.Snapshot {
	t.Helper()
	g := inventory.NewGraph()
	nicRef := inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno1"}
	bridgeRef := inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr1"}
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: "pve1"}, []inventory.Entity{
		&inventory.PhysNic{Ref: nicRef, Name: "eno1"},
		&inventory.Bridge{
			Ref: bridgeRef, Name: "vmbr1", Virt: inventory.BridgeLinux,
			VlanAware: true, VlanAwareSet: true,
			Vids:      []inventory.VidRange{{Low: 10, High: 30}},
			PortNames: []string{"eno1"},
		},
	})
	g.ApplyPoll(inventory.SourceHostLLDP, inventory.Scope{Node: "pve1"}, []inventory.Entity{n})
	return g.Snapshot()
}

// TestVlanFindings_Matching is T-302 AC2's clean-match golden case: switch
// advertises exactly what the bridge expects.
func TestVlanFindings_Matching(t *testing.T) {
	now := time.Now()
	n := &inventory.LldpNeighbor{
		Ref:        inventory.Ref{Kind: inventory.KindLldpNeighbor, Node: "pve1", ID: "eno1/sw1/Gi1"},
		LocalIface: "eno1", Node: "pve1", ChassisID: "sw1", PortID: "Gi1/0/14",
		VLAN: 10, TaggedVLANs: []int{11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30},
		TTL: 120, LastSeen: now.Unix(),
	}
	snap := buildVlanCheckGraph(t, n)
	findings := topology.VlanFindings(snap)
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.Code != topology.VlanCheckOK {
		t.Errorf("code = %q, want %q", f.Code, topology.VlanCheckOK)
	}
	if f.Severity != "info" {
		t.Errorf("severity = %q, want info", f.Severity)
	}
	if len(f.Missing) != 0 || len(f.Extra) != 0 {
		t.Errorf("missing/extra = %v/%v, want both empty", f.Missing, f.Extra)
	}
}

// TestVlanFindings_MissingOnSwitch is T-302 AC2's golden case matching the
// spec's own example: "bridge vmbr1 is VLAN-aware for 10-30 but switch port
// Gi1/0/14 advertises only 10,20."
func TestVlanFindings_MissingOnSwitch(t *testing.T) {
	now := time.Now()
	n := &inventory.LldpNeighbor{
		Ref:        inventory.Ref{Kind: inventory.KindLldpNeighbor, Node: "pve1", ID: "eno1/sw1/Gi1"},
		LocalIface: "eno1", Node: "pve1", ChassisID: "sw1", PortID: "Gi1/0/14",
		VLAN: 10, TaggedVLANs: []int{20},
		TTL: 120, LastSeen: now.Unix(),
	}
	snap := buildVlanCheckGraph(t, n)
	findings := topology.VlanFindings(snap)
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.Code != topology.VlanCheckMissingOnSwitch {
		t.Errorf("code = %q, want %q", f.Code, topology.VlanCheckMissingOnSwitch)
	}
	if f.Severity != "warning" {
		t.Errorf("severity = %q, want warning", f.Severity)
	}
	wantMsg := "bridge vmbr1 is VLAN-aware for 10-30 but switch port Gi1/0/14 advertises only 10,20"
	if f.Message != wantMsg {
		t.Errorf("message = %q, want %q", f.Message, wantMsg)
	}
	if len(f.Missing) != 19 { // 11..30 minus 20 = 19 vids
		t.Errorf("missing count = %d, want 19: %v", len(f.Missing), f.Missing)
	}
	if len(f.Extra) != 0 {
		t.Errorf("extra = %v, want empty", f.Extra)
	}
}

// TestVlanFindings_MissingOnBridge is T-302 AC2's inverse golden case: the
// switch advertises a VLAN the bridge is not configured to expect.
func TestVlanFindings_MissingOnBridge(t *testing.T) {
	now := time.Now()
	n := &inventory.LldpNeighbor{
		Ref:        inventory.Ref{Kind: inventory.KindLldpNeighbor, Node: "pve1", ID: "eno1/sw1/Gi1"},
		LocalIface: "eno1", Node: "pve1", ChassisID: "sw1", PortID: "Gi1/0/14",
		VLAN: 10, TaggedVLANs: append([]int{99}, allVids(11, 30)...),
		TTL: 120, LastSeen: now.Unix(),
	}
	snap := buildVlanCheckGraph(t, n)
	findings := topology.VlanFindings(snap)
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.Code != topology.VlanCheckMissingOnBridge {
		t.Errorf("code = %q, want %q", f.Code, topology.VlanCheckMissingOnBridge)
	}
	if f.Severity != "warning" {
		t.Errorf("severity = %q, want warning", f.Severity)
	}
	if len(f.Extra) != 1 || f.Extra[0] != 99 {
		t.Errorf("extra = %v, want [99]", f.Extra)
	}
	if len(f.Missing) != 0 {
		t.Errorf("missing = %v, want empty (bridge's full expected range is advertised)", f.Missing)
	}
}

func allVids(low, high int) []int {
	out := make([]int, 0, high-low+1)
	for v := low; v <= high; v++ {
		out = append(out, v)
	}
	return out
}
