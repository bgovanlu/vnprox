package change

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

const restoreTestLo = "auto lo\niface lo inet loopback\n\n"

func TestRestoreOpsForNode_NoDiff(t *testing.T) {
	ops, err := restoreOpsForNode("pve1", restoreTestLo, restoreTestLo)
	if err != nil {
		t.Fatalf("restoreOpsForNode: %v", err)
	}
	if len(ops) != 0 {
		t.Fatalf("ops = %+v, want none for identical content", ops)
	}
}

func TestRestoreOpsForNode_BridgeCreate(t *testing.T) {
	target := restoreTestLo + "auto vmbr1\niface vmbr1 inet manual\n\tbridge-ports eno1\n"
	ops, err := restoreOpsForNode("pve1", target, restoreTestLo)
	if err != nil {
		t.Fatalf("restoreOpsForNode: %v", err)
	}
	if len(ops) != 1 || ops[0].Type != OpBridgeCreate || ops[0].Target.ID != "vmbr1" {
		t.Fatalf("ops = %+v, want a single bridge.create vmbr1", ops)
	}
	p, ok := ops[0].Params.(*BridgeCreateParams)
	if !ok || len(p.Ports) != 1 || p.Ports[0] != "eno1" {
		t.Fatalf("bridge.create params = %+v, want ports=[eno1]", ops[0].Params)
	}
}

func TestRestoreOpsForNode_BridgeDelete(t *testing.T) {
	live := restoreTestLo + "auto vmbr1\niface vmbr1 inet manual\n\tbridge-ports eno1\n"
	ops, err := restoreOpsForNode("pve1", restoreTestLo, live)
	if err != nil {
		t.Fatalf("restoreOpsForNode: %v", err)
	}
	if len(ops) != 1 || ops[0].Type != OpBridgeDelete || ops[0].Target.ID != "vmbr1" {
		t.Fatalf("ops = %+v, want a single bridge.delete vmbr1", ops)
	}
}

func TestRestoreOpsForNode_BridgePortAndMTUUpdate(t *testing.T) {
	live := restoreTestLo + "auto vmbr1\niface vmbr1 inet manual\n\tbridge-ports eno1\n"
	target := restoreTestLo + "auto vmbr1\niface vmbr1 inet manual\n\tbridge-ports eno2\n\tmtu 9000\n"

	ops, err := restoreOpsForNode("pve1", target, live)
	if err != nil {
		t.Fatalf("restoreOpsForNode: %v", err)
	}
	var sawRemove, sawAdd, sawUpdate bool
	for _, op := range ops {
		switch op.Type {
		case OpBridgePortRemove:
			if p := op.Params.(*BridgePortRemoveParams); p.Port == "eno1" {
				sawRemove = true
			}
		case OpBridgePortAdd:
			if p := op.Params.(*BridgePortAddParams); p.Port == "eno2" {
				sawAdd = true
			}
		case OpBridgeUpdate:
			p := op.Params.(*BridgeUpdateParams)
			if p.MTU != nil && *p.MTU == 9000 {
				sawUpdate = true
			}
		}
	}
	if !sawRemove || !sawAdd || !sawUpdate {
		t.Fatalf("ops = %+v, want port.remove(eno1) + port.add(eno2) + update(mtu=9000)", ops)
	}
}

// TestRestoreOpsForNode_OVSBondIsRestorable is the T-3105 fix, and it is
// the same fixture that used to prove the opposite: this exact stanza —
// carrying an ovs_bridge line all along — was refused with
// ErrRestoreUnsupported, because inventory.Bond dropped the attachment on
// the way in. The snapshot was never missing the information; the model
// was. Reverting Bond.OVSBridge (or interfacesOVSBond's read of it) turns
// this test red.
func TestRestoreOpsForNode_OVSBondIsRestorable(t *testing.T) {
	target := restoreTestLo + "auto bond0\niface bond0 inet manual\n\tovs_bonds eth0 eth1\n\tovs_type OVSBond\n\tovs_bridge vmbr0\n"
	ops, err := restoreOpsForNode("pve1", target, restoreTestLo)
	if err != nil {
		t.Fatalf("restoreOpsForNode = %v, want an OVS bond create op", err)
	}
	var create *Op
	for i := range ops {
		if ops[i].Type == OpBondCreate {
			create = &ops[i]
		}
	}
	if create == nil {
		t.Fatalf("restoreOpsForNode produced no bond.create op (got %d ops)", len(ops))
	}
	if create.Target.Kind != inventory.KindOVSBond {
		t.Errorf("op target kind = %s, want %s", create.Target.Kind, inventory.KindOVSBond)
	}
	p, ok := create.Params.(*BondCreateParams)
	if !ok {
		t.Fatalf("op params = %T, want *BondCreateParams", create.Params)
	}
	// The whole point of the card: without this, the op renders an OVS bond
	// attached to nothing.
	if p.Bridge != "vmbr0" {
		t.Errorf("BondCreateParams.Bridge = %q, want %q — the ovs_bridge attachment was dropped", p.Bridge, "vmbr0")
	}
	if got := strings.Join(p.Slaves, ","); got != "eth0,eth1" {
		t.Errorf("BondCreateParams.Slaves = %q, want %q", got, "eth0,eth1")
	}
}

// TestRestoreOpsForNode_OVSBondWithoutBridgeStillRefused pins the narrowed
// half. Snapshots outlive the code that wrote them: one taken before
// Bond.OVSBridge existed carries no attachment, and restoring it would
// render a bond attached to nothing. The refusal must survive for that
// case rather than being deleted along with the general one.
func TestRestoreOpsForNode_OVSBondWithoutBridgeStillRefused(t *testing.T) {
	target := restoreTestLo + "auto bond0\niface bond0 inet manual\n\tovs_bonds eth0 eth1\n\tovs_type OVSBond\n"
	_, err := restoreOpsForNode("pve1", target, restoreTestLo)
	var unsupp *ErrRestoreUnsupported
	if !errors.As(err, &unsupp) {
		t.Fatalf("restoreOpsForNode err = %v, want *ErrRestoreUnsupported", err)
	}
	if unsupp.Kind != inventory.KindOVSBond {
		t.Errorf("refusal Kind = %s, want %s", unsupp.Kind, inventory.KindOVSBond)
	}
}

func TestRestoringTitle(t *testing.T) {
	if got := restoringTitle(Changeset{Title: "add bridge"}); got != "Rollback of add bridge" {
		t.Fatalf("restoringTitle = %q", got)
	}
	if got := restoringTitle(Changeset{ID: "01ABC"}); got != "Rollback of 01ABC" {
		t.Fatalf("restoringTitle (no title) = %q", got)
	}
}

func TestClampConfirmTimeout(t *testing.T) {
	cases := []struct{ in, want time.Duration }{
		{10 * time.Second, MinConfirmTimeout},
		{120 * time.Second, 120 * time.Second},
		{2 * time.Hour, MaxConfirmTimeout},
	}
	for _, c := range cases {
		if got := clampConfirmTimeout(c.in); got != c.want {
			t.Fatalf("clampConfirmTimeout(%s) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestApplyErrorStrings(t *testing.T) {
	errs := []error{
		&ErrChangesetLocked{HeldBy: "cs1"},
		&ErrApplyNotConfigured{},
		&ErrUnsupportedOp{OpType: OpSdnApply},
		&ErrNotConfirmable{ID: "cs1", Status: StatusDraft},
		&ErrValidationBlocked{},
		&ErrRestoreUnsupported{Node: "pve1", Kind: inventory.KindOVSBond, ID: "bond0", Reason: "test"},
		&ErrRollbackWindowExpired{ID: "cs1", CommittedAt: 100, WindowDays: 7},
	}
	for _, e := range errs {
		if e.Error() == "" {
			t.Fatalf("empty Error() for %T", e)
		}
	}
}

func TestNodeAgentReader_UnsupportedMethods(t *testing.T) {
	r := nodeAgentReader{agent: nil}
	ctx := context.Background()
	if _, err := r.Links(ctx, "pve1"); err == nil {
		t.Fatal("Links should be unsupported")
	}
	if _, err := r.LLDP(ctx, "pve1"); err == nil {
		t.Fatal("LLDP should be unsupported")
	}
	if _, err := r.Stats(ctx, "pve1"); err == nil {
		t.Fatal("Stats should be unsupported")
	}
}

// TestParamsUnion_Membership asserts every params type is a member of the
// sealed Params union (docs/data-model.md §3's op vocabulary), exercising each
// type's isChangeParams marker so a params struct that silently drops out of
// the union — or a new op whose params were never wired in — is caught here.
func TestParamsUnion_Membership(t *testing.T) {
	members := []Params{
		&IfaceUpdateParams{}, &IfaceRawReplaceParams{},
		&BondCreateParams{}, &BondUpdateParams{}, &BondDeleteParams{},
		&BridgeCreateParams{}, &BridgeUpdateParams{}, &BridgeDeleteParams{},
		&BridgePortAddParams{}, &BridgePortRemoveParams{},
		&VlanCreateParams{}, &VlanUpdateParams{}, &VlanDeleteParams{},
		&SdnZoneCreateParams{}, &SdnZoneUpdateParams{}, &SdnZoneDeleteParams{},
		&SdnVnetCreateParams{}, &SdnVnetUpdateParams{}, &SdnVnetDeleteParams{},
		&SdnSubnetCreateParams{}, &SdnSubnetUpdateParams{}, &SdnSubnetDeleteParams{},
		&SdnDnsZoneCreateParams{}, &SdnDnsZoneUpdateParams{}, &SdnDnsZoneDeleteParams{},
		&SdnDnsRecordCreateParams{}, &SdnDnsRecordUpdateParams{}, &SdnDnsRecordDeleteParams{},
		&SdnFabricCreateParams{}, &SdnFabricUpdateParams{}, &SdnFabricDeleteParams{},
		&SdnApplyParams{},
		&GuestNicUpdateParams{},
		&FwRuleCreateParams{}, &FwRuleUpdateParams{}, &FwRuleDeleteParams{}, &FwRuleMoveParams{},
		&FwOptionsUpdateParams{},
		&FwAliasCreateParams{}, &FwAliasUpdateParams{}, &FwAliasDeleteParams{},
		&FwIpsetCreateParams{}, &FwIpsetUpdateParams{}, &FwIpsetDeleteParams{},
		&FwGroupCreateParams{}, &FwGroupUpdateParams{}, &FwGroupDeleteParams{},
		&IfaceRenameParams{},
		&IpamAllocCreateParams{}, &IpamAllocDeleteParams{},
		&QosShapeCreateParams{}, &QosShapeUpdateParams{}, &QosShapeDeleteParams{},
		&WgTunnelCreateParams{}, &WgTunnelUpdateParams{}, &WgTunnelDeleteParams{},
		&WgPeerAddParams{}, &WgPeerRemoveParams{},
		&NatMasqueradeCreateParams{}, &NatMasqueradeDeleteParams{},
		&NatPortForwardCreateParams{}, &NatPortForwardUpdateParams{}, &NatPortForwardDeleteParams{},
		&RouteStaticCreateParams{}, &RouteStaticUpdateParams{}, &RouteStaticDeleteParams{},
		&VFProvisionParams{},
		&SwitchPortUpdateParams{},
	}
	for _, m := range members {
		m.isChangeParams() // marker; asserts union membership at runtime too
	}
	if len(members) != len(paramFactories) {
		t.Fatalf("Params union has %d members, but %d op factories exist — a params type is missing from this membership test", len(members), len(paramFactories))
	}
}

func TestDecodeHelpers_Empty(t *testing.T) {
	if p := decodePlan(nil); len(p.Steps) != 0 {
		t.Fatalf("decodePlan(nil) = %+v", p)
	}
	if a := decodeApplyLog(nil); len(a.Steps) != 0 {
		t.Fatalf("decodeApplyLog(nil) = %+v", a)
	}
}
