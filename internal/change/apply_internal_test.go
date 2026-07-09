package change

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

func bridgeRef(id string) inventory.Ref {
	return inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: id}
}

func TestInverseOp_AllInvertibleKinds(t *testing.T) {
	ops := []Op{
		{Type: OpBridgeCreate, Target: bridgeRef("vmbr1"), Params: &BridgeCreateParams{}},
		{Type: OpBondCreate, Target: inventory.Ref{Kind: inventory.KindBond, Node: "pve1", ID: "bond0"}, Params: &BondCreateParams{}},
		{Type: OpVlanCreate, Target: inventory.Ref{Kind: inventory.KindVlan, Node: "pve1", ID: "vmbr0.10"}, Params: &VlanCreateParams{}},
		{Type: OpBridgePortAdd, Target: bridgeRef("vmbr0"), Params: &BridgePortAddParams{Port: "eno2"}},
		{Type: OpBridgePortRemove, Target: bridgeRef("vmbr0"), Params: &BridgePortRemoveParams{Port: "eno3"}},
		{Type: OpSdnApply, Params: &SdnApplyParams{}},
	}
	inv, err := buildRestoringOps(ops)
	if err != nil {
		t.Fatalf("buildRestoringOps: %v", err)
	}
	// sdn.apply is skipped; the other five invert in reverse order.
	want := []OpType{
		OpBridgePortAdd,    // inverse of the port.remove (last non-skip op)
		OpBridgePortRemove, // inverse of the port.add
		OpVlanDelete,
		OpBondDelete,
		OpBridgeDelete,
	}
	if len(inv) != len(want) {
		t.Fatalf("inverse ops = %d, want %d: %+v", len(inv), len(want), inv)
	}
	for i, w := range want {
		if inv[i].Type != w {
			t.Fatalf("inverse[%d] = %s, want %s", i, inv[i].Type, w)
		}
	}
	// port swaps carry the port through
	if p, ok := inv[0].Params.(*BridgePortAddParams); !ok || p.Port != "eno3" {
		t.Fatalf("port.remove inverse lost its port: %+v", inv[0].Params)
	}
	if p, ok := inv[1].Params.(*BridgePortRemoveParams); !ok || p.Port != "eno2" {
		t.Fatalf("port.add inverse lost its port: %+v", inv[1].Params)
	}
}

func TestInverseOp_Unsupported(t *testing.T) {
	for _, ot := range []OpType{OpBridgeDelete, OpIfaceUpdate, OpBondUpdate, OpVlanDelete} {
		_, err := buildRestoringOps([]Op{{Type: ot, Target: bridgeRef("x"), Params: &BridgeDeleteParams{}}})
		var unsupp *ErrInverseUnsupported
		if !errors.As(err, &unsupp) {
			t.Fatalf("op %s: err = %v, want *ErrInverseUnsupported", ot, err)
		}
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
		&ErrInverseUnsupported{OpType: OpBridgeDelete},
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
		&IfaceUpdateParams{},
		&BondCreateParams{}, &BondUpdateParams{}, &BondDeleteParams{},
		&BridgeCreateParams{}, &BridgeUpdateParams{}, &BridgeDeleteParams{},
		&BridgePortAddParams{}, &BridgePortRemoveParams{},
		&VlanCreateParams{}, &VlanUpdateParams{}, &VlanDeleteParams{},
		&SdnZoneCreateParams{}, &SdnZoneUpdateParams{}, &SdnZoneDeleteParams{},
		&SdnVnetCreateParams{}, &SdnVnetUpdateParams{}, &SdnVnetDeleteParams{},
		&SdnSubnetCreateParams{}, &SdnSubnetUpdateParams{}, &SdnSubnetDeleteParams{},
		&SdnApplyParams{},
		&GuestNicUpdateParams{},
		&FwRuleCreateParams{}, &FwRuleUpdateParams{}, &FwRuleDeleteParams{}, &FwRuleMoveParams{},
		&FwOptionsUpdateParams{},
		&FwAliasCreateParams{}, &FwAliasUpdateParams{}, &FwAliasDeleteParams{},
		&FwIpsetCreateParams{}, &FwIpsetUpdateParams{}, &FwIpsetDeleteParams{},
		&FwGroupCreateParams{}, &FwGroupUpdateParams{}, &FwGroupDeleteParams{},
		&IpamAllocCreateParams{}, &IpamAllocDeleteParams{},
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
