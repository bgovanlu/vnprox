// SPDX-License-Identifier: Apache-2.0

package runbook_test

import (
	"errors"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/fwlog"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/runbook"
)

func mustRunbook(t *testing.T, name string) runbook.Runbook {
	t.Helper()
	rb, ok := runbook.ByName(name)
	if !ok {
		t.Fatalf("runbook.ByName(%q): not found", name)
	}
	return rb
}

// --- delete-orphan-vnet -----------------------------------------------

func TestRender_DeleteOrphanVnet_ProposesDeleteOp(t *testing.T) {
	g := newGraph()
	vnetRef := addSdnVnet(g, "vnetx", "goneZone") // no matching zone polled at all
	f := findings.Finding{Check: "orphan_vnet", ID: "health:orphan_vnet|" + vnetRef.String(), Refs: []string{vnetRef.String()}}

	ops, title, err := runbook.Render(mustRunbook(t, runbook.DeleteOrphanVnet), f, runbook.ReadContext{Snapshot: g.Snapshot()})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("ops = %+v, want exactly 1", ops)
	}
	if ops[0].Type != change.OpSdnVnetDelete {
		t.Errorf("op type = %s, want %s", ops[0].Type, change.OpSdnVnetDelete)
	}
	if ops[0].Target != vnetRef {
		t.Errorf("op target = %s, want %s", ops[0].Target, vnetRef)
	}
	if title == "" {
		t.Error("title is empty")
	}
}

func TestRender_DeleteOrphanVnet_ZoneRecreated_NothingToDo(t *testing.T) {
	g := newGraph()
	vnetRef := addSdnVnet(g, "vnetx", "zone1")
	addSdnZone(g, "zone1") // the zone now exists again -- no longer orphaned
	f := findings.Finding{Check: "orphan_vnet", ID: "health:orphan_vnet|" + vnetRef.String(), Refs: []string{vnetRef.String()}}

	_, _, err := runbook.Render(mustRunbook(t, runbook.DeleteOrphanVnet), f, runbook.ReadContext{Snapshot: g.Snapshot()})
	if !errors.Is(err, runbook.ErrNothingToDo) {
		t.Fatalf("err = %v, want ErrNothingToDo", err)
	}
}

func TestRender_DeleteOrphanVnet_VnetAlreadyGone_NothingToDo(t *testing.T) {
	g := newGraph()
	vnetRef := inventory.Ref{Kind: inventory.KindSDNVnet, ID: "goneVnet"} // never polled
	f := findings.Finding{Check: "orphan_vnet", ID: "health:orphan_vnet|" + vnetRef.String(), Refs: []string{vnetRef.String()}}

	_, _, err := runbook.Render(mustRunbook(t, runbook.DeleteOrphanVnet), f, runbook.ReadContext{Snapshot: g.Snapshot()})
	if !errors.Is(err, runbook.ErrNothingToDo) {
		t.Fatalf("err = %v, want ErrNothingToDo", err)
	}
}

// --- trim-unused-trunk-vids ---------------------------------------------

func TestRender_TrimUnusedTrunkVids_NarrowsToUsedVids(t *testing.T) {
	g := newGraph()
	brRef := addBridge(g, "pve1", "vmbr9", []inventory.VidRange{{Low: 10, High: 15}})
	addGuestsWithNics(g, "pve1", []nicSpec{{typ: "qemu", vmid: 100, bridgeName: "vmbr9", vid: 10}, {typ: "qemu", vmid: 101, bridgeName: "vmbr9", vid: 11}})
	// 12,13,14,15 remain unused.
	f := findings.Finding{Check: "trunk_unused_vlans", ID: "health:trunk_unused_vlans|" + brRef.String(), Refs: []string{brRef.String()}}

	ops, _, err := runbook.Render(mustRunbook(t, runbook.TrimUnusedTrunkVids), f, runbook.ReadContext{Snapshot: g.Snapshot()})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(ops) != 1 || ops[0].Type != change.OpBridgeUpdate {
		t.Fatalf("ops = %+v, want exactly one bridge.update", ops)
	}
	params, ok := ops[0].Params.(change.BridgeUpdateParams)
	if !ok || params.Vids == nil {
		t.Fatalf("params = %+v, want BridgeUpdateParams with Vids set", ops[0].Params)
	}
	want := []change.VidRange{{Low: 10, High: 11}}
	if len(*params.Vids) != len(want) || (*params.Vids)[0] != want[0] {
		t.Errorf("Vids = %+v, want %+v", *params.Vids, want)
	}
}

func TestRender_TrimUnusedTrunkVids_AlreadyFullyUsed_NothingToDo(t *testing.T) {
	g := newGraph()
	brRef := addBridge(g, "pve1", "vmbr9", []inventory.VidRange{{Low: 10, High: 11}})
	addGuestsWithNics(g, "pve1", []nicSpec{{typ: "qemu", vmid: 100, bridgeName: "vmbr9", vid: 10}, {typ: "qemu", vmid: 101, bridgeName: "vmbr9", vid: 11}})
	f := findings.Finding{Check: "trunk_unused_vlans", ID: "health:trunk_unused_vlans|" + brRef.String(), Refs: []string{brRef.String()}}

	_, _, err := runbook.Render(mustRunbook(t, runbook.TrimUnusedTrunkVids), f, runbook.ReadContext{Snapshot: g.Snapshot()})
	if !errors.Is(err, runbook.ErrNothingToDo) {
		t.Fatalf("err = %v, want ErrNothingToDo", err)
	}
}

func TestRender_TrimUnusedTrunkVids_NoneUsed_Refuses(t *testing.T) {
	g := newGraph()
	brRef := addBridge(g, "pve1", "vmbr9", []inventory.VidRange{{Low: 10, High: 11}})
	// no guest NICs at all -- narrowing to zero VIDs would change meaning,
	// not merely prune, so this must be refused (a distinct error, not
	// ErrNothingToDo, since there genuinely IS something to remediate).
	f := findings.Finding{Check: "trunk_unused_vlans", ID: "health:trunk_unused_vlans|" + brRef.String(), Refs: []string{brRef.String()}}

	_, _, err := runbook.Render(mustRunbook(t, runbook.TrimUnusedTrunkVids), f, runbook.ReadContext{Snapshot: g.Snapshot()})
	if err == nil {
		t.Fatal("Render: want an error, got nil")
	}
	if errors.Is(err, runbook.ErrNothingToDo) {
		t.Errorf("err = %v, want a refusal distinct from ErrNothingToDo", err)
	}
}

// --- delete-unused-fw-rule -----------------------------------------------

func guestFwRule() (guestRef inventory.Ref, findingID string) {
	guestRef = inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "100"}
	findingID = "health:fw_rule_unused|" + guestRef.String() + "|guest|0"
	return
}

func TestRender_DeleteUnusedFwRule_ProposesDeleteOp(t *testing.T) {
	g := newGraph()
	guestRef, findingID := guestFwRule()
	g.ApplyPoll(inventory.SourcePVEGuest, inventory.Scope{Node: "pve1", Kinds: []inventory.Kind{inventory.KindGuest}},
		[]inventory.Entity{&inventory.Guest{Ref: guestRef, Name: "g100", Type: "qemu", Node: "pve1", Status: "running", VMID: 100}})
	rulesetRef := addGuestFwRuleset(g, "pve1", "qemu", 100, []inventory.FwRule{{Pos: 0, Enabled: true, Direction: "in", Action: "ACCEPT"}})

	f := findings.Finding{Check: "fw_rule_unused", ID: findingID, Refs: []string{guestRef.String()}}
	rc := runbook.ReadContext{
		Snapshot: g.Snapshot(),
		FwAnalytics: &fwlog.Analytics{UnusedRules: []fwlog.UnusedRule{
			{Rule: fwlog.RuleRef{GuestRef: guestRef.String(), Origin: "guest", Pos: 0}},
		}},
	}

	ops, title, err := runbook.Render(mustRunbook(t, runbook.DeleteUnusedFwRule), f, rc)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(ops) != 1 || ops[0].Type != change.OpFwRuleDelete {
		t.Fatalf("ops = %+v, want exactly one fw.rule.delete", ops)
	}
	if ops[0].Target != rulesetRef {
		t.Errorf("op target = %s, want %s", ops[0].Target, rulesetRef)
	}
	params, ok := ops[0].Params.(change.FwRuleDeleteParams)
	if !ok || params.Pos != 0 {
		t.Errorf("params = %+v, want FwRuleDeleteParams{Pos: 0}", ops[0].Params)
	}
	if title == "" {
		t.Error("title is empty")
	}
}

func TestRender_DeleteUnusedFwRule_RuleNowHasHits_NothingToDo(t *testing.T) {
	g := newGraph()
	guestRef, findingID := guestFwRule()
	g.ApplyPoll(inventory.SourcePVEGuest, inventory.Scope{Node: "pve1", Kinds: []inventory.Kind{inventory.KindGuest}},
		[]inventory.Entity{&inventory.Guest{Ref: guestRef, Name: "g100", Type: "qemu", Node: "pve1", Status: "running", VMID: 100}})
	addGuestFwRuleset(g, "pve1", "qemu", 100, []inventory.FwRule{{Pos: 0, Enabled: true, Direction: "in", Action: "ACCEPT"}})

	f := findings.Finding{Check: "fw_rule_unused", ID: findingID, Refs: []string{guestRef.String()}}
	// Fresh analytics no longer lists this rule as unused -- it recorded a
	// hit since the finding fired.
	rc := runbook.ReadContext{Snapshot: g.Snapshot(), FwAnalytics: &fwlog.Analytics{}}

	_, _, err := runbook.Render(mustRunbook(t, runbook.DeleteUnusedFwRule), f, rc)
	if !errors.Is(err, runbook.ErrNothingToDo) {
		t.Fatalf("err = %v, want ErrNothingToDo", err)
	}
}

func TestRender_DeleteUnusedFwRule_ClusterOrigin_Unsupported(t *testing.T) {
	f := findings.Finding{
		Check: "fw_rule_unused",
		ID:    "health:fw_rule_unused|guest:pve1:100|cluster|2",
		Refs:  []string{"guest:pve1:100"},
	}
	_, _, err := runbook.Render(mustRunbook(t, runbook.DeleteUnusedFwRule), f, runbook.ReadContext{Snapshot: newGraph().Snapshot()})
	if !errors.Is(err, runbook.ErrUnsupportedRuleOrigin) {
		t.Fatalf("err = %v, want ErrUnsupportedRuleOrigin", err)
	}
}

func TestRender_DeleteUnusedFwRule_MalformedID(t *testing.T) {
	f := findings.Finding{Check: "fw_rule_unused", ID: "health:fw_rule_unused|not-enough-parts"}
	_, _, err := runbook.Render(mustRunbook(t, runbook.DeleteUnusedFwRule), f, runbook.ReadContext{})
	if !errors.Is(err, runbook.ErrMalformedFindingID) {
		t.Fatalf("err = %v, want ErrMalformedFindingID", err)
	}
}

// --- attachment / dispatch -----------------------------------------------

func TestRender_CheckMismatch(t *testing.T) {
	f := findings.Finding{Check: "some_other_check", ID: "x"}
	_, _, err := runbook.Render(mustRunbook(t, runbook.DeleteOrphanVnet), f, runbook.ReadContext{})
	if !errors.Is(err, runbook.ErrNotAttached) {
		t.Fatalf("err = %v, want ErrNotAttached", err)
	}
}

func TestRender_UnimplementedTemplate(t *testing.T) {
	rb := runbook.Runbook{Name: "bogus", CheckName: "orphan_vnet", Template: "not-a-real-template"}
	f := findings.Finding{Check: "orphan_vnet", ID: "x"}
	_, _, err := runbook.Render(rb, f, runbook.ReadContext{})
	if !errors.Is(err, runbook.ErrUnimplementedTemplate) {
		t.Fatalf("err = %v, want ErrUnimplementedTemplate", err)
	}
}
