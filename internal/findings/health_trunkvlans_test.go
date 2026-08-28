// SPDX-License-Identifier: Apache-2.0

package findings_test

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// trunkBridgeWithVids applies a VLAN-aware bridge trunking vids on node.
func trunkBridgeWithVids(g *inventory.Graph, node, name string, vids []inventory.VidRange) inventory.Ref {
	ref := inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: name}
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: node, Kinds: []inventory.Kind{inventory.KindBridge}}, []inventory.Entity{
		&inventory.Bridge{Ref: ref, Name: name, VlanAware: true, VlanAwareSet: true, Vids: vids},
	})
	return ref
}

// guestNicOn applies a single GuestNic attached to bridgeRef with the given
// already-resolved EffectiveVid. Scope{Node, Kinds:[KindGuestNic]} retires
// every other GuestNic this same source previously contributed for that
// node, so a test needing more than one live guest NIC on the same node
// must batch them via guestNicsOn below, not call this once per NIC (the
// same footgun netlinkPhysNics' doc comment documents for physical NICs).
func guestNicOn(g *inventory.Graph, node, guestID, key string, bridgeRef inventory.Ref, effectiveVid int) {
	guestNicsOn(g, node, []guestNicSpec{{guestID: guestID, key: key, bridgeRef: bridgeRef, effectiveVid: effectiveVid}})
}

type guestNicSpec struct {
	guestID      string
	key          string
	bridgeRef    inventory.Ref
	effectiveVid int
}

// guestNicsOn applies every spec in specs as one single-source, single-scope
// poll. This package's checks read the already-resolved graph, but
// resolveGuestNic (internal/inventory/link.go) recomputes EffectiveVid from
// Vid on every linking pass whenever TargetName is empty (leaving
// BridgeOrVnet as directly set) — so a synthetic already-resolved GuestNic
// must set Vid (not EffectiveVid directly) to survive that pass, exactly
// the same "set the raw field the resolver derives from" requirement
// TestGuestAttachPlainBridge's own construction follows.
func guestNicsOn(g *inventory.Graph, node string, specs []guestNicSpec) {
	var entities []inventory.Entity
	for _, s := range specs {
		entities = append(entities, &inventory.GuestNic{
			Ref:          inventory.Ref{Kind: inventory.KindGuestNic, Node: node, ID: s.guestID + "/" + s.key},
			Guest:        inventory.Ref{Kind: inventory.KindGuest, Node: node, ID: s.guestID},
			Key:          s.key,
			BridgeOrVnet: s.bridgeRef,
			Vid:          s.effectiveVid,
		})
	}
	g.ApplyPoll(inventory.SourcePVEGuest, inventory.Scope{Node: node, Kinds: []inventory.Kind{inventory.KindGuestNic}}, entities)
}

// TestTrunkUnusedVlans_Fires: a trunk carries VIDs 100-102 but only VID 100
// is in guest use (AC1's firing case).
func TestTrunkUnusedVlans_Fires(t *testing.T) {
	g := newGraphWithNodes("pve1")
	br := trunkBridgeWithVids(g, "pve1", "vmbr0", []inventory.VidRange{{Low: 100, High: 102}})
	guestNicOn(g, "pve1", "200", "net0", br, 100)

	eng := findings.New(findings.Config{Graph: g})
	found := findByCheck(t, eng.Findings(), findings.CheckTrunkUnusedVlans)
	if len(found) != 1 {
		t.Fatalf("got %d trunk_unused_vlans findings, want 1: %+v", len(found), found)
	}
	f := found[0]
	if f.Severity != findings.SeverityInfo {
		t.Errorf("severity = %q, want info (informational per the task card)", f.Severity)
	}
	if f.Fixable {
		t.Errorf("trunk_unused_vlans should never be fixable (never auto-narrows), got Fixable=true")
	}
	if !strings.Contains(f.Detail, "101") || !strings.Contains(f.Detail, "102") || strings.Contains(f.Detail, ", 100") {
		t.Errorf("detail = %q, want mention of unused 101/102 but not used 100", f.Detail)
	}
}

// TestTrunkUnusedVlans_AllUsed_NoFinding: every trunked VID has a guest NIC
// using it.
func TestTrunkUnusedVlans_AllUsed_NoFinding(t *testing.T) {
	g := newGraphWithNodes("pve1")
	br := trunkBridgeWithVids(g, "pve1", "vmbr0", []inventory.VidRange{{Low: 100, High: 101}})
	guestNicsOn(g, "pve1", []guestNicSpec{
		{guestID: "200", key: "net0", bridgeRef: br, effectiveVid: 100},
		{guestID: "201", key: "net0", bridgeRef: br, effectiveVid: 101},
	})

	eng := findings.New(findings.Config{Graph: g})
	if found := findByCheck(t, eng.Findings(), findings.CheckTrunkUnusedVlans); len(found) != 0 {
		t.Fatalf("fully-used trunk produced a finding: %+v", found)
	}
}

// TestTrunkUnusedVlans_NotVlanAware_NeverFires: a non-VLAN-aware bridge is
// never evaluated, even if Vids happens to be populated.
func TestTrunkUnusedVlans_NotVlanAware_NeverFires(t *testing.T) {
	g := newGraphWithNodes("pve1")
	ref := inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr1"}
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: "pve1"}, []inventory.Entity{
		&inventory.Bridge{Ref: ref, Name: "vmbr1", VlanAware: false, Vids: []inventory.VidRange{{Low: 100, High: 101}}},
	})

	eng := findings.New(findings.Config{Graph: g})
	if found := findByCheck(t, eng.Findings(), findings.CheckTrunkUnusedVlans); len(found) != 0 {
		t.Fatalf("non-vlan-aware bridge produced a finding: %+v", found)
	}
}

// TestTrunkUnusedVlans_NoVids_NeverFires: a VLAN-aware bridge with no
// declared trunk VIDs (nothing to compare against) never fires.
func TestTrunkUnusedVlans_NoVids_NeverFires(t *testing.T) {
	g := newGraphWithNodes("pve1")
	trunkBridgeWithVids(g, "pve1", "vmbr0", nil)

	eng := findings.New(findings.Config{Graph: g})
	if found := findByCheck(t, eng.Findings(), findings.CheckTrunkUnusedVlans); len(found) != 0 {
		t.Fatalf("bridge with no declared vids produced a finding: %+v", found)
	}
}
