package inventory

import "testing"

func findEdge(edges []Edge, from, to Ref, kind EdgeKind) (Edge, bool) {
	for _, e := range edges {
		if e.From == from && e.To == to && e.Kind == kind {
			return e, true
		}
	}
	return Edge{}, false
}

// TestGuestAttachPlainBridge checks a guest NIC attached to a plain bridge
// resolves to that bridge Ref, carries the guest's own tag as the effective
// VLAN, and produces an attached-to edge.
func TestGuestAttachPlainBridge(t *testing.T) {
	g := NewGraph()
	node := "pve1"
	vmbr0 := Ref{Kind: KindBridge, Node: node, ID: "vmbr0"}
	nicRef := Ref{Kind: KindGuestNic, Node: node, ID: "100/net0"}

	g.ApplyPoll(SourceHostNetlink, Scope{Node: node}, []Entity{
		&Bridge{Ref: vmbr0, Name: "vmbr0", VlanAware: true},
	})
	g.ApplyPoll(SourcePVEGuest, Scope{Kinds: []Kind{KindGuest, KindGuestNic}}, []Entity{
		&Guest{Ref: Ref{Kind: KindGuest, Node: node, ID: "100"}, VMID: 100, Node: node},
		&GuestNic{Ref: nicRef, Guest: Ref{Kind: KindGuest, Node: node, ID: "100"}, Key: "net0", TargetName: "vmbr0", Vid: 20},
	})

	snap := g.Snapshot()
	e, ok := snap.Get(nicRef)
	if !ok {
		t.Fatal("guest nic missing")
	}
	nic := e.(*GuestNic)
	if nic.BridgeOrVnet != vmbr0 {
		t.Errorf("BridgeOrVnet = %v, want %v", nic.BridgeOrVnet, vmbr0)
	}
	if nic.EffectiveVid != 20 {
		t.Errorf("EffectiveVid = %d, want 20", nic.EffectiveVid)
	}
	if _, ok := findEdge(snap.Edges(), nicRef, vmbr0, EdgeAttachedTo); !ok {
		t.Errorf("missing attached-to edge to bridge")
	}
}

// TestGuestAttachVNetTagPropagation checks a guest NIC attached to an SDN
// VNet resolves to the VNet Ref and takes the VNet's tag as the effective
// (fabric) VLAN — the key VLAN-propagation case.
func TestGuestAttachVNetTagPropagation(t *testing.T) {
	g := NewGraph()
	node := "pve1"
	zoneRef := Ref{Kind: KindSDNZone, ID: "zone1"}
	vnetRef := Ref{Kind: KindSDNVnet, ID: "zone1/vnet10"}
	nicRef := Ref{Kind: KindGuestNic, Node: node, ID: "101/net0"}

	g.ApplyPoll(SourcePVESDN, Scope{}, []Entity{
		&SdnZone{Ref: zoneRef, ID: "zone1", Type: "vlan", Bridge: "vmbr9"},
		&SdnVnet{Ref: vnetRef, ID: "vnet10", Zone: "zone1", Tag: 30},
	})
	g.ApplyPoll(SourcePVEGuest, Scope{Kinds: []Kind{KindGuest, KindGuestNic}}, []Entity{
		&Guest{Ref: Ref{Kind: KindGuest, Node: node, ID: "101"}, VMID: 101, Node: node},
		// The guest config attaches to the VNet by its bare name; the guest's
		// own tag (5) is an inner tag, overridden by the VNet's fabric tag.
		&GuestNic{Ref: nicRef, Guest: Ref{Kind: KindGuest, Node: node, ID: "101"}, Key: "net0", TargetName: "vnet10", Vid: 5},
	})

	snap := g.Snapshot()
	nic := mustGet[*GuestNic](t, snap, nicRef)
	if nic.BridgeOrVnet != vnetRef {
		t.Errorf("BridgeOrVnet = %v, want %v", nic.BridgeOrVnet, vnetRef)
	}
	if nic.EffectiveVid != 30 {
		t.Errorf("EffectiveVid = %d, want 30 (VNet tag propagated)", nic.EffectiveVid)
	}
	if _, ok := findEdge(snap.Edges(), nicRef, vnetRef, EdgeAttachedTo); !ok {
		t.Errorf("missing attached-to edge to VNet")
	}
}

// TestVNetRealizesBridge checks realizes edges from a VNet to the per-node
// bridge named by its zone.
func TestVNetRealizesBridge(t *testing.T) {
	g := NewGraph()
	zoneRef := Ref{Kind: KindSDNZone, ID: "zone1"}
	vnetRef := Ref{Kind: KindSDNVnet, ID: "zone1/vnet10"}
	brA := Ref{Kind: KindBridge, Node: "pve1", ID: "vmbr9"}
	brB := Ref{Kind: KindBridge, Node: "pve2", ID: "vmbr9"}

	g.ApplyPoll(SourcePVESDN, Scope{}, []Entity{
		&SdnZone{Ref: zoneRef, ID: "zone1", Type: "vlan", Bridge: "vmbr9"},
		&SdnVnet{Ref: vnetRef, ID: "vnet10", Zone: "zone1", Tag: 30},
	})
	g.ApplyPoll(SourceHostNetlink, Scope{Node: "pve1"}, []Entity{&Bridge{Ref: brA, Name: "vmbr9"}})
	g.ApplyPoll(SourceHostNetlink, Scope{Node: "pve2"}, []Entity{&Bridge{Ref: brB, Name: "vmbr9"}})

	snap := g.Snapshot()
	for _, br := range []Ref{brA, brB} {
		if _, ok := findEdge(snap.Edges(), vnetRef, br, EdgeRealizes); !ok {
			t.Errorf("missing realizes edge %v -> %v", vnetRef, br)
		}
	}
}

// TestVlanAndBondEdges checks tagged-on, port-of, and enslaved-by edges.
func TestL2Edges(t *testing.T) {
	g := NewGraph()
	node := "pve1"
	eno1 := Ref{Kind: KindPhysNic, Node: node, ID: "eno1"}
	eno2 := Ref{Kind: KindPhysNic, Node: node, ID: "eno2"}
	bond0 := Ref{Kind: KindBond, Node: node, ID: "bond0"}
	vmbr0 := Ref{Kind: KindBridge, Node: node, ID: "vmbr0"}
	vlan := Ref{Kind: KindVlan, Node: node, ID: "vmbr0.100"}

	g.ApplyPoll(SourceHostNetlink, Scope{Node: node}, []Entity{
		&PhysNic{Ref: eno1, Name: "eno1"},
		&PhysNic{Ref: eno2, Name: "eno2"},
		&Bond{Ref: bond0, Name: "bond0", Slaves: []string{"eno1", "eno2"}},
		&Bridge{Ref: vmbr0, Name: "vmbr0", PortNames: []string{"bond0"}},
		&VlanIface{Ref: vlan, Name: "vmbr0.100", ParentName: "vmbr0", Vid: 100},
	})
	snap := g.Snapshot()
	if _, ok := findEdge(snap.Edges(), eno1, bond0, EdgeEnslavedBy); !ok {
		t.Error("missing enslaved-by eno1->bond0")
	}
	if _, ok := findEdge(snap.Edges(), bond0, vmbr0, EdgePortOf); !ok {
		t.Error("missing port-of bond0->vmbr0")
	}
	if _, ok := findEdge(snap.Edges(), vlan, vmbr0, EdgeTaggedOn); !ok {
		t.Error("missing tagged-on vmbr0.100->vmbr0")
	}
	// Bridge.Ports should be resolved to the bond Ref.
	br := mustGet[*Bridge](t, snap, vmbr0)
	if len(br.Ports) != 1 || br.Ports[0] != bond0 {
		t.Errorf("bridge ports = %v, want [%v]", br.Ports, bond0)
	}
}

func mustGet[T Entity](t *testing.T, snap Snapshot, ref Ref) T {
	t.Helper()
	e, ok := snap.Get(ref)
	if !ok {
		t.Fatalf("entity %v missing", ref)
	}
	v, ok := e.(T)
	if !ok {
		t.Fatalf("entity %v is %T, want %T", ref, e, *new(T))
	}
	return v
}
