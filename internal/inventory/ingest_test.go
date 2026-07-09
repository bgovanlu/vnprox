package inventory

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// TestIngestNetlink checks the host.LinkState adapter produces runtime-tagged
// entities of the right kinds.
func TestIngestNetlink(t *testing.T) {
	links := []host.LinkState{
		{Kind: "physical", Name: "eno1", Mac: "aa:bb:cc:dd:ee:01", Driver: "ixgbe", SpeedMbps: 10000, Duplex: "full", MTU: 1500, LinkUp: true},
		{Kind: "bond", Name: "bond0", Members: []string{"eno1", "eno2"}, MTU: 1500, Bond: &host.BondDetail{Mode: "802.3ad", ActiveSlave: "eno1", MIIStatus: "up", Slaves: []host.BondSlave{{Name: "eno1", Active: true, MIIStatus: "up"}}}},
		{Kind: "bridge", Name: "vmbr0", Members: []string{"bond0"}, MTU: 1500, Bridge: &host.BridgeDetail{VlanAware: true, STP: false, VLANs: []host.VidRange{{Low: 2, High: 4094}}}},
		{Kind: "vlan", Name: "vmbr0.100", VlanParent: "vmbr0", VlanID: 100, MTU: 1500},
		{Kind: "veth", Name: "veth99"}, // skipped
	}
	ents := FromNetlinkLinks("pve1", links)
	byKind := map[Kind]int{}
	for _, e := range ents {
		byKind[e.GetRef().Kind]++
	}
	if byKind[KindPhysNic] != 1 || byKind[KindBond] != 1 || byKind[KindBridge] != 1 || byKind[KindVlan] != 1 {
		t.Fatalf("unexpected kind counts: %v", byKind)
	}
	if len(ents) != 4 {
		t.Fatalf("veth should be skipped, got %d entities", len(ents))
	}

	g := NewGraph()
	g.ApplyPoll(SourceHostNetlink, Scope{Node: "pve1"}, ents)
	bond := mustGet[*Bond](t, g.Snapshot(), Ref{Kind: KindBond, Node: "pve1", ID: "bond0"})
	if bond.Mode != "802.3ad" || bond.ActiveSlave != "eno1" || len(bond.SlaveDetail) != 1 {
		t.Errorf("bond runtime detail not mapped: %+v", bond)
	}
	br := mustGet[*Bridge](t, g.Snapshot(), Ref{Kind: KindBridge, Node: "pve1", ID: "vmbr0"})
	if !br.VlanAware || len(br.Vids) != 1 || br.Vids[0] != (VidRange{Low: 2, High: 4094}) {
		t.Errorf("bridge runtime detail not mapped: %+v", br)
	}
}

// TestIngestPVENetworkMerge checks the pve.NetworkInterface adapter yields
// declared-tagged entities that merge cleanly with netlink runtime data on
// the same Refs.
func TestIngestPVENetworkMerge(t *testing.T) {
	g := NewGraph()
	node := "pve1"
	g.ApplyPoll(SourceHostNetlink, Scope{Node: node}, FromNetlinkLinks(node, []host.LinkState{
		{Kind: "bridge", Name: "vmbr0", MTU: 1500, Bridge: &host.BridgeDetail{VlanAware: true}},
	}))
	g.ApplyPoll(SourcePVENetwork, Scope{Node: node}, FromPVENetwork(node, []pve.NetworkInterface{
		{Iface: "vmbr0", Type: "bridge", MTU: 9000, BridgeVlanAware: true, Comments: "uplink bridge", Address: "10.0.0.2/24", Gateway: "10.0.0.1"},
	}))
	br := mustGet[*Bridge](t, g.Snapshot(), Ref{Kind: KindBridge, Node: node, ID: "vmbr0"})
	if br.MTU != 1500 {
		t.Errorf("runtime MTU = %d, want 1500", br.MTU)
	}
	if br.MTUDeclared != 9000 {
		t.Errorf("declared MTU = %d, want 9000", br.MTUDeclared)
	}
	if br.Comments != "uplink bridge" || br.Gateway != "10.0.0.1" {
		t.Errorf("declared metadata not merged: %+v", br)
	}
}

// TestIngestGuestConfig checks the guest NIC config parser.
func TestIngestGuestConfig(t *testing.T) {
	resources := []pve.ClusterResource{
		{Type: "qemu", Node: "pve1", VMID: 100, Name: "web", Status: "running"},
	}
	configs := map[int]map[string]string{
		100: {
			"net0":   "virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0,tag=20,rate=100,firewall=1",
			"net1":   "e1000=11:22:33:44:55:66,bridge=vnet5,link_down=1",
			"memory": "2048", // ignored
		},
	}
	ents := FromPVEGuests(resources, configs)
	var nics []*GuestNic
	for _, e := range ents {
		if n, ok := e.(*GuestNic); ok {
			nics = append(nics, n)
		}
	}
	if len(nics) != 2 {
		t.Fatalf("want 2 nics, got %d", len(nics))
	}
	byKey := map[string]*GuestNic{}
	for _, n := range nics {
		byKey[n.Key] = n
	}
	net0 := byKey["net0"]
	if net0.Model != "virtio" || net0.Mac != "AA:BB:CC:DD:EE:FF" || net0.TargetName != "vmbr0" || net0.Vid != 20 || net0.RateMbps != 100 || !net0.Firewall {
		t.Errorf("net0 parsed wrong: %+v", net0)
	}
	net1 := byKey["net1"]
	if net1.Model != "e1000" || net1.TargetName != "vnet5" || !net1.LinkDown {
		t.Errorf("net1 parsed wrong: %+v", net1)
	}
}

// TestIngestLLDP checks the LLDP JSON adapter.
func TestIngestLLDP(t *testing.T) {
	raw := []byte(`[{"local-iface":"eno1","chassis_name":"sw-access-01","chassis_id":"00:11:22:33:44:55","port_id":"Gi0/1","vlan":10,"ttl":120}]`)
	ents, err := FromLLDP("pve1", raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		t.Fatalf("want 1 neighbor, got %d", len(ents))
	}
	n := ents[0].(*LldpNeighbor)
	if n.ChassisName != "sw-access-01" || n.LocalIface != "eno1" || n.VLAN != 10 {
		t.Errorf("lldp parsed wrong: %+v", n)
	}

	// End to end: the neighbor links to its local NIC.
	g := NewGraph()
	nicRef := Ref{Kind: KindPhysNic, Node: "pve1", ID: "eno1"}
	g.ApplyPoll(SourceHostNetlink, Scope{Node: "pve1"}, []Entity{&PhysNic{Ref: nicRef, Name: "eno1"}})
	g.ApplyPoll(SourceHostLLDP, Scope{Node: "pve1"}, ents)
	snap := g.Snapshot()
	got := mustGet[*LldpNeighbor](t, snap, n.GetRef())
	if got.LocalNic != nicRef {
		t.Errorf("LocalNic = %v, want %v", got.LocalNic, nicRef)
	}
	if _, ok := findEdge(snap.Edges(), nicRef, n.GetRef(), EdgeLldpAdjacent); !ok {
		t.Errorf("missing lldp-adjacent edge")
	}
}
