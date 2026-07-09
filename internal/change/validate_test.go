package change

import (
	"sort"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// --- shared test helpers ---------------------------------------------------

func testRef(kind inventory.Kind, node, id string) inventory.Ref {
	return inventory.Ref{Kind: kind, Node: node, ID: id}
}

func mkOp(t OpType, target inventory.Ref, params Params) Op {
	return Op{Type: t, Target: target, Params: params}
}

func intPtr(v int) *int             { return &v }
func strPtr(v string) *string       { return &v }
func strsPtr(v ...string) *[]string { return &v }

// buildSnapshot builds an inventory.Snapshot from a fixed set of entities in
// one ApplyPoll call — sufficient for validate_test.go's purposes, which
// only ever reads a snapshot, never exercises incremental delta behavior
// (that is internal/inventory's own test suite's job). SourceHostNetlink is
// used uniformly: per merge.go's ownershipRules, every field this package's
// validators read is either solely owned by SourceHostNetlink or accepts it
// as a (here, the only) contributing source.
func buildSnapshot(entities ...inventory.Entity) inventory.Snapshot {
	g := inventory.NewGraph()
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{}, entities)
	return g.Snapshot()
}

// wantFinding is a golden case's expected (severity, code, ref) triple —
// deliberately omitting Message/Fix, which are not part of the "exact
// (code, ref) match" acceptance criterion.
type wantFinding struct {
	sev  Severity
	code string
	ref  string
}

// assertFindings compares got against want as sets (order-independent: the
// pipeline's internal ordering is an implementation detail), failing with a
// full diff on any mismatch.
func assertFindings(t *testing.T, got []Finding, want []wantFinding) {
	t.Helper()
	gotSet := make([]wantFinding, len(got))
	for i, f := range got {
		gotSet[i] = wantFinding{sev: f.Severity, code: f.Code, ref: f.Ref}
	}
	sortFn := func(s []wantFinding) {
		sort.Slice(s, func(i, j int) bool {
			if s[i].code != s[j].code {
				return s[i].code < s[j].code
			}
			if s[i].ref != s[j].ref {
				return s[i].ref < s[j].ref
			}
			return s[i].sev < s[j].sev
		})
	}
	wantSet := append([]wantFinding(nil), want...)
	sortFn(gotSet)
	sortFn(wantSet)
	if len(gotSet) != len(wantSet) {
		t.Fatalf("got %d findings, want %d\ngot:  %+v\nwant: %+v", len(gotSet), len(wantSet), got, want)
	}
	for i := range gotSet {
		if gotSet[i] != wantSet[i] {
			t.Fatalf("finding[%d] mismatch: got %+v, want %+v\nfull got:  %+v\nfull want: %+v", i, gotSet[i], wantSet[i], got, want)
		}
	}
}

// --- golden validation suite (T-202 acceptance criterion 1) ---------------

type goldenCase struct {
	snap inventory.Snapshot
	name string
	ops  []Op
	want []wantFinding
}

// goldenCases is the ≥40-case table: each targets exactly one validator
// check in isolation (via minimal, otherwise-clean ops and snapshots) so a
// mismatch pinpoints exactly which check regressed. Cases are grouped by
// validator class in pipeline order.
func goldenCases() []goldenCase {
	pve1eno1 := &inventory.PhysNic{Ref: testRef(inventory.KindPhysNic, "pve1", "eno1"), Name: "eno1"}
	pve1eno2 := &inventory.PhysNic{Ref: testRef(inventory.KindPhysNic, "pve1", "eno2"), Name: "eno2"}

	return []goldenCase{
		// --- schema (class 1) ------------------------------------------

		{
			name: "schema: bond.create missing mode",
			ops: []Op{mkOp(OpBondCreate, testRef(inventory.KindBond, "pve1", "bond0"),
				&BondCreateParams{Slaves: []string{"eno1"}})},
			want: []wantFinding{{SeverityError, codeRequiredFieldMissing, "bond:pve1:bond0"}},
		},
		{
			name: "schema: bond.create missing slaves",
			ops: []Op{mkOp(OpBondCreate, testRef(inventory.KindBond, "pve1", "bond0"),
				&BondCreateParams{Mode: "active-backup"})},
			want: []wantFinding{{SeverityError, codeRequiredFieldMissing, "bond:pve1:bond0"}},
		},
		{
			name: "schema: bond.create mtu out of range",
			ops: []Op{mkOp(OpBondCreate, testRef(inventory.KindBond, "pve1", "bond0"),
				&BondCreateParams{Mode: "active-backup", Slaves: []string{"eno1"}, MTU: 100})},
			want: []wantFinding{{SeverityError, codeMTUOutOfRange, "bond:pve1:bond0"}},
		},
		{
			name: "schema: vlan.create vid out of range",
			ops: []Op{mkOp(OpVlanCreate, testRef(inventory.KindVlan, "pve1", "vmbr0.9000"),
				&VlanCreateParams{Parent: "vmbr0", Vid: 9000})},
			want: []wantFinding{{SeverityError, codeVIDOutOfRange, "vlan:pve1:vmbr0.9000"}},
		},
		{
			name: "schema: bridge.create vid range low>high",
			ops: []Op{mkOp(OpBridgeCreate, testRef(inventory.KindBridge, "pve1", "vmbr9"),
				&BridgeCreateParams{Vids: []VidRange{{Low: 100, High: 50}}})},
			want: []wantFinding{{SeverityError, codeVIDRangeInvalid, "bridge:pve1:vmbr9"}},
		},
		{
			name: "schema: bond.create invalid mode",
			ops: []Op{mkOp(OpBondCreate, testRef(inventory.KindBond, "pve1", "bond0"),
				&BondCreateParams{Mode: "bogus-mode", Slaves: []string{"eno1"}})},
			want: []wantFinding{{SeverityError, codeBondModeInvalid, "bond:pve1:bond0"}},
		},
		{
			name: "schema: bond.create invalid lacpRate",
			ops: []Op{mkOp(OpBondCreate, testRef(inventory.KindBond, "pve1", "bond0"),
				&BondCreateParams{Mode: "802.3ad", Slaves: []string{"eno1", "eno2"}, LACPRate: "turbo"})},
			want: []wantFinding{{SeverityError, codeLACPRateInvalid, "bond:pve1:bond0"}},
		},
		{
			name: "schema: bond.create invalid xmitHashPolicy",
			ops: []Op{mkOp(OpBondCreate, testRef(inventory.KindBond, "pve1", "bond0"),
				&BondCreateParams{Mode: "802.3ad", Slaves: []string{"eno1", "eno2"}, XmitHashPolicy: "bogus"})},
			want: []wantFinding{{SeverityError, codeXmitHashInvalid, "bond:pve1:bond0"}},
		},
		{
			name: "schema: bond.create negative miimon",
			ops: []Op{mkOp(OpBondCreate, testRef(inventory.KindBond, "pve1", "bond0"),
				&BondCreateParams{Mode: "active-backup", Slaves: []string{"eno1"}, MIIMon: -5})},
			want: []wantFinding{{SeverityError, codeMIIMonInvalid, "bond:pve1:bond0"}},
		},
		{
			name: "schema: bond.create duplicate slave",
			ops: []Op{mkOp(OpBondCreate, testRef(inventory.KindBond, "pve1", "bond0"),
				&BondCreateParams{Mode: "active-backup", Slaves: []string{"eno1", "eno1"}})},
			want: []wantFinding{{SeverityError, codeDuplicateSlave, "bond:pve1:bond0"}},
		},
		{
			name: "schema: bridge.create duplicate port",
			ops: []Op{mkOp(OpBridgeCreate, testRef(inventory.KindBridge, "pve1", "vmbr9"),
				&BridgeCreateParams{Ports: []string{"eno1", "eno1"}})},
			want: []wantFinding{{SeverityError, codeDuplicatePort, "bridge:pve1:vmbr9"}},
		},
		{
			name: "schema: bridge.create invalid address cidr",
			ops: []Op{mkOp(OpBridgeCreate, testRef(inventory.KindBridge, "pve1", "vmbr9"),
				&BridgeCreateParams{Addresses: []string{"not-a-cidr"}})},
			want: []wantFinding{{SeverityError, codeCIDRInvalid, "bridge:pve1:vmbr9"}},
		},
		{
			name: "schema: bridge.create invalid gateway ip",
			ops: []Op{mkOp(OpBridgeCreate, testRef(inventory.KindBridge, "pve1", "vmbr9"),
				&BridgeCreateParams{Gateway: "not-an-ip"})},
			want: []wantFinding{{SeverityError, codeIPInvalid, "bridge:pve1:vmbr9"}},
		},
		{
			name: "schema: ipam.alloc.create invalid mac",
			ops: []Op{mkOp(OpIpamAllocCreate, testRef(inventory.KindSDNSubnet, "", "10.0.0.0/24"),
				&IpamAllocCreateParams{CIDR: "10.0.0.5/32", MAC: "zz:zz:zz"})},
			want: []wantFinding{{SeverityError, codeMACInvalid, "sdn-subnet::10.0.0.0/24"}},
		},
		{
			name: "schema: sdn.subnet.create invalid dhcp range",
			ops: []Op{mkOp(OpSdnSubnetCreate, testRef(inventory.KindSDNSubnet, "", "10.0.0.0/24"),
				&SdnSubnetCreateParams{Vnet: "zone1/vnet1", CIDR: "10.0.0.0/24", DHCPRanges: []string{"bogus"}})},
			want: []wantFinding{{SeverityError, codeDHCPRangeInvalid, "sdn-subnet::10.0.0.0/24"}},
		},
		{
			name: "schema: sdn.zone.create invalid type",
			ops: []Op{mkOp(OpSdnZoneCreate, testRef(inventory.KindSDNZone, "", "zone9"),
				&SdnZoneCreateParams{Type: "bogus"})},
			want: []wantFinding{{SeverityError, codeSDNZoneTypeInvalid, "sdn-zone::zone9"}},
		},
		{
			name: "schema: guest.nic.update negative rate",
			ops: []Op{mkOp(OpGuestNicUpdate, testRef(inventory.KindGuestNic, "pve1", "100/net0"),
				&GuestNicUpdateParams{RateMbps: intPtr(-5)})},
			want: []wantFinding{{SeverityError, codeRateInvalid, "guest-nic:pve1:100/net0"}},
		},
		{
			name: "schema: fw.rule.create invalid direction",
			ops: []Op{mkOp(OpFwRuleCreate, testRef(inventory.KindFwRuleset, "", "cluster"),
				&FwRuleCreateParams{Direction: "sideways", Action: "ACCEPT", Pos: 0})},
			want: []wantFinding{{SeverityError, codeFwDirectionInvalid, "fw-ruleset::cluster"}},
		},
		{
			name: "schema: fw.rule.create invalid action",
			ops: []Op{mkOp(OpFwRuleCreate, testRef(inventory.KindFwRuleset, "", "cluster"),
				&FwRuleCreateParams{Direction: "in", Action: "MAYBE", Pos: 0})},
			want: []wantFinding{{SeverityError, codeFwActionInvalid, "fw-ruleset::cluster"}},
		},
		{
			name: "schema: fw.options.update invalid policy",
			ops: []Op{mkOp(OpFwOptionsUpdate, testRef(inventory.KindFwRuleset, "", "cluster"),
				&FwOptionsUpdateParams{DefaultIn: strPtr("MAYBE")})},
			want: []wantFinding{{SeverityError, codeFwPolicyInvalid, "fw-ruleset::cluster"}},
		},
		{
			name: "schema: fw.rule.create invalid log level",
			ops: []Op{mkOp(OpFwRuleCreate, testRef(inventory.KindFwRuleset, "", "cluster"),
				&FwRuleCreateParams{Direction: "in", Action: "ACCEPT", Pos: 0, Log: "screaming"})},
			want: []wantFinding{{SeverityError, codeFwLogInvalid, "fw-ruleset::cluster"}},
		},
		{
			name: "schema: fw.rule.delete negative pos",
			ops: []Op{mkOp(OpFwRuleDelete, testRef(inventory.KindFwRuleset, "", "cluster"),
				&FwRuleDeleteParams{Pos: -1})},
			want: []wantFinding{{SeverityError, codeFwPosInvalid, "fw-ruleset::cluster"}},
		},

		// --- referential (class 2) --------------------------------------

		{
			name: "referential: bond.update target not found",
			ops:  []Op{mkOp(OpBondUpdate, testRef(inventory.KindBond, "pve1", "bond0"), &BondUpdateParams{})},
			want: []wantFinding{{SeverityError, codeTargetNotFound, "bond:pve1:bond0"}},
		},
		{
			name: "referential: bond.create already exists",
			snap: buildSnapshot(
				pve1eno1,
				&inventory.Bond{Ref: testRef(inventory.KindBond, "pve1", "bond0"), Name: "bond0"},
			),
			ops: []Op{mkOp(OpBondCreate, testRef(inventory.KindBond, "pve1", "bond0"),
				&BondCreateParams{Mode: "active-backup", Slaves: []string{"eno1"}})},
			want: []wantFinding{{SeverityError, codeAlreadyExists, "bond:pve1:bond0"}},
		},
		{
			name: "referential: vlan.create parent not found",
			ops: []Op{mkOp(OpVlanCreate, testRef(inventory.KindVlan, "pve1", "vmbr0.20"),
				&VlanCreateParams{Parent: "vmbr0", Vid: 20})},
			want: []wantFinding{{SeverityError, codeParentNotFound, "vlan:pve1:vmbr0.20"}},
		},
		{
			name: "referential: bond.create slave not found",
			ops: []Op{mkOp(OpBondCreate, testRef(inventory.KindBond, "pve1", "bond0"),
				&BondCreateParams{Mode: "active-backup", Slaves: []string{"eno1"}})},
			want: []wantFinding{{SeverityError, codeSlaveNotFound, "bond:pve1:bond0"}},
		},
		{
			name: "referential: bridge.port.add port not found",
			snap: buildSnapshot(&inventory.Bridge{Ref: testRef(inventory.KindBridge, "pve1", "vmbr0"), Name: "vmbr0"}),
			ops: []Op{mkOp(OpBridgePortAdd, testRef(inventory.KindBridge, "pve1", "vmbr0"),
				&BridgePortAddParams{Port: "eno1"})},
			want: []wantFinding{{SeverityError, codePortNotFound, "bridge:pve1:vmbr0"}},
		},
		{
			name: "referential: bridge.port.remove not attached",
			snap: buildSnapshot(
				&inventory.Bridge{Ref: testRef(inventory.KindBridge, "pve1", "vmbr0"), Name: "vmbr0"},
				pve1eno1,
			),
			ops: []Op{mkOp(OpBridgePortRemove, testRef(inventory.KindBridge, "pve1", "vmbr0"),
				&BridgePortRemoveParams{Port: "eno1"})},
			want: []wantFinding{{SeverityError, codePortNotAttached, "bridge:pve1:vmbr0"}},
		},
		{
			name: "referential: bridge.port.add duplicate enslavement",
			snap: buildSnapshot(
				&inventory.Bridge{Ref: testRef(inventory.KindBridge, "pve1", "vmbr0"), Name: "vmbr0", PortNames: []string{"eno1"}},
				&inventory.Bridge{Ref: testRef(inventory.KindBridge, "pve1", "vmbr1"), Name: "vmbr1"},
				pve1eno1,
			),
			ops: []Op{mkOp(OpBridgePortAdd, testRef(inventory.KindBridge, "pve1", "vmbr1"),
				&BridgePortAddParams{Port: "eno1"})},
			want: []wantFinding{{SeverityError, codeDuplicateEnslavement, "bridge:pve1:vmbr1"}},
		},
		{
			name: "referential: sdn.vnet.create zone not found",
			ops: []Op{mkOp(OpSdnVnetCreate, testRef(inventory.KindSDNVnet, "", "zoneX/vnet1"),
				&SdnVnetCreateParams{Zone: "zoneX"})},
			want: []wantFinding{{SeverityError, codeZoneNotFound, "sdn-vnet::zoneX/vnet1"}},
		},
		{
			name: "referential: sdn.subnet.create vnet not found",
			ops: []Op{mkOp(OpSdnSubnetCreate, testRef(inventory.KindSDNSubnet, "", "10.0.0.0/24"),
				&SdnSubnetCreateParams{Vnet: "zoneX/vnet1", CIDR: "10.0.0.0/24"})},
			want: []wantFinding{{SeverityError, codeVnetNotFound, "sdn-subnet::10.0.0.0/24"}},
		},
		{
			name: "referential: sdn.zone.create node not found",
			ops: []Op{mkOp(OpSdnZoneCreate, testRef(inventory.KindSDNZone, "", "zone9"),
				&SdnZoneCreateParams{Type: "simple", Nodes: []string{"pve9"}})},
			want: []wantFinding{{SeverityError, codeNodeNotFound, "sdn-zone::zone9"}},
		},
		{
			name: "referential: guest.nic.update bridge/vnet not found",
			snap: buildSnapshot(&inventory.GuestNic{Ref: testRef(inventory.KindGuestNic, "pve1", "100/net0"), Guest: testRef(inventory.KindGuest, "pve1", "100")}),
			ops: []Op{mkOp(OpGuestNicUpdate, testRef(inventory.KindGuestNic, "pve1", "100/net0"),
				&GuestNicUpdateParams{BridgeOrVnet: strPtr("vmbr99")})},
			want: []wantFinding{{SeverityError, codeBridgeOrVnetNotFound, "guest-nic:pve1:100/net0"}},
		},
		{
			name: "referential: bridge.create vid overlap within op",
			ops: []Op{mkOp(OpBridgeCreate, testRef(inventory.KindBridge, "pve1", "vmbr9"),
				&BridgeCreateParams{VlanAware: true, Vids: []VidRange{{Low: 10, High: 20}, {Low: 15, High: 25}}})},
			want: []wantFinding{{SeverityError, codeVIDOverlap, "bridge:pve1:vmbr9"}},
		},
		{
			name: "referential: vlan.create duplicate parent+vid",
			snap: buildSnapshot(
				&inventory.Bridge{Ref: testRef(inventory.KindBridge, "pve1", "vmbr0"), Name: "vmbr0"},
				&inventory.VlanIface{Ref: testRef(inventory.KindVlan, "pve1", "vmbr0.20"), Name: "vmbr0.20", ParentName: "vmbr0", Vid: 20},
			),
			ops: []Op{mkOp(OpVlanCreate, testRef(inventory.KindVlan, "pve1", "vmbr0.20-second"),
				&VlanCreateParams{Parent: "vmbr0", Vid: 20})},
			want: []wantFinding{{SeverityError, codeVIDOverlap, "vlan:pve1:vmbr0.20-second"}},
		},
		{
			name: "referential: bridge.create address overlaps existing",
			snap: buildSnapshot(&inventory.Bridge{Ref: testRef(inventory.KindBridge, "pve1", "vmbr0"), Name: "vmbr0", Addresses: []string{"10.0.0.1/24"}}),
			ops: []Op{mkOp(OpBridgeCreate, testRef(inventory.KindBridge, "pve1", "vmbr9"),
				&BridgeCreateParams{Comments: "x", Addresses: []string{"10.0.0.5/24"}})},
			want: []wantFinding{{SeverityError, codeAddressOverlap, "bridge:pve1:vmbr9"}},
		},
		{
			name: "referential: bridge.create address overlaps within op",
			ops: []Op{mkOp(OpBridgeCreate, testRef(inventory.KindBridge, "pve1", "vmbr9"),
				&BridgeCreateParams{Comments: "x", Addresses: []string{"10.0.0.1/24", "10.0.0.5/24"}})},
			want: []wantFinding{{SeverityError, codeAddressOverlap, "bridge:pve1:vmbr9"}},
		},
		{
			name: "referential: ipam.alloc.create out of subnet",
			snap: buildSnapshot(&inventory.SdnSubnet{Ref: testRef(inventory.KindSDNSubnet, "", "10.0.0.0/24"), ID: "10.0.0.0/24", Vnet: "zone1/vnet1"}),
			ops: []Op{mkOp(OpIpamAllocCreate, testRef(inventory.KindSDNSubnet, "", "10.0.0.0/24"),
				&IpamAllocCreateParams{CIDR: "192.168.1.5/32"})},
			want: []wantFinding{{SeverityError, codeAddressOutOfSubnet, "sdn-subnet::10.0.0.0/24"}},
		},
		{
			name: "referential: fw.rule.update pos out of range",
			snap: buildSnapshot(&inventory.FwRuleset{
				Ref: testRef(inventory.KindFwRuleset, "", "cluster"), Scope: inventory.FwScopeCluster,
				Rules: []inventory.FwRule{{Pos: 0, Direction: "in", Action: "ACCEPT"}, {Pos: 1, Direction: "in", Action: "DROP"}},
			}),
			ops: []Op{mkOp(OpFwRuleUpdate, testRef(inventory.KindFwRuleset, "", "cluster"),
				&FwRuleUpdateParams{Pos: 5, Action: strPtr("DROP")})},
			want: []wantFinding{{SeverityError, codeFwPosOutOfRange, "fw-ruleset::cluster"}},
		},
		{
			name: "referential: fw.rule.update ruleset not found",
			ops: []Op{mkOp(OpFwRuleUpdate, testRef(inventory.KindFwRuleset, "", "cluster"),
				&FwRuleUpdateParams{Pos: 0, Action: strPtr("DROP")})},
			want: []wantFinding{{SeverityError, codeTargetNotFound, "fw-ruleset::cluster"}},
		},
		{
			name: "referential: fw.alias.create duplicate within changeset",
			ops: []Op{
				mkOp(OpFwAliasCreate, testRef(inventory.KindFwRuleset, "", "cluster"), &FwAliasCreateParams{Name: "office", CIDR: "203.0.113.0/24"}),
				mkOp(OpFwAliasCreate, testRef(inventory.KindFwRuleset, "", "cluster"), &FwAliasCreateParams{Name: "office", CIDR: "203.0.113.0/25"}),
			},
			want: []wantFinding{{SeverityError, codeAlreadyExists, "fw-ruleset::cluster"}},
		},
		{
			name: "referential: sdn.subnet.create overlaps sibling subnet",
			snap: buildSnapshot(
				&inventory.SdnZone{Ref: testRef(inventory.KindSDNZone, "", "zone1"), ID: "zone1", Type: "vxlan"},
				&inventory.SdnVnet{Ref: testRef(inventory.KindSDNVnet, "", "zone1/vnet1"), ID: "zone1/vnet1", Zone: "zone1"},
				&inventory.SdnSubnet{Ref: testRef(inventory.KindSDNSubnet, "", "10.0.0.0/24"), ID: "10.0.0.0/24", Vnet: "zone1/vnet1"},
			),
			ops: []Op{mkOp(OpSdnSubnetCreate, testRef(inventory.KindSDNSubnet, "", "10.0.0.128/25"),
				&SdnSubnetCreateParams{Vnet: "zone1/vnet1", CIDR: "10.0.0.128/25"})},
			want: []wantFinding{{SeverityError, codeAddressOverlap, "sdn-subnet::10.0.0.128/25"}},
		},
		{
			name: "referential: ipam.alloc.create overlaps sibling allocation within changeset",
			snap: buildSnapshot(&inventory.SdnSubnet{Ref: testRef(inventory.KindSDNSubnet, "", "10.0.0.0/24"), ID: "10.0.0.0/24", Vnet: "zone1/vnet1"}),
			ops: []Op{
				mkOp(OpIpamAllocCreate, testRef(inventory.KindSDNSubnet, "", "10.0.0.0/24"), &IpamAllocCreateParams{CIDR: "10.0.0.10/32"}),
				mkOp(OpIpamAllocCreate, testRef(inventory.KindSDNSubnet, "", "10.0.0.0/24"), &IpamAllocCreateParams{CIDR: "10.0.0.10/32"}),
			},
			want: []wantFinding{{SeverityError, codeAddressOverlap, "sdn-subnet::10.0.0.0/24"}},
		},

		// --- advisory (class 5) -----------------------------------------

		{
			name: "advisory: 802.3ad bond without layer3+4 hash policy",
			snap: buildSnapshot(pve1eno1, pve1eno2),
			ops: []Op{mkOp(OpBondCreate, testRef(inventory.KindBond, "pve1", "bond0"),
				&BondCreateParams{Mode: "802.3ad", Slaves: []string{"eno1", "eno2"}, XmitHashPolicy: "layer2"})},
			want: []wantFinding{{SeverityWarning, codeAdvisoryBondHashPolicy, "bond:pve1:bond0"}},
		},
		{
			name: "advisory: single-slave bond",
			snap: buildSnapshot(pve1eno1),
			ops: []Op{mkOp(OpBondCreate, testRef(inventory.KindBond, "pve1", "bond0"),
				&BondCreateParams{Mode: "active-backup", Slaves: []string{"eno1"}})},
			want: []wantFinding{{SeverityWarning, codeAdvisorySingleSlave, "bond:pve1:bond0"}},
		},
		{
			name: "advisory: bridge without description",
			ops: []Op{mkOp(OpBridgeCreate, testRef(inventory.KindBridge, "pve1", "vmbr9"),
				&BridgeCreateParams{})},
			want: []wantFinding{{SeverityWarning, codeAdvisoryBridgeComment, "bridge:pve1:vmbr9"}},
		},

		// --- clean (happy path) ------------------------------------------

		{
			name: "clean: bridge.create with description and mtu",
			ops: []Op{mkOp(OpBridgeCreate, testRef(inventory.KindBridge, "pve1", "vmbr9"),
				&BridgeCreateParams{Comments: "core uplink", MTU: 1500})},
			want: nil,
		},
		{
			name: "clean: 802.3ad bond with two slaves and layer3+4 hash",
			snap: buildSnapshot(pve1eno1, pve1eno2),
			ops: []Op{mkOp(OpBondCreate, testRef(inventory.KindBond, "pve1", "bond0"),
				&BondCreateParams{Mode: "802.3ad", Slaves: []string{"eno1", "eno2"}, XmitHashPolicy: "layer3+4"})},
			want: nil,
		},
		{
			name: "clean: sdn.zone.create with no nodes restriction",
			ops: []Op{mkOp(OpSdnZoneCreate, testRef(inventory.KindSDNZone, "", "zone1"),
				&SdnZoneCreateParams{Type: "vxlan"})},
			want: nil,
		},
		{
			name: "clean: bridge.create with a valid, unenslaved port",
			snap: buildSnapshot(pve1eno1),
			ops: []Op{mkOp(OpBridgeCreate, testRef(inventory.KindBridge, "pve1", "vmbr9"),
				&BridgeCreateParams{Comments: "x", Ports: []string{"eno1"}})},
			want: nil,
		},
		{
			name: "clean: vlan.create with valid parent",
			snap: buildSnapshot(&inventory.Bridge{Ref: testRef(inventory.KindBridge, "pve1", "vmbr0"), Name: "vmbr0"}),
			ops: []Op{mkOp(OpVlanCreate, testRef(inventory.KindVlan, "pve1", "vmbr0.20"),
				&VlanCreateParams{Parent: "vmbr0", Vid: 20, Addresses: []string{"10.20.0.1/24"}})},
			want: nil,
		},
		{
			name: "clean: fw.rule.create with no ruleset yet",
			ops: []Op{mkOp(OpFwRuleCreate, testRef(inventory.KindFwRuleset, "", "cluster"),
				&FwRuleCreateParams{Direction: "in", Action: "ACCEPT", Pos: 0})},
			want: nil,
		},
	}
}

func TestValidate_Golden(t *testing.T) {
	cases := goldenCases()
	if len(cases) < 40 {
		t.Fatalf("goldenCases has %d cases, want at least 40 (T-202 acceptance criterion 1)", len(cases))
	}
	emptySnap := buildSnapshot()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap := tc.snap
			if snap == (inventory.Snapshot{}) {
				snap = emptySnap
			}
			got := Validate(tc.ops, snap)
			assertFindings(t, got, tc.want)
		})
	}
}

// --- intra-changeset ordering (T-202 acceptance criterion 2) --------------

func TestValidate_IntraChangesetOrdering(t *testing.T) {
	snap := buildSnapshot(
		&inventory.PhysNic{Ref: testRef(inventory.KindPhysNic, "pve1", "eno1"), Name: "eno1"},
		&inventory.Bridge{Ref: testRef(inventory.KindBridge, "pve1", "vmbr0"), Name: "vmbr0"},
	)

	createBond := mkOp(OpBondCreate, testRef(inventory.KindBond, "pve1", "bond0"),
		&BondCreateParams{Mode: "802.3ad", Slaves: []string{"eno1"}, XmitHashPolicy: "layer3+4"})
	enslaveBond := mkOp(OpBridgePortAdd, testRef(inventory.KindBridge, "pve1", "vmbr0"),
		&BridgePortAddParams{Port: "bond0"})

	t.Run("create then enslave validates clean", func(t *testing.T) {
		findings := Validate([]Op{createBond, enslaveBond}, snap)
		if hasError(findings) {
			t.Errorf("forward order: unexpected error findings: %+v", findings)
		}
	})

	t.Run("enslave then create errors (bond0 does not exist yet)", func(t *testing.T) {
		findings := Validate([]Op{enslaveBond, createBond}, snap)
		assertFindings(t, findings, []wantFinding{{SeverityError, codePortNotFound, "bridge:pve1:vmbr0"}})
	})
}

// --- coverage sweep: every op type has at least one passing case ---------

// TestValidate_EveryOpTypeHasAPassingCase is not itself an acceptance
// criterion, but rounds out the "schema validators for every op" deliverable
// by proving every one of the 39 documented op types (not just the ones the
// golden/fix corpora happen to exercise via an error case) can validate
// clean against a snapshot built for it.
func TestValidate_EveryOpTypeHasAPassingCase(t *testing.T) {
	snap := buildSnapshot(
		&inventory.PhysNic{Ref: testRef(inventory.KindPhysNic, "pve1", "eno1"), Name: "eno1"},
		&inventory.Bond{Ref: testRef(inventory.KindBond, "pve1", "bond0"), Name: "bond0", Mode: "active-backup", Slaves: []string{"eno1"}},
		&inventory.Bridge{Ref: testRef(inventory.KindBridge, "pve1", "vmbr0"), Name: "vmbr0"},
		&inventory.VlanIface{Ref: testRef(inventory.KindVlan, "pve1", "vmbr0.20"), Name: "vmbr0.20", ParentName: "vmbr0", Vid: 20},
		&inventory.SdnZone{Ref: testRef(inventory.KindSDNZone, "", "zone1"), ID: "zone1", Type: "vxlan"},
		&inventory.SdnVnet{Ref: testRef(inventory.KindSDNVnet, "", "zone1/vnet1"), ID: "zone1/vnet1", Zone: "zone1"},
		&inventory.SdnSubnet{Ref: testRef(inventory.KindSDNSubnet, "", "10.0.0.0/24"), ID: "10.0.0.0/24", Vnet: "zone1/vnet1"},
		&inventory.GuestNic{Ref: testRef(inventory.KindGuestNic, "pve1", "100/net0"), Guest: testRef(inventory.KindGuest, "pve1", "100")},
		&inventory.FwRuleset{Ref: testRef(inventory.KindFwRuleset, "", "cluster"), Scope: inventory.FwScopeCluster,
			Rules: []inventory.FwRule{{Pos: 0, Direction: "in", Action: "ACCEPT"}}},
	)

	cases := []struct {
		name string
		op   Op
	}{
		{"iface.update", mkOp(OpIfaceUpdate, testRef(inventory.KindPhysNic, "pve1", "eno1"), &IfaceUpdateParams{MTU: intPtr(1500)})},
		{"bond.update", mkOp(OpBondUpdate, testRef(inventory.KindBond, "pve1", "bond0"), &BondUpdateParams{MTU: intPtr(1500)})},
		{"bond.delete", mkOp(OpBondDelete, testRef(inventory.KindBond, "pve1", "bond0"), &BondDeleteParams{})},
		{"bridge.update", mkOp(OpBridgeUpdate, testRef(inventory.KindBridge, "pve1", "vmbr0"), &BridgeUpdateParams{MTU: intPtr(1500)})},
		{"bridge.delete", mkOp(OpBridgeDelete, testRef(inventory.KindBridge, "pve1", "vmbr0"), &BridgeDeleteParams{})},
		{"vlan.update", mkOp(OpVlanUpdate, testRef(inventory.KindVlan, "pve1", "vmbr0.20"), &VlanUpdateParams{MTU: intPtr(1500)})},
		{"vlan.delete", mkOp(OpVlanDelete, testRef(inventory.KindVlan, "pve1", "vmbr0.20"), &VlanDeleteParams{})},
		{"sdn.zone.update", mkOp(OpSdnZoneUpdate, testRef(inventory.KindSDNZone, "", "zone1"), &SdnZoneUpdateParams{MTU: intPtr(1500)})},
		{"sdn.zone.delete", mkOp(OpSdnZoneDelete, testRef(inventory.KindSDNZone, "", "zone1"), &SdnZoneDeleteParams{})},
		{"sdn.vnet.update", mkOp(OpSdnVnetUpdate, testRef(inventory.KindSDNVnet, "", "zone1/vnet1"), &SdnVnetUpdateParams{Alias: strPtr("prod")})},
		{"sdn.vnet.delete", mkOp(OpSdnVnetDelete, testRef(inventory.KindSDNVnet, "", "zone1/vnet1"), &SdnVnetDeleteParams{})},
		{"sdn.subnet.update", mkOp(OpSdnSubnetUpdate, testRef(inventory.KindSDNSubnet, "", "10.0.0.0/24"), &SdnSubnetUpdateParams{Gateway: strPtr("10.0.0.1")})},
		{"sdn.subnet.delete", mkOp(OpSdnSubnetDelete, testRef(inventory.KindSDNSubnet, "", "10.0.0.0/24"), &SdnSubnetDeleteParams{})},
		{"sdn.apply", mkOp(OpSdnApply, inventory.Ref{}, &SdnApplyParams{})},
		{"fw.rule.move", mkOp(OpFwRuleMove, testRef(inventory.KindFwRuleset, "", "cluster"), &FwRuleMoveParams{FromPos: 0, ToPos: 0})},
		{"fw.alias.update", mkOp(OpFwAliasUpdate, testRef(inventory.KindFwRuleset, "", "cluster"), &FwAliasUpdateParams{Name: "office", CIDR: strPtr("203.0.113.0/25")})},
		{"fw.alias.delete", mkOp(OpFwAliasDelete, testRef(inventory.KindFwRuleset, "", "cluster"), &FwAliasDeleteParams{Name: "office"})},
		{"fw.ipset.create", mkOp(OpFwIpsetCreate, testRef(inventory.KindFwRuleset, "", "cluster"), &FwIpsetCreateParams{Name: "blocklist", CIDRs: []string{"198.51.100.0/24"}})},
		{"fw.ipset.update", mkOp(OpFwIpsetUpdate, testRef(inventory.KindFwRuleset, "", "cluster"), &FwIpsetUpdateParams{Name: "blocklist", CIDRs: strsPtr("198.51.100.0/24")})},
		{"fw.ipset.delete", mkOp(OpFwIpsetDelete, testRef(inventory.KindFwRuleset, "", "cluster"), &FwIpsetDeleteParams{Name: "blocklist"})},
		{"fw.group.create", mkOp(OpFwGroupCreate, testRef(inventory.KindFwRuleset, "", "cluster"), &FwGroupCreateParams{Name: "web", Rules: []FwRuleSpec{{Direction: "in", Action: "ACCEPT"}}})},
		{"fw.group.update", mkOp(OpFwGroupUpdate, testRef(inventory.KindFwRuleset, "", "cluster"), &FwGroupUpdateParams{Name: "web", Comment: strPtr("web tier")})},
		{"fw.group.delete", mkOp(OpFwGroupDelete, testRef(inventory.KindFwRuleset, "", "cluster"), &FwGroupDeleteParams{Name: "web"})},
		{"ipam.alloc.delete", mkOp(OpIpamAllocDelete, testRef(inventory.KindSDNSubnet, "", "10.0.0.0/24"), &IpamAllocDeleteParams{CIDR: "10.0.0.5/32"})},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings := Validate([]Op{tc.op}, snap)
			if hasError(findings) {
				t.Errorf("unexpected error findings for %s: %+v", tc.name, findings)
			}
		})
	}
}
