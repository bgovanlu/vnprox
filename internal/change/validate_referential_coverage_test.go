package change_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// populatedSnapshot builds an inventory snapshot with a small but complete
// single-node topology so the referential validators' existence/collision/
// enslavement branches (T-202) can be exercised against real entities.
func populatedSnapshot() inventory.Snapshot {
	g := inventory.NewGraph()
	pn := func(id string) inventory.Ref { return inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: id} }
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: "pve1"}, []inventory.Entity{
		&inventory.Node{Ref: inventory.Ref{Kind: inventory.KindNode, ID: "pve1"}, Name: "pve1", IP: "192.168.1.10"},
		&inventory.PhysNic{Ref: pn("eno1"), Name: "eno1"},
		&inventory.PhysNic{Ref: pn("eno2"), Name: "eno2"},
		&inventory.PhysNic{Ref: pn("eno3"), Name: "eno3"},
		&inventory.Bond{Ref: inventory.Ref{Kind: inventory.KindBond, Node: "pve1", ID: "bond0"}, Name: "bond0", DeclaredSlaves: []string{"eno3"}},
		&inventory.Bridge{
			Ref: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}, Name: "vmbr0",
			Ports: []inventory.Ref{pn("eno1")}, Addresses: []string{"192.168.1.10/24"},
		},
		&inventory.VlanIface{Ref: inventory.Ref{Kind: inventory.KindVlan, Node: "pve1", ID: "vmbr0.10"}, Name: "vmbr0.10", ParentName: "vmbr0", Vid: 10},
		&inventory.GuestNic{Ref: inventory.Ref{Kind: inventory.KindGuestNic, Node: "pve1", ID: "100/net0"}, Key: "net0"},
	})
	g.ApplyPoll(inventory.SourcePVESDN, inventory.Scope{}, []inventory.Entity{
		&inventory.SdnZone{Ref: inventory.Ref{Kind: inventory.KindSDNZone, ID: "zone1"}, ID: "zone1"},
		&inventory.SdnVnet{Ref: inventory.Ref{Kind: inventory.KindSDNVnet, ID: "zone1/vnet1"}, ID: "zone1/vnet1", Zone: "zone1"},
		&inventory.SdnSubnet{Ref: inventory.Ref{Kind: inventory.KindSDNSubnet, ID: "10.0.0.0/24"}, ID: "10.0.0.0/24", Vnet: "zone1/vnet1"},
	})
	return g.Snapshot()
}

func bridgeR(id string) inventory.Ref {
	return inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: id}
}
func bondR(id string) inventory.Ref {
	return inventory.Ref{Kind: inventory.KindBond, Node: "pve1", ID: id}
}
func vlanR(id string) inventory.Ref {
	return inventory.Ref{Kind: inventory.KindVlan, Node: "pve1", ID: id}
}
func physR(id string) inventory.Ref {
	return inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: id}
}
func zoneR(id string) inventory.Ref   { return inventory.Ref{Kind: inventory.KindSDNZone, ID: id} }
func vnetR(id string) inventory.Ref   { return inventory.Ref{Kind: inventory.KindSDNVnet, ID: id} }
func subnetR(id string) inventory.Ref { return inventory.Ref{Kind: inventory.KindSDNSubnet, ID: id} }

// TestReferentialValidate_AgainstPopulatedSnapshot exercises the referential
// validator's existence, duplicate-enslavement, collision, and SDN-parent
// branches by validating schema-clean ops (so referential actually runs)
// against a real topology. Each case asserts the presence or absence of an
// error finding.
func TestReferentialValidate_AgainstPopulatedSnapshot(t *testing.T) {
	snap := populatedSnapshot()

	cases := []struct {
		name    string
		op      change.Op
		wantErr bool
	}{
		{"iface.update existing", change.Op{Type: change.OpIfaceUpdate, Target: physR("eno1"), Params: &change.IfaceUpdateParams{MTU: ptrInt(1500)}}, false},
		{"iface.update missing", change.Op{Type: change.OpIfaceUpdate, Target: physR("ghost"), Params: &change.IfaceUpdateParams{MTU: ptrInt(1500)}}, true},
		{"iface.update addresses", change.Op{Type: change.OpIfaceUpdate, Target: physR("eno2"), Params: &change.IfaceUpdateParams{Addresses: ptrStrs([]string{"172.16.0.1/24"})}}, false},
		{"vlan.update addresses", change.Op{Type: change.OpVlanUpdate, Target: vlanR("vmbr0.10"), Params: &change.VlanUpdateParams{Addresses: ptrStrs([]string{"172.16.9.1/24"})}}, false},

		{"bond.create free slave", change.Op{Type: change.OpBondCreate, Target: bondR("bond1"), Params: &change.BondCreateParams{Mode: "active-backup", Slaves: []string{"eno2"}}}, false},
		{"bond.create exists+enslaved", change.Op{Type: change.OpBondCreate, Target: bondR("bond0"), Params: &change.BondCreateParams{Mode: "active-backup", Slaves: []string{"eno3"}}}, true},
		{"bond.update existing", change.Op{Type: change.OpBondUpdate, Target: bondR("bond0"), Params: &change.BondUpdateParams{Slaves: ptrStrs([]string{"eno2"})}}, false},
		{"bond.update missing", change.Op{Type: change.OpBondUpdate, Target: bondR("ghost"), Params: &change.BondUpdateParams{}}, true},
		{"bond.delete existing", change.Op{Type: change.OpBondDelete, Target: bondR("bond0"), Params: &change.BondDeleteParams{}}, false},
		{"bond.delete missing", change.Op{Type: change.OpBondDelete, Target: bondR("ghost"), Params: &change.BondDeleteParams{}}, true},

		{"bridge.create exists", change.Op{Type: change.OpBridgeCreate, Target: bridgeR("vmbr0"), Params: &change.BridgeCreateParams{Ports: []string{"eno2"}}}, true},
		{"bridge.create ok", change.Op{Type: change.OpBridgeCreate, Target: bridgeR("vmbr8"), Params: &change.BridgeCreateParams{Ports: []string{"eno2"}}}, false},
		{"bridge.update missing", change.Op{Type: change.OpBridgeUpdate, Target: bridgeR("ghost"), Params: &change.BridgeUpdateParams{MTU: ptrInt(1500)}}, true},
		{"bridge.delete existing", change.Op{Type: change.OpBridgeDelete, Target: bridgeR("vmbr0"), Params: &change.BridgeDeleteParams{}}, false},

		{"port.add free", change.Op{Type: change.OpBridgePortAdd, Target: bridgeR("vmbr0"), Params: &change.BridgePortAddParams{Port: "eno2"}}, false},
		{"port.add unknown port", change.Op{Type: change.OpBridgePortAdd, Target: bridgeR("vmbr0"), Params: &change.BridgePortAddParams{Port: "ghost"}}, true},
		{"port.add unknown bridge", change.Op{Type: change.OpBridgePortAdd, Target: bridgeR("ghost"), Params: &change.BridgePortAddParams{Port: "eno2"}}, true},
		{"port.remove not attached", change.Op{Type: change.OpBridgePortRemove, Target: bridgeR("vmbr0"), Params: &change.BridgePortRemoveParams{Port: "eno2"}}, true},
		{"port.remove unknown port", change.Op{Type: change.OpBridgePortRemove, Target: bridgeR("vmbr0"), Params: &change.BridgePortRemoveParams{Port: "ghost"}}, true},

		{"vlan.create ok", change.Op{Type: change.OpVlanCreate, Target: vlanR("vmbr0.20"), Params: &change.VlanCreateParams{Parent: "vmbr0", Vid: 20}}, false},
		{"vlan.create vid overlap", change.Op{Type: change.OpVlanCreate, Target: vlanR("vmbr0b.10"), Params: &change.VlanCreateParams{Parent: "vmbr0", Vid: 10}}, true},
		{"vlan.create parent missing", change.Op{Type: change.OpVlanCreate, Target: vlanR("ghost.30"), Params: &change.VlanCreateParams{Parent: "ghost", Vid: 30}}, true},
		{"vlan.update existing", change.Op{Type: change.OpVlanUpdate, Target: vlanR("vmbr0.10"), Params: &change.VlanUpdateParams{MTU: ptrInt(1400)}}, false},
		{"vlan.delete missing", change.Op{Type: change.OpVlanDelete, Target: vlanR("ghost.10"), Params: &change.VlanDeleteParams{}}, true},

		{"zone.create exists", change.Op{Type: change.OpSdnZoneCreate, Target: zoneR("zone1"), Params: &change.SdnZoneCreateParams{Type: "simple"}}, true},
		{"zone.create node missing", change.Op{Type: change.OpSdnZoneCreate, Target: zoneR("zone2"), Params: &change.SdnZoneCreateParams{Type: "simple", Nodes: []string{"ghostnode"}}}, true},
		{"zone.create ok", change.Op{Type: change.OpSdnZoneCreate, Target: zoneR("zone3"), Params: &change.SdnZoneCreateParams{Type: "simple", Nodes: []string{"pve1"}}}, false},
		// T-403: exitNodes is checked against known cluster nodes exactly
		// like nodes; peers (underlay IPs, not node names) is not.
		{"zone.create exitNode missing", change.Op{Type: change.OpSdnZoneCreate, Target: zoneR("zone4"), Params: &change.SdnZoneCreateParams{Type: "evpn", Nodes: []string{"pve1"}, ExitNodes: []string{"ghostnode"}}}, true},
		{"zone.create exitNode ok, peers unchecked", change.Op{Type: change.OpSdnZoneCreate, Target: zoneR("zone5"), Params: &change.SdnZoneCreateParams{Type: "evpn", Nodes: []string{"pve1"}, ExitNodes: []string{"pve1"}, Peers: []string{"10.10.0.99"}}}, false},
		{"zone.update missing", change.Op{Type: change.OpSdnZoneUpdate, Target: zoneR("ghost"), Params: &change.SdnZoneUpdateParams{}}, true},
		{"zone.update exitNode missing", change.Op{Type: change.OpSdnZoneUpdate, Target: zoneR("zone1"), Params: &change.SdnZoneUpdateParams{ExitNodes: &[]string{"ghostnode"}}}, true},
		{"zone.delete existing", change.Op{Type: change.OpSdnZoneDelete, Target: zoneR("zone1"), Params: &change.SdnZoneDeleteParams{}}, false},

		{"vnet.create bad zone", change.Op{Type: change.OpSdnVnetCreate, Target: vnetR("zone1/vnetX"), Params: &change.SdnVnetCreateParams{Zone: "ghostzone"}}, true},
		{"vnet.create ok", change.Op{Type: change.OpSdnVnetCreate, Target: vnetR("zone1/vnet2"), Params: &change.SdnVnetCreateParams{Zone: "zone1"}}, false},
		{"vnet.update missing", change.Op{Type: change.OpSdnVnetUpdate, Target: vnetR("zone1/ghost"), Params: &change.SdnVnetUpdateParams{}}, true},
		{"vnet.delete existing", change.Op{Type: change.OpSdnVnetDelete, Target: vnetR("zone1/vnet1"), Params: &change.SdnVnetDeleteParams{}}, false},

		{"subnet.create bad vnet", change.Op{Type: change.OpSdnSubnetCreate, Target: subnetR("10.9.0.0/24"), Params: &change.SdnSubnetCreateParams{Vnet: "ghost", CIDR: "10.9.0.0/24"}}, true},
		{"subnet.update missing", change.Op{Type: change.OpSdnSubnetUpdate, Target: subnetR("10.9.9.0/24"), Params: &change.SdnSubnetUpdateParams{}}, true},
		{"subnet.delete existing", change.Op{Type: change.OpSdnSubnetDelete, Target: subnetR("10.0.0.0/24"), Params: &change.SdnSubnetDeleteParams{}}, false},

		{"port.remove unknown bridge", change.Op{Type: change.OpBridgePortRemove, Target: bridgeR("ghost"), Params: &change.BridgePortRemoveParams{Port: "eno1"}}, true},
		{"guest.nic.update missing", change.Op{Type: change.OpGuestNicUpdate, Target: inventory.Ref{Kind: inventory.KindGuestNic, Node: "pve1", ID: "999/net0"}, Params: &change.GuestNicUpdateParams{BridgeOrVnet: ptrStr("vmbr0")}}, true},
		{"guest.nic.update ok", change.Op{Type: change.OpGuestNicUpdate, Target: inventory.Ref{Kind: inventory.KindGuestNic, Node: "pve1", ID: "100/net0"}, Params: &change.GuestNicUpdateParams{BridgeOrVnet: ptrStr("vmbr0")}}, false},
		{"guest.nic.update bad target bridge", change.Op{Type: change.OpGuestNicUpdate, Target: inventory.Ref{Kind: inventory.KindGuestNic, Node: "pve1", ID: "100/net0"}, Params: &change.GuestNicUpdateParams{BridgeOrVnet: ptrStr("ghostbr")}}, true},
		{"ipam.alloc missing subnet", change.Op{Type: change.OpIpamAllocCreate, Target: subnetR("10.9.0.0/24"), Params: &change.IpamAllocCreateParams{CIDR: "10.9.0.5/32"}}, true},
		{"ipam.alloc out of subnet", change.Op{Type: change.OpIpamAllocCreate, Target: subnetR("10.0.0.0/24"), Params: &change.IpamAllocCreateParams{CIDR: "192.168.9.9/32"}}, true},
		{"ipam.alloc.delete missing", change.Op{Type: change.OpIpamAllocDelete, Target: subnetR("10.9.0.0/24"), Params: &change.IpamAllocDeleteParams{CIDR: "10.9.0.5/32"}}, true},
		{"bridge.create vid overlap", change.Op{Type: change.OpBridgeCreate, Target: bridgeR("vmbrVid"), Params: &change.BridgeCreateParams{Vids: []change.VidRange{{Low: 10, High: 20}, {Low: 15, High: 25}}}}, true},
		{"fw.rule.create pos gap", change.Op{Type: change.OpFwRuleCreate, Target: rulesetRef(), Params: &change.FwRuleCreateParams{Direction: "in", Action: "ACCEPT", Pos: 99}}, false},
	}

	for _, c := range cases {
		findings := change.Validate([]change.Op{c.op}, snap)
		gotErr := false
		for _, f := range findings {
			if f.Severity == change.SeverityError {
				gotErr = true
			}
		}
		if gotErr != c.wantErr {
			t.Errorf("%s: gotErr=%v want=%v (findings=%+v)", c.name, gotErr, c.wantErr, findings)
		}
	}
}
