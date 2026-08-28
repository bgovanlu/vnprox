// SPDX-License-Identifier: Apache-2.0

package change

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// allOpTypeConstants is a hand-maintained mirror of every OpType constant
// declared in op.go's const block. This is deliberately independent of
// paramFactories' keys (rather than deriving one from the other), so
// TestKnownOpTypes_MatchesVocabulary can catch a real bug in either
// direction: an OpType constant nobody registered a factory for (would
// panic at decode time in production), or a stray factory entry keyed by
// something that isn't a documented op at all.
var allOpTypeConstants = []OpType{
	OpIfaceUpdate, OpIfaceRename, OpIfaceRawReplace,
	OpBondCreate, OpBondUpdate, OpBondDelete,
	OpBridgeCreate, OpBridgeUpdate, OpBridgeDelete, OpBridgePortAdd, OpBridgePortRemove,
	OpVlanCreate, OpVlanUpdate, OpVlanDelete,
	OpSdnZoneCreate, OpSdnZoneUpdate, OpSdnZoneDelete,
	OpSdnVnetCreate, OpSdnVnetUpdate, OpSdnVnetDelete,
	OpSdnSubnetCreate, OpSdnSubnetUpdate, OpSdnSubnetDelete,
	OpSdnDnsZoneCreate, OpSdnDnsZoneUpdate, OpSdnDnsZoneDelete,
	OpSdnDnsRecordCreate, OpSdnDnsRecordUpdate, OpSdnDnsRecordDelete,
	OpSdnFabricCreate, OpSdnFabricUpdate, OpSdnFabricDelete,
	OpSdnControllerCreate, OpSdnControllerUpdate, OpSdnControllerDelete,
	OpSdnIpamCreate, OpSdnIpamUpdate, OpSdnIpamDelete,
	OpSdnApply,
	OpGuestNicUpdate,
	OpFwRuleCreate, OpFwRuleUpdate, OpFwRuleDelete, OpFwRuleMove, OpFwOptionsUpdate,
	OpFwAliasCreate, OpFwAliasUpdate, OpFwAliasDelete,
	OpFwIpsetCreate, OpFwIpsetUpdate, OpFwIpsetDelete,
	OpFwGroupCreate, OpFwGroupUpdate, OpFwGroupDelete,
	OpIpamAllocCreate, OpIpamAllocDelete,
	OpQosShapeCreate, OpQosShapeUpdate, OpQosShapeDelete,
	OpWgTunnelCreate, OpWgTunnelUpdate, OpWgTunnelDelete, OpWgPeerAdd, OpWgPeerRemove,
	OpNatMasqueradeCreate, OpNatMasqueradeDelete,
	OpNatPortForwardCreate, OpNatPortForwardUpdate, OpNatPortForwardDelete,
	OpRouteStaticCreate, OpRouteStaticUpdate, OpRouteStaticDelete,
	OpVFProvision,
	OpSwitchPortUpdate,
	OpTcMirrorCreate, OpTcMirrorUpdate, OpTcMirrorDelete,
}

// docs/data-model.md §3's table lists exactly these groups: iface(3, incl.
// T-208's iface.raw.replace), bond(3), bridge(5), vlan(3), sdn(10), guest(1),
// fw(14), ipam(2), qos(3, T-1505), wg(5, T-1401), nat(5, T-1403),
// route(3, T-1403), vf(1, T-1506) = 58, plus T-1204's sdn.dns.zone.*(3) +
// sdn.dns.record.*(3) = 6, T-1205's switch.port.update(1) = 65,
// T-3101's sdn.fabric.create/update/delete(3) = 68, T-3102's
// sdn.controller.create/update/delete(3) = 71, T-3104's
// sdn.ipam.create/update/delete(3) = 74, and T-4014's
// tc.mirror.create/update/delete(3) = 77.
const wantOpVocabularySize = 77

func TestOpVocabulary_SizeMatchesDataModelDoc(t *testing.T) {
	if len(allOpTypeConstants) != wantOpVocabularySize {
		t.Fatalf("allOpTypeConstants has %d entries, want %d (docs/data-model.md §3's op table) — "+
			"this test's own list is out of date, update it alongside op.go", len(allOpTypeConstants), wantOpVocabularySize)
	}
}

// TestKnownOpTypes_MatchesVocabulary proves paramFactories has exactly one
// entry per documented OpType constant — no OpType missing a factory (a
// production panic waiting to happen at decode time) and no factory for a
// type that isn't one of the documented constants.
func TestKnownOpTypes_MatchesVocabulary(t *testing.T) {
	want := map[OpType]bool{}
	for _, ot := range allOpTypeConstants {
		want[ot] = true
	}
	got := map[OpType]bool{}
	for _, ot := range KnownOpTypes() {
		got[ot] = true
	}
	for ot := range want {
		if !got[ot] {
			t.Errorf("OpType %q has no paramFactories entry", ot)
		}
	}
	for ot := range got {
		if !want[ot] {
			t.Errorf("paramFactories has an entry for %q, which is not in allOpTypeConstants", ot)
		}
	}
	if len(want) != len(got) {
		t.Errorf("len(paramFactories) = %d, want %d", len(got), len(want))
	}
}

// opRoundTripCase is one (op type, target, params) fixture used to prove
// every documented op round-trips JSON (T-201 acceptance criterion 1).
type opRoundTripCase struct {
	target inventory.Ref
	params Params
	name   string
	opType OpType
}

func opRoundTripCases() []opRoundTripCase {
	ref := func(kind inventory.Kind, node, id string) inventory.Ref {
		return inventory.Ref{Kind: kind, Node: node, ID: id}
	}
	str := func(s string) *string { return &s }
	i := func(n int) *int { return &n }
	b := func(v bool) *bool { return &v }
	ss := func(v ...string) *[]string { return &v }

	return []opRoundTripCase{
		{
			name: "iface.update", opType: OpIfaceUpdate,
			target: ref(inventory.KindPhysNic, "pve1", "eno1"),
			params: &IfaceUpdateParams{MTU: i(9000), Comments: str("uplink"), Addresses: ss("10.0.0.1/24"), Gateway: str("10.0.0.254"), Autostart: b(true)},
		},
		{
			name: "iface.rename", opType: OpIfaceRename,
			target: ref(inventory.KindBridge, "pve1", "vmbr0"),
			params: &IfaceRenameParams{NewName: "vmbrmgmt"},
		},
		{
			name: "iface.raw.replace", opType: OpIfaceRawReplace,
			target: ref(inventory.KindNode, "pve1", "pve1"),
			params: &IfaceRawReplaceParams{Content: "auto lo\niface lo inet loopback\n", BaseHash: "deadbeef"},
		},
		{
			name: "bond.create", opType: OpBondCreate,
			target: ref(inventory.KindBond, "pve1", "bond0"),
			params: &BondCreateParams{Mode: "802.3ad", Slaves: []string{"eno1", "eno2"}, LACPRate: "fast", XmitHashPolicy: "layer3+4", MIIMon: 100, MTU: 9000, Comments: "core uplink"},
		},
		{
			name: "bond.update", opType: OpBondUpdate,
			target: ref(inventory.KindBond, "pve1", "bond0"),
			params: &BondUpdateParams{Mode: str("active-backup"), Slaves: ss("eno1", "eno3")},
		},
		{
			name: "bond.delete", opType: OpBondDelete,
			target: ref(inventory.KindBond, "pve1", "bond0"),
			params: &BondDeleteParams{},
		},
		{
			name: "bridge.create", opType: OpBridgeCreate,
			target: ref(inventory.KindBridge, "pve1", "vmbr5"),
			params: &BridgeCreateParams{Ports: []string{"bond0"}, VlanAware: true, Vids: []VidRange{{Low: 10, High: 20}, {Low: 100, High: 100}}, Addresses: []string{"10.1.0.1/24"}, MTU: 9000, STP: true, Comments: "guest bridge"},
		},
		{
			name: "bridge.update", opType: OpBridgeUpdate,
			target: ref(inventory.KindBridge, "pve1", "vmbr0"),
			params: &BridgeUpdateParams{VlanAware: b(true), MTU: i(1500)},
		},
		{
			name: "bridge.delete", opType: OpBridgeDelete,
			target: ref(inventory.KindBridge, "pve1", "vmbr5"),
			params: &BridgeDeleteParams{},
		},
		{
			name: "bridge.port.add", opType: OpBridgePortAdd,
			target: ref(inventory.KindBridge, "pve1", "vmbr0"),
			params: &BridgePortAddParams{Port: "eno2"},
		},
		{
			name: "bridge.port.remove", opType: OpBridgePortRemove,
			target: ref(inventory.KindBridge, "pve1", "vmbr0"),
			params: &BridgePortRemoveParams{Port: "eno2"},
		},
		{
			name: "vlan.create", opType: OpVlanCreate,
			target: ref(inventory.KindVlan, "pve1", "vmbr0.20"),
			params: &VlanCreateParams{Parent: "vmbr0", Vid: 20, Addresses: []string{"10.20.0.1/24"}, MTU: 1500},
		},
		{
			name: "vlan.update", opType: OpVlanUpdate,
			target: ref(inventory.KindVlan, "pve1", "vmbr0.20"),
			params: &VlanUpdateParams{MTU: i(1400)},
		},
		{
			name: "vlan.delete", opType: OpVlanDelete,
			target: ref(inventory.KindVlan, "pve1", "vmbr0.20"),
			params: &VlanDeleteParams{},
		},
		{
			name: "sdn.zone.create", opType: OpSdnZoneCreate,
			target: ref(inventory.KindSDNZone, "", "zone1"),
			params: &SdnZoneCreateParams{Type: "vxlan", Bridge: "vmbr0", Controller: "ctl1", Nodes: []string{"pve1", "pve2"}, VrfVxlan: 4000, MTU: 1450},
		},
		{
			name: "sdn.zone.update", opType: OpSdnZoneUpdate,
			target: ref(inventory.KindSDNZone, "", "zone1"),
			params: &SdnZoneUpdateParams{MTU: i(1400)},
		},
		{
			name: "sdn.zone.delete", opType: OpSdnZoneDelete,
			target: ref(inventory.KindSDNZone, "", "zone1"),
			params: &SdnZoneDeleteParams{},
		},
		{
			name: "sdn.vnet.create", opType: OpSdnVnetCreate,
			target: ref(inventory.KindSDNVnet, "", "zone1/vnet1"),
			params: &SdnVnetCreateParams{Zone: "zone1", Alias: "prod", Tag: 100, VlanAware: false},
		},
		{
			name: "sdn.vnet.update", opType: OpSdnVnetUpdate,
			target: ref(inventory.KindSDNVnet, "", "zone1/vnet1"),
			params: &SdnVnetUpdateParams{Alias: str("prod2")},
		},
		{
			name: "sdn.vnet.delete", opType: OpSdnVnetDelete,
			target: ref(inventory.KindSDNVnet, "", "zone1/vnet1"),
			params: &SdnVnetDeleteParams{},
		},
		{
			name: "sdn.subnet.create", opType: OpSdnSubnetCreate,
			target: ref(inventory.KindSDNSubnet, "", "10.10.0.0/24"),
			params: &SdnSubnetCreateParams{Vnet: "zone1/vnet1", CIDR: "10.10.0.0/24", Gateway: "10.10.0.1", SNAT: true, DHCPRanges: []string{"10.10.0.100-10.10.0.200"}},
		},
		{
			name: "sdn.subnet.update", opType: OpSdnSubnetUpdate,
			target: ref(inventory.KindSDNSubnet, "", "10.10.0.0/24"),
			params: &SdnSubnetUpdateParams{Gateway: str("10.10.0.2")},
		},
		{
			name: "sdn.subnet.delete", opType: OpSdnSubnetDelete,
			target: ref(inventory.KindSDNSubnet, "", "10.10.0.0/24"),
			params: &SdnSubnetDeleteParams{},
		},
		{
			name: "sdn.dns.zone.create", opType: OpSdnDnsZoneCreate,
			target: ref(inventory.KindSDNDnsZone, "", "example.com"),
			params: &SdnDnsZoneCreateParams{DNS: "powerdns", TTL: 3600},
		},
		{
			name: "sdn.dns.zone.update", opType: OpSdnDnsZoneUpdate,
			target: ref(inventory.KindSDNDnsZone, "", "example.com"),
			params: &SdnDnsZoneUpdateParams{TTL: i(7200)},
		},
		{
			name: "sdn.dns.zone.delete", opType: OpSdnDnsZoneDelete,
			target: ref(inventory.KindSDNDnsZone, "", "example.com"),
			params: &SdnDnsZoneDeleteParams{},
		},
		{
			name: "sdn.dns.record.create", opType: OpSdnDnsRecordCreate,
			target: ref(inventory.KindSDNDnsRecord, "", "example.com/web1/A"),
			params: &SdnDnsRecordCreateParams{Zone: "example.com", Name: "web1", Type: "A", Value: "10.10.0.5", TTL: 300},
		},
		{
			name: "sdn.dns.record.update", opType: OpSdnDnsRecordUpdate,
			target: ref(inventory.KindSDNDnsRecord, "", "example.com/web1/A"),
			params: &SdnDnsRecordUpdateParams{Value: str("10.10.0.6")},
		},
		{
			name: "sdn.dns.record.delete", opType: OpSdnDnsRecordDelete,
			target: ref(inventory.KindSDNDnsRecord, "", "example.com/web1/A"),
			params: &SdnDnsRecordDeleteParams{},
		},
		{
			name: "sdn.fabric.create", opType: OpSdnFabricCreate,
			target: ref(inventory.KindSDNFabric, "", "fab1"),
			params: &SdnFabricCreateParams{Protocol: "ospf", Area: "0.0.0.0", Redistribute: []string{"connected"}, RouteFilter: "pl1", IPPrefix: "10.255.0.0/24"},
		},
		{
			name: "sdn.fabric.update", opType: OpSdnFabricUpdate,
			target: ref(inventory.KindSDNFabric, "", "fab1"),
			params: &SdnFabricUpdateParams{Area: str("0.0.0.1")},
		},
		{
			name: "sdn.fabric.delete", opType: OpSdnFabricDelete,
			target: ref(inventory.KindSDNFabric, "", "fab1"),
			params: &SdnFabricDeleteParams{},
		},
		{
			name: "sdn.controller.create", opType: OpSdnControllerCreate,
			target: ref(inventory.KindSDNController, "", "bgp1"),
			params: &SdnControllerCreateParams{Type: "bgp", ASN: 65000, Peers: []string{"10.0.0.1"}, Ebgp: true},
		},
		{
			name: "sdn.controller.update", opType: OpSdnControllerUpdate,
			target: ref(inventory.KindSDNController, "", "bgp1"),
			params: &SdnControllerUpdateParams{ASN: i(65001)},
		},
		{
			name: "sdn.controller.delete", opType: OpSdnControllerDelete,
			target: ref(inventory.KindSDNController, "", "bgp1"),
			params: &SdnControllerDeleteParams{},
		},
		{
			name: "sdn.ipam.create", opType: OpSdnIpamCreate,
			target: ref(inventory.KindSDNIpam, "", "nb1"),
			params: &SdnIpamCreateParams{Type: "netbox", URL: "https://netbox.example.com", Token: "secret"},
		},
		{
			name: "sdn.ipam.update", opType: OpSdnIpamUpdate,
			target: ref(inventory.KindSDNIpam, "", "nb1"),
			params: &SdnIpamUpdateParams{URL: str("https://netbox2.example.com")},
		},
		{
			name: "sdn.ipam.delete", opType: OpSdnIpamDelete,
			target: ref(inventory.KindSDNIpam, "", "nb1"),
			params: &SdnIpamDeleteParams{},
		},
		{
			name: "sdn.apply", opType: OpSdnApply,
			target: inventory.Ref{},
			params: &SdnApplyParams{},
		},
		{
			name: "guest.nic.update", opType: OpGuestNicUpdate,
			target: ref(inventory.KindGuestNic, "pve1", "100/net0"),
			params: &GuestNicUpdateParams{BridgeOrVnet: str("vmbr5"), Vid: i(20), RateMbps: i(100), Firewall: b(true), LinkDown: b(false)},
		},
		{
			name: "fw.rule.create", opType: OpFwRuleCreate,
			target: ref(inventory.KindFwRuleset, "", "cluster"),
			params: &FwRuleCreateParams{Pos: 0, Direction: "in", Action: "ACCEPT", Enabled: true, Proto: "tcp", Dport: "22", Comment: "ssh"},
		},
		{
			name: "fw.rule.update", opType: OpFwRuleUpdate,
			target: ref(inventory.KindFwRuleset, "", "cluster"),
			params: &FwRuleUpdateParams{Pos: 0, Action: str("DROP")},
		},
		{
			name: "fw.rule.delete", opType: OpFwRuleDelete,
			target: ref(inventory.KindFwRuleset, "", "cluster"),
			params: &FwRuleDeleteParams{Pos: 0},
		},
		{
			name: "fw.rule.move", opType: OpFwRuleMove,
			target: ref(inventory.KindFwRuleset, "", "cluster"),
			params: &FwRuleMoveParams{FromPos: 0, ToPos: 3},
		},
		{
			name: "fw.options.update", opType: OpFwOptionsUpdate,
			target: ref(inventory.KindFwRuleset, "pve1", "pve1"),
			params: &FwOptionsUpdateParams{Enabled: b(true), DefaultIn: str("DROP"), DefaultOut: str("ACCEPT")},
		},
		{
			name: "fw.alias.create", opType: OpFwAliasCreate,
			target: ref(inventory.KindFwRuleset, "", "cluster"),
			params: &FwAliasCreateParams{Name: "office", CIDR: "203.0.113.0/24"},
		},
		{
			name: "fw.alias.update", opType: OpFwAliasUpdate,
			target: ref(inventory.KindFwRuleset, "", "cluster"),
			params: &FwAliasUpdateParams{Name: "office", CIDR: str("203.0.113.0/25")},
		},
		{
			name: "fw.alias.delete", opType: OpFwAliasDelete,
			target: ref(inventory.KindFwRuleset, "", "cluster"),
			params: &FwAliasDeleteParams{Name: "office"},
		},
		{
			name: "fw.ipset.create", opType: OpFwIpsetCreate,
			target: ref(inventory.KindFwRuleset, "", "cluster"),
			params: &FwIpsetCreateParams{Name: "blocklist", CIDRs: []string{"198.51.100.0/24"}},
		},
		{
			name: "fw.ipset.update", opType: OpFwIpsetUpdate,
			target: ref(inventory.KindFwRuleset, "", "cluster"),
			params: &FwIpsetUpdateParams{Name: "blocklist", CIDRs: ss("198.51.100.0/24", "192.0.2.0/24")},
		},
		{
			name: "fw.ipset.delete", opType: OpFwIpsetDelete,
			target: ref(inventory.KindFwRuleset, "", "cluster"),
			params: &FwIpsetDeleteParams{Name: "blocklist"},
		},
		{
			name: "fw.group.create", opType: OpFwGroupCreate,
			target: ref(inventory.KindFwRuleset, "", "cluster"),
			params: &FwGroupCreateParams{Name: "web", Rules: []FwRuleSpec{{Direction: "in", Action: "ACCEPT", Proto: "tcp", Dport: "443", Enabled: true}}},
		},
		{
			name: "fw.group.update", opType: OpFwGroupUpdate,
			target: ref(inventory.KindFwRuleset, "", "cluster"),
			params: &FwGroupUpdateParams{Name: "web", Comment: str("web tier")},
		},
		{
			name: "fw.group.delete", opType: OpFwGroupDelete,
			target: ref(inventory.KindFwRuleset, "", "cluster"),
			params: &FwGroupDeleteParams{Name: "web"},
		},
		{
			name: "ipam.alloc.create", opType: OpIpamAllocCreate,
			target: ref(inventory.KindSDNSubnet, "", "10.10.0.0/24"),
			params: &IpamAllocCreateParams{CIDR: "10.10.0.50/32", Hostname: "web1", MAC: "aa:bb:cc:dd:ee:ff"},
		},
		{
			name: "ipam.alloc.delete", opType: OpIpamAllocDelete,
			target: ref(inventory.KindSDNSubnet, "", "10.10.0.0/24"),
			params: &IpamAllocDeleteParams{CIDR: "10.10.0.50/32"},
		},
		{
			name: "qos.shape.create", opType: OpQosShapeCreate,
			target: ref(inventory.KindQosShape, "pve1", "shape1"),
			params: &QosShapeCreateParams{Bridge: "vmbr0", MatchCIDR: "10.10.0.0/24", RateMbit: 10, CeilMbit: i(20), Priority: i(1)},
		},
		{
			name: "qos.shape.update", opType: OpQosShapeUpdate,
			target: ref(inventory.KindQosShape, "pve1", "shape1"),
			params: &QosShapeUpdateParams{RateMbit: i(15), CeilMbit: i(30)},
		},
		{
			name: "qos.shape.delete", opType: OpQosShapeDelete,
			target: ref(inventory.KindQosShape, "pve1", "shape1"),
			params: &QosShapeDeleteParams{},
		},
		{
			name: "wg.tunnel.create", opType: OpWgTunnelCreate,
			target: ref(inventory.KindWgTunnel, "pve1", "01HWGTUN000000000000000001"),
			params: &WgTunnelCreateParams{IfName: "wg0", ListenPort: 51820, Addresses: []string{"10.10.0.1/24"}, MTU: 1420, Carrier: "vmbr0"},
		},
		{
			name: "wg.tunnel.update", opType: OpWgTunnelUpdate,
			target: ref(inventory.KindWgTunnel, "pve1", "01HWGTUN000000000000000001"),
			params: &WgTunnelUpdateParams{ListenPort: i(51821), MTU: i(1380)},
		},
		{
			name: "wg.tunnel.delete", opType: OpWgTunnelDelete,
			target: ref(inventory.KindWgTunnel, "pve1", "01HWGTUN000000000000000001"),
			params: &WgTunnelDeleteParams{},
		},
		{
			name: "wg.peer.add", opType: OpWgPeerAdd,
			target: ref(inventory.KindWgPeer, "pve1", "01HWGTUN000000000000000001/PEERkey000000000000000000000000000000000000="),
			params: &WgPeerAddParams{PublicKey: "PEERkey000000000000000000000000000000000000=", Endpoint: "203.0.113.10:51820", AllowedIPs: []string{"10.10.0.2/32"}, KeepaliveSec: 25, External: true},
		},
		{
			name: "wg.peer.remove", opType: OpWgPeerRemove,
			target: ref(inventory.KindWgPeer, "pve1", "01HWGTUN000000000000000001/PEERkey000000000000000000000000000000000000="),
			params: &WgPeerRemoveParams{PublicKey: "PEERkey000000000000000000000000000000000000="},
		},
		{
			name: "nat.masquerade.create", opType: OpNatMasqueradeCreate,
			target: ref(inventory.KindNatRule, "pve1", "masq1"),
			params: &NatMasqueradeCreateParams{Iface: "vmbr0", SourceCIDR: "192.168.1.0/24"},
		},
		{
			name: "nat.masquerade.delete", opType: OpNatMasqueradeDelete,
			target: ref(inventory.KindNatRule, "pve1", "masq1"),
			params: &NatMasqueradeDeleteParams{},
		},
		{
			name: "nat.portforward.create", opType: OpNatPortForwardCreate,
			target: ref(inventory.KindNatRule, "pve1", "pf1"),
			params: &NatPortForwardCreateParams{Iface: "vmbr0", Proto: "tcp", ExtPort: 8080, IntIP: "192.168.1.50", IntPort: 80},
		},
		{
			name: "nat.portforward.update", opType: OpNatPortForwardUpdate,
			target: ref(inventory.KindNatRule, "pve1", "pf1"),
			params: &NatPortForwardUpdateParams{ExtPort: intPtr(9090)},
		},
		{
			name: "nat.portforward.delete", opType: OpNatPortForwardDelete,
			target: ref(inventory.KindNatRule, "pve1", "pf1"),
			params: &NatPortForwardDeleteParams{},
		},
		{
			name: "route.static.create", opType: OpRouteStaticCreate,
			target: ref(inventory.KindStaticRoute, "pve1", "lab-route"),
			params: &RouteStaticCreateParams{Iface: "vmbr0", DestCIDR: "10.10.0.0/24", Gateway: "203.0.113.1"},
		},
		{
			name: "route.static.update", opType: OpRouteStaticUpdate,
			target: ref(inventory.KindStaticRoute, "pve1", "lab-route"),
			params: &RouteStaticUpdateParams{Gateway: strPtr("203.0.113.2")},
		},
		{
			name: "route.static.delete", opType: OpRouteStaticDelete,
			target: ref(inventory.KindStaticRoute, "pve1", "lab-route"),
			params: &RouteStaticDeleteParams{},
		},
		{
			name: "vf.provision", opType: OpVFProvision,
			target: ref(inventory.KindPhysNic, "pve1", "eno1"),
			params: &VFProvisionParams{Count: 2, VLAN: 100, SpoofCheck: boolPtr(true), Trust: boolPtr(false)},
		},
		{
			name: "switch.port.update", opType: OpSwitchPortUpdate,
			target: ref(inventory.KindSwitchPort, "", "sw-1/Ethernet1/14"),
			params: &SwitchPortUpdateParams{
				Untagged:       intPtr(10),
				Tagged:         &[]int{10, 20},
				Description:    str("pve1 uplink"),
				LacpMode:       str("active"),
				LacpRate:       str("fast"),
				ExpectNeighbor: SwitchNeighbor{ChassisID: "aa:bb:cc:dd:ee:ff", PortID: "eno1"},
			},
		},
		{
			name: "tc.mirror.create", opType: OpTcMirrorCreate,
			target: ref(inventory.KindTcMirror, "pve1", "span1"),
			params: &TcMirrorCreateParams{SourceIface: "vmbr0", DestIface: "vmbr99", MaxMbit: intPtr(100), MaxDurationSec: 3600},
		},
		{
			name: "tc.mirror.update", opType: OpTcMirrorUpdate,
			target: ref(inventory.KindTcMirror, "pve1", "span1"),
			params: &TcMirrorUpdateParams{MaxDurationSec: intPtr(7200)},
		},
		{
			name: "tc.mirror.delete", opType: OpTcMirrorDelete,
			target: ref(inventory.KindTcMirror, "pve1", "span1"),
			params: &TcMirrorDeleteParams{},
		},
	}
}

// TestOp_RoundTrip is T-201 acceptance criterion 1's positive half: every
// documented op type marshals then unmarshals back to an equal Op.
func TestOp_RoundTrip(t *testing.T) {
	cases := opRoundTripCases()
	seen := map[OpType]bool{}
	for _, tc := range cases {
		seen[tc.opType] = true
		t.Run(tc.name, func(t *testing.T) {
			op := Op{Type: tc.opType, Target: tc.target, Params: tc.params}
			data, err := json.Marshal(op)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}

			var got Op
			if unmarshalErr := json.Unmarshal(data, &got); unmarshalErr != nil {
				t.Fatalf("Unmarshal(%s): %v", data, unmarshalErr)
			}
			if got.Type != op.Type {
				t.Errorf("Type = %q, want %q", got.Type, op.Type)
			}
			if got.Target != op.Target {
				t.Errorf("Target = %+v, want %+v", got.Target, op.Target)
			}

			gotParams, err := json.Marshal(got.Params)
			if err != nil {
				t.Fatalf("marshaling decoded params: %v", err)
			}
			wantParams, err := json.Marshal(tc.params)
			if err != nil {
				t.Fatalf("marshaling want params: %v", err)
			}
			if string(gotParams) != string(wantParams) {
				t.Errorf("params round-tripped wrong:\n got  %s\n want %s", gotParams, wantParams)
			}

			// Round-tripping a slice of ops (the actual shape a changeset
			// stores/decodes) must work identically to a lone Op.
			opsData, err := json.Marshal([]Op{op})
			if err != nil {
				t.Fatalf("Marshal([]Op): %v", err)
			}
			var gotOps []Op
			if err := json.Unmarshal(opsData, &gotOps); err != nil {
				t.Fatalf("Unmarshal([]Op) %s: %v", opsData, err)
			}
			if len(gotOps) != 1 || gotOps[0].Type != op.Type {
				t.Fatalf("[]Op round-trip: got %+v", gotOps)
			}
		})
	}

	// Every case here must be one of the documented op types, and every
	// documented type must be covered by some case, so this test doesn't
	// silently miss new ops added to op.go without a matching fixture.
	for _, wantType := range allOpTypeConstants {
		if !seen[wantType] {
			t.Errorf("op type %q has no TestOp_RoundTrip fixture", wantType)
		}
	}
}

func TestOp_UnknownOpType_400Path(t *testing.T) {
	var op Op
	err := json.Unmarshal([]byte(`{"op":"bridge.teleport","target":"bridge:pve1:vmbr0","params":{}}`), &op)
	if err == nil {
		t.Fatal("expected an error for an unknown op type")
	}
	var decErr *OpDecodeError
	if !errors.As(err, &decErr) {
		t.Fatalf("error = %v (%T), want *OpDecodeError", err, err)
	}
	if decErr.Path != "op" {
		t.Errorf("Path = %q, want %q", decErr.Path, "op")
	}
}

func TestOp_UnknownParamField_400Path(t *testing.T) {
	var op Op
	err := json.Unmarshal([]byte(`{"op":"bridge.update","target":"bridge:pve1:vmbr0","params":{"mtuu":9000}}`), &op)
	if err == nil {
		t.Fatal("expected an error for an unknown params field")
	}
	var decErr *OpDecodeError
	if !errors.As(err, &decErr) {
		t.Fatalf("error = %v (%T), want *OpDecodeError", err, err)
	}
	if decErr.Path != "params.mtuu" {
		t.Errorf("Path = %q, want %q", decErr.Path, "params.mtuu")
	}
}

func TestOp_UnknownEnvelopeField_400Path(t *testing.T) {
	var op Op
	err := json.Unmarshal([]byte(`{"op":"bridge.update","target":"bridge:pve1:vmbr0","params":{},"extra":true}`), &op)
	if err == nil {
		t.Fatal("expected an error for an unknown envelope field")
	}
	var decErr *OpDecodeError
	if !errors.As(err, &decErr) {
		t.Fatalf("error = %v (%T), want *OpDecodeError", err, err)
	}
	if decErr.Path != "extra" {
		t.Errorf("Path = %q, want %q", decErr.Path, "extra")
	}
}

func TestOp_MissingTarget_ErrorsUnlessNoTargetOp(t *testing.T) {
	var op Op
	err := json.Unmarshal([]byte(`{"op":"bridge.update","params":{}}`), &op)
	if err == nil {
		t.Fatal("expected an error for a missing target on an op that requires one")
	}
	var decErr *OpDecodeError
	if !errors.As(err, &decErr) {
		t.Fatalf("error = %v (%T), want *OpDecodeError", err, err)
	}
	if decErr.Path != "target" {
		t.Errorf("Path = %q, want %q", decErr.Path, "target")
	}

	// sdn.apply is the one documented exception: no target required.
	var applyOp Op
	if err := json.Unmarshal([]byte(`{"op":"sdn.apply","params":{}}`), &applyOp); err != nil {
		t.Fatalf("sdn.apply with no target: %v", err)
	}
	if !applyOp.Target.IsZero() {
		t.Errorf("Target = %+v, want zero", applyOp.Target)
	}
}

func TestOp_MalformedTarget_400Path(t *testing.T) {
	var op Op
	err := json.Unmarshal([]byte(`{"op":"bridge.update","target":"not-a-valid-ref","params":{}}`), &op)
	if err == nil {
		t.Fatal("expected an error for a malformed target ref")
	}
	var decErr *OpDecodeError
	if !errors.As(err, &decErr) {
		t.Fatalf("error = %v (%T), want *OpDecodeError", err, err)
	}
	if decErr.Path != "target" {
		t.Errorf("Path = %q, want %q", decErr.Path, "target")
	}
}

func TestOp_MissingOpType_400Path(t *testing.T) {
	var op Op
	err := json.Unmarshal([]byte(`{"target":"bridge:pve1:vmbr0","params":{}}`), &op)
	if err == nil {
		t.Fatal("expected an error for a missing op type")
	}
	var decErr *OpDecodeError
	if !errors.As(err, &decErr) {
		t.Fatalf("error = %v (%T), want *OpDecodeError", err, err)
	}
	if decErr.Path != "op" {
		t.Errorf("Path = %q, want %q", decErr.Path, "op")
	}
}

// TestOp_EmptyParamsObject_OK proves a delete op (or any op) with an
// entirely absent "params" key decodes fine — content validation (required
// fields present, ranges, enums) is T-202's job, not this package's
// structural decoding.
func TestOp_EmptyParamsObject_OK(t *testing.T) {
	var op Op
	if err := json.Unmarshal([]byte(`{"op":"bond.delete","target":"bond:pve1:bond0"}`), &op); err != nil {
		t.Fatalf("Unmarshal with no params key: %v", err)
	}
	if _, ok := op.Params.(*BondDeleteParams); !ok {
		t.Errorf("Params = %T, want *BondDeleteParams", op.Params)
	}
}
