package findings_test

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// dualstackVnet applies one VNet + its v4/v6 subnet pair (both realized on
// a simple, non-EVPN zone with no exit nodes — reachability then depends
// solely on each subnet's own SNAT flag, so the two families can be set up
// to disagree without any guest/firewall config at all).
func dualstackVnet(g *inventory.Graph, zoneID, vnetID string, v4SNAT, v6SNAT bool) {
	g.ApplyPoll(inventory.SourcePVESDN, inventory.Scope{}, []inventory.Entity{
		&inventory.SdnZone{Ref: inventory.Ref{Kind: inventory.KindSDNZone, ID: zoneID}, ID: zoneID, Type: "simple"},
		&inventory.SdnVnet{Ref: inventory.Ref{Kind: inventory.KindSDNVnet, ID: zoneID + "/" + vnetID}, ID: vnetID, Zone: zoneID},
		&inventory.SdnSubnet{
			Ref: inventory.Ref{Kind: inventory.KindSDNSubnet, ID: "10.70.0.0/24"},
			ID:  "10.70.0.0/24", Vnet: vnetID, Gateway: "10.70.0.1", SNAT: v4SNAT,
		},
		&inventory.SdnSubnet{
			Ref: inventory.Ref{Kind: inventory.KindSDNSubnet, ID: "2001:db8:70::/64"},
			ID:  "2001:db8:70::/64", Vnet: vnetID, Gateway: "2001:db8:70::1", SNAT: v6SNAT,
		},
	})
}

// TestDualstackDrift_Fires: v4 SNAT'd (reaches external), v6 not (no SNAT,
// no exit node) — the classic silent dual-stack failure.
func TestDualstackDrift_Fires(t *testing.T) {
	g := newGraphWithNodes("pve1")
	dualstackVnet(g, "dsz", "vnet21", true, false)

	eng := findings.New(findings.Config{Graph: g})
	found := findByCheck(t, eng.Findings(), findings.CheckDualstackDrift)
	if len(found) != 1 {
		t.Fatalf("got %d dualstack_drift findings, want 1: %+v", len(found), found)
	}
	f := found[0]
	if !strings.Contains(f.Detail, "vnet21") {
		t.Errorf("detail = %q, want mention of vnet21", f.Detail)
	}
	if !strings.Contains(f.Detail, "allow") {
		t.Errorf("detail = %q, want the v4 allow verdict named", f.Detail)
	}
	if f.Fixable {
		t.Error("dualstack_drift should never be fixable")
	}
}

// TestDualstackDrift_HealthyDualStack_NoFinding: both families SNAT'd —
// both reach external, no finding.
func TestDualstackDrift_HealthyDualStack_NoFinding(t *testing.T) {
	g := newGraphWithNodes("pve1")
	dualstackVnet(g, "dsz", "vnet20", true, true)

	eng := findings.New(findings.Config{Graph: g})
	if found := findByCheck(t, eng.Findings(), findings.CheckDualstackDrift); len(found) != 0 {
		t.Fatalf("healthy dual-stack VNet produced a finding: %+v", found)
	}
}

// TestDualstackDrift_V4OnlyVnet_NoFinding: a VNet with only a v4 subnet
// (not dual-stack at all) is never evaluated.
func TestDualstackDrift_V4OnlyVnet_NoFinding(t *testing.T) {
	g := newGraphWithNodes("pve1")
	g.ApplyPoll(inventory.SourcePVESDN, inventory.Scope{}, []inventory.Entity{
		&inventory.SdnZone{Ref: inventory.Ref{Kind: inventory.KindSDNZone, ID: "z1"}, ID: "z1", Type: "simple"},
		&inventory.SdnVnet{Ref: inventory.Ref{Kind: inventory.KindSDNVnet, ID: "z1/vnet1"}, ID: "vnet1", Zone: "z1"},
		&inventory.SdnSubnet{
			Ref: inventory.Ref{Kind: inventory.KindSDNSubnet, ID: "10.80.0.0/24"},
			ID:  "10.80.0.0/24", Vnet: "vnet1", Gateway: "10.80.0.1", SNAT: true,
		},
	})
	eng := findings.New(findings.Config{Graph: g})
	if found := findByCheck(t, eng.Findings(), findings.CheckDualstackDrift); len(found) != 0 {
		t.Fatalf("v4-only VNet produced a finding: %+v", found)
	}
}
