package findings_test

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// evpnZoneVnetSubnet applies one EVPN zone + its vnet + a gateway-bearing
// subnet in a single SourcePVESDN poll (Scope{} reconciles every
// cluster-scoped SDN entity at once, so zone/vnet/subnet must always be
// supplied together, not across separate ApplyPoll calls).
func evpnZoneVnetSubnet(g *inventory.Graph, zoneID string, nodes []string, vnetID, gateway string) {
	g.ApplyPoll(inventory.SourcePVESDN, inventory.Scope{}, []inventory.Entity{
		&inventory.SdnZone{Ref: inventory.Ref{Kind: inventory.KindSDNZone, ID: zoneID}, ID: zoneID, Type: "evpn", Nodes: nodes},
		&inventory.SdnVnet{Ref: inventory.Ref{Kind: inventory.KindSDNVnet, ID: zoneID + "/" + vnetID}, ID: vnetID, Zone: zoneID},
		&inventory.SdnSubnet{Ref: inventory.Ref{Kind: inventory.KindSDNSubnet, ID: gateway + "/24"}, ID: gateway + "/24", Vnet: vnetID, Gateway: gateway},
	})
}

func netlinkVnetBridge(g *inventory.Graph, node, vnetID string, addresses []string) {
	br := &inventory.Bridge{
		Ref: inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: vnetID}, Name: vnetID, Addresses: addresses,
	}
	// Scoped to KindBridge only (not a bare Scope{Node}) so this poll never
	// retires other kinds (e.g. a PhysNic) this same SourceHostNetlink
	// already contributed for node — the same "narrow the scope to what
	// you're actually supplying" rule netlinkPhysNics' doc comment documents.
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: node, Kinds: []inventory.Kind{inventory.KindBridge}}, []inventory.Entity{br})
}

// TestEvpnGwInconsistency_Fires: the anycast gateway is realized on two of
// three member nodes but missing on the third (AC1's firing case).
func TestEvpnGwInconsistency_Fires(t *testing.T) {
	g := newGraphWithNodes("pve1", "pve2", "pve3")
	evpnZoneVnetSubnet(g, "evpnz", []string{"pve1", "pve2", "pve3"}, "vnet-tenant-a", "192.168.50.1")
	netlinkVnetBridge(g, "pve1", "vnet-tenant-a", []string{"192.168.50.1/24"})
	netlinkVnetBridge(g, "pve2", "vnet-tenant-a", []string{"192.168.50.1/24"})
	netlinkVnetBridge(g, "pve3", "vnet-tenant-a", nil) // realized, but no gateway address

	eng := findings.New(findings.Config{Graph: g})
	found := findByCheck(t, eng.Findings(), findings.CheckEvpnGwInconsistency)
	if len(found) != 1 {
		t.Fatalf("got %d evpn_gw_inconsistency findings, want 1: %+v", len(found), found)
	}
	f := found[0]
	if f.Fixable {
		t.Errorf("evpn_gw_inconsistency should never be fixable, got Fixable=true")
	}
	if f.DocsLink == "" {
		t.Error("evpn_gw_inconsistency must carry a DocsLink")
	}
	if !strings.Contains(f.Detail, "192.168.50.1") || !strings.Contains(f.Detail, "pve3") {
		t.Errorf("detail = %q, want mention of the gateway ip and pve3 (the dissenting node)", f.Detail)
	}
	if len(f.Nodes) != 3 {
		t.Errorf("Nodes = %v, want all 3 member nodes named", f.Nodes)
	}
}

// TestEvpnGwInconsistency_Consistent_NoFinding: the gateway realized
// identically on every member node never fires.
func TestEvpnGwInconsistency_Consistent_NoFinding(t *testing.T) {
	g := newGraphWithNodes("pve1", "pve2", "pve3")
	evpnZoneVnetSubnet(g, "evpnz", []string{"pve1", "pve2", "pve3"}, "vnet-tenant-a", "192.168.50.1")
	for _, node := range []string{"pve1", "pve2", "pve3"} {
		netlinkVnetBridge(g, node, "vnet-tenant-a", []string{"192.168.50.1/24"})
	}

	eng := findings.New(findings.Config{Graph: g})
	if found := findByCheck(t, eng.Findings(), findings.CheckEvpnGwInconsistency); len(found) != 0 {
		t.Fatalf("consistently-realized gateway produced a finding: %+v", found)
	}
}

// TestEvpnGwInconsistency_NotRealizedAnywhere_NoFinding: the gateway missing
// on every member node is "not realized at all", a different (validate-time)
// problem this continuous check does not claim — only a genuine split fires.
func TestEvpnGwInconsistency_NotRealizedAnywhere_NoFinding(t *testing.T) {
	g := newGraphWithNodes("pve1", "pve2")
	evpnZoneVnetSubnet(g, "evpnz", []string{"pve1", "pve2"}, "vnet-tenant-a", "192.168.50.1")
	// No vnet-tenant-a bridge on either node at all.

	eng := findings.New(findings.Config{Graph: g})
	if found := findByCheck(t, eng.Findings(), findings.CheckEvpnGwInconsistency); len(found) != 0 {
		t.Fatalf("gateway realized nowhere produced a finding (not this check's job): %+v", found)
	}
}

// TestEvpnGwInconsistency_NonEvpnZone_NeverFires: a vlan/simple zone's
// subnet gateway is never evaluated by this check (the anycast-gateway
// contract is EVPN-specific).
func TestEvpnGwInconsistency_NonEvpnZone_NeverFires(t *testing.T) {
	g := newGraphWithNodes("pve1", "pve2")
	g.ApplyPoll(inventory.SourcePVESDN, inventory.Scope{}, []inventory.Entity{
		&inventory.SdnZone{Ref: inventory.Ref{Kind: inventory.KindSDNZone, ID: "vlanz"}, ID: "vlanz", Type: "vlan", Nodes: []string{"pve1", "pve2"}},
		&inventory.SdnVnet{Ref: inventory.Ref{Kind: inventory.KindSDNVnet, ID: "vlanz/vnet1"}, ID: "vnet1", Zone: "vlanz"},
		&inventory.SdnSubnet{Ref: inventory.Ref{Kind: inventory.KindSDNSubnet, ID: "10.0.0.1/24"}, ID: "10.0.0.1/24", Vnet: "vnet1", Gateway: "10.0.0.1"},
	})
	netlinkVnetBridge(g, "pve1", "vnet1", []string{"10.0.0.1/24"})
	// pve2 has no vnet1 bridge — would be inconsistent if this were EVPN.

	eng := findings.New(findings.Config{Graph: g})
	if found := findByCheck(t, eng.Findings(), findings.CheckEvpnGwInconsistency); len(found) != 0 {
		t.Fatalf("non-evpn zone produced a finding: %+v", found)
	}
}
