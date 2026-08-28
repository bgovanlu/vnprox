// SPDX-License-Identifier: Apache-2.0

package change_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

func ptrStr(s string) *string                        { return &s }
func ptrInt(i int) *int                              { return &i }
func ptrStrs(s []string) *[]string                   { return &s }
func ptrVids(v []change.VidRange) *[]change.VidRange { return &v }

func rulesetRef() inventory.Ref {
	return inventory.Ref{Kind: inventory.KindFwRuleset, ID: "cluster"}
}

// TestSchemaValidate_AllOpFamilies drives the schema validator (T-202) across
// every op family with deliberately invalid field values, so each op case's
// range/enum/syntax branch executes and reports at least one error. It rounds
// out coverage of the large schemaValidateOp switch for the SDN/firewall/IPAM/
// guest op families beyond what the apply-engine tests exercise.
func TestSchemaValidate_AllOpFamilies(t *testing.T) {
	snap := inventory.NewGraph().Snapshot()

	physnic := inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno1"}
	bond := inventory.Ref{Kind: inventory.KindBond, Node: "pve1", ID: "bond0"}
	bridge := inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}
	vlan := inventory.Ref{Kind: inventory.KindVlan, Node: "pve1", ID: "vmbr0.10"}
	zone := inventory.Ref{Kind: inventory.KindSDNZone, ID: "zone1"}
	vnet := inventory.Ref{Kind: inventory.KindSDNVnet, ID: "zone1/vnet1"}
	subnet := inventory.Ref{Kind: inventory.KindSDNSubnet, ID: "10.0.0.0/24"}
	guestNic := inventory.Ref{Kind: inventory.KindGuestNic, Node: "pve1", ID: "100/net0"}

	badVids := []change.VidRange{{Low: 10, High: 9999}} // High out of range → schema error + clamp fix

	ops := []change.Op{
		{Type: change.OpIfaceUpdate, Target: physnic, Params: &change.IfaceUpdateParams{
			MTU: ptrInt(100), Addresses: ptrStrs([]string{"not-a-cidr"}), Gateway: ptrStr("not-an-ip")}},

		{Type: change.OpBondCreate, Target: bond, Params: &change.BondCreateParams{
			Mode: "nope", Slaves: []string{"a", "a"}, LACPRate: "nope", XmitHashPolicy: "nope", MIIMon: -1, MTU: 100}},
		{Type: change.OpBondUpdate, Target: bond, Params: &change.BondUpdateParams{
			Mode: ptrStr("nope"), Slaves: ptrStrs([]string{"x", "x"}), LACPRate: ptrStr("nope"),
			XmitHashPolicy: ptrStr("nope"), MIIMon: ptrInt(-1), MTU: ptrInt(100)}},

		{Type: change.OpBridgeCreate, Target: bridge, Params: &change.BridgeCreateParams{
			Ports: []string{"p", "p"}, Vids: badVids, Addresses: []string{"bad"}, Gateway: "bad", MTU: 100}},
		{Type: change.OpBridgeUpdate, Target: bridge, Params: &change.BridgeUpdateParams{
			Vids: ptrVids(badVids), Addresses: ptrStrs([]string{"bad"}), Gateway: ptrStr("bad"), MTU: ptrInt(100)}},

		{Type: change.OpVlanCreate, Target: vlan, Params: &change.VlanCreateParams{
			Parent: "", Vid: 9999, Addresses: []string{"bad"}, MTU: 100}},
		{Type: change.OpVlanUpdate, Target: vlan, Params: &change.VlanUpdateParams{
			Addresses: ptrStrs([]string{"bad"}), MTU: ptrInt(100)}},

		{Type: change.OpSdnZoneCreate, Target: zone, Params: &change.SdnZoneCreateParams{Type: "nope", MTU: 100}},
		{Type: change.OpSdnZoneUpdate, Target: zone, Params: &change.SdnZoneUpdateParams{MTU: ptrInt(100)}},
		{Type: change.OpSdnVnetCreate, Target: vnet, Params: &change.SdnVnetCreateParams{Zone: "", Tag: 9999}},
		{Type: change.OpSdnVnetUpdate, Target: vnet, Params: &change.SdnVnetUpdateParams{Tag: ptrInt(9999)}},
		{Type: change.OpSdnSubnetCreate, Target: subnet, Params: &change.SdnSubnetCreateParams{
			Vnet: "", CIDR: "bad", Gateway: "bad", DHCPRanges: []string{"bad"}}},
		{Type: change.OpSdnSubnetUpdate, Target: subnet, Params: &change.SdnSubnetUpdateParams{
			Gateway: ptrStr("bad"), DHCPRanges: ptrStrs([]string{"bad"})}},

		{Type: change.OpGuestNicUpdate, Target: guestNic, Params: &change.GuestNicUpdateParams{
			BridgeOrVnet: ptrStr(""), Vid: ptrInt(9999), RateMbps: ptrInt(-1)}},

		{Type: change.OpFwRuleCreate, Target: rulesetRef(), Params: &change.FwRuleCreateParams{
			Direction: "nope", Action: "nope", Log: "nope", Pos: -1}},
		{Type: change.OpFwRuleUpdate, Target: rulesetRef(), Params: &change.FwRuleUpdateParams{
			Direction: ptrStr("nope"), Action: ptrStr("nope"), Log: ptrStr("nope"), Pos: -1}},
		{Type: change.OpFwRuleDelete, Target: rulesetRef(), Params: &change.FwRuleDeleteParams{Pos: -1}},
		{Type: change.OpFwRuleMove, Target: rulesetRef(), Params: &change.FwRuleMoveParams{FromPos: -1, ToPos: -1}},
		{Type: change.OpFwOptionsUpdate, Target: rulesetRef(), Params: &change.FwOptionsUpdateParams{
			DefaultIn: ptrStr("nope"), DefaultOut: ptrStr("nope")}},
		{Type: change.OpFwAliasCreate, Target: rulesetRef(), Params: &change.FwAliasCreateParams{Name: "", CIDR: "bad"}},
		{Type: change.OpFwAliasUpdate, Target: rulesetRef(), Params: &change.FwAliasUpdateParams{Name: "", CIDR: ptrStr("bad")}},
		{Type: change.OpFwAliasDelete, Target: rulesetRef(), Params: &change.FwAliasDeleteParams{Name: ""}},
		{Type: change.OpFwIpsetCreate, Target: rulesetRef(), Params: &change.FwIpsetCreateParams{Name: "", CIDRs: []string{"bad"}}},
		{Type: change.OpFwIpsetUpdate, Target: rulesetRef(), Params: &change.FwIpsetUpdateParams{Name: "", CIDRs: ptrStrs([]string{"bad"})}},
		{Type: change.OpFwIpsetDelete, Target: rulesetRef(), Params: &change.FwIpsetDeleteParams{Name: ""}},
		{Type: change.OpFwGroupCreate, Target: rulesetRef(), Params: &change.FwGroupCreateParams{
			Name: "g", Rules: []change.FwRuleSpec{{Direction: "nope", Action: "nope"}}}},
		{Type: change.OpFwGroupUpdate, Target: rulesetRef(), Params: &change.FwGroupUpdateParams{
			Name: "g", Rules: ptrStrsRuleSpecs()}},
		{Type: change.OpFwGroupDelete, Target: rulesetRef(), Params: &change.FwGroupDeleteParams{Name: ""}},

		{Type: change.OpIpamAllocCreate, Target: subnet, Params: &change.IpamAllocCreateParams{CIDR: "bad", MAC: "not-a-mac"}},
		{Type: change.OpIpamAllocDelete, Target: subnet, Params: &change.IpamAllocDeleteParams{CIDR: "bad"}},
	}

	for i, op := range ops {
		findings := change.Validate([]change.Op{op}, snap)
		if len(findings) == 0 {
			t.Errorf("op[%d] %s produced no findings; expected a schema error", i, op.Type)
		}
	}
}

func ptrStrsRuleSpecs() *[]change.FwRuleSpec {
	r := []change.FwRuleSpec{{Direction: "nope", Action: "nope"}}
	return &r
}
