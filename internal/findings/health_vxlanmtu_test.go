package findings_test

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// sdnZone applies a single SdnZone into g (cluster-scoped, Scope{}).
func sdnZone(g *inventory.Graph, z *inventory.SdnZone) {
	g.ApplyPoll(inventory.SourcePVESDN, inventory.Scope{}, []inventory.Entity{z})
}

// netlinkPhysNicMTU applies one node's physical NIC with a runtime MTU (the
// substrate checkVxlanUnderlayMTU's observedUnderlayMTU reads).
func netlinkPhysNicMTU(g *inventory.Graph, node, name string, mtu int) {
	n := &inventory.PhysNic{
		Ref:  inventory.Ref{Kind: inventory.KindPhysNic, Node: node, ID: name},
		Name: name, MTU: mtu, LinkUp: true, LinkUpSet: true,
	}
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: node, Kinds: []inventory.Kind{inventory.KindPhysNic}}, []inventory.Entity{n})
}

// TestVxlanUnderlayMTU_Fires: a vxlan zone's configured mtu leaves no
// headroom over the node's observed (degraded) underlay NIC MTU (AC1's
// firing case).
func TestVxlanUnderlayMTU_Fires(t *testing.T) {
	g := newGraphWithNodes("pve1")
	sdnZone(g, &inventory.SdnZone{
		Ref: inventory.Ref{Kind: inventory.KindSDNZone, ID: "vxlanz"},
		ID:  "vxlanz", Type: "vxlan", MTU: 1450, Nodes: []string{"pve1"},
	})
	netlinkPhysNicMTU(g, "pve1", "eno1", 1400) // 1450+50=1500 > 1400 observed

	eng := findings.New(findings.Config{Graph: g})
	eng.Findings()
	found := findByCheck(t, eng.Findings(), findings.CheckVxlanUnderlayMTU)
	if len(found) != 1 {
		t.Fatalf("got %d vxlan_underlay_mtu findings after 2 cycles, want 1: %+v", len(found), found)
	}
	f := found[0]
	if f.Fixable {
		t.Errorf("vxlan_underlay_mtu should never be fixable, got Fixable=true")
	}
	if f.DocsLink == "" {
		t.Error("vxlan_underlay_mtu must carry a DocsLink")
	}
	if !strings.Contains(f.Detail, "vxlanz") || !strings.Contains(f.Detail, "pve1") || !strings.Contains(f.Detail, "1400") {
		t.Errorf("detail = %q, want mention of vxlanz/pve1/1400", f.Detail)
	}
}

// TestVxlanUnderlayMTU_Healthy_NoFinding: the evpn-lab-style healthy case
// (zone mtu + overhead == observed underlay) never fires.
func TestVxlanUnderlayMTU_Healthy_NoFinding(t *testing.T) {
	g := newGraphWithNodes("pve1")
	sdnZone(g, &inventory.SdnZone{
		Ref: inventory.Ref{Kind: inventory.KindSDNZone, ID: "evpnz"},
		ID:  "evpnz", Type: "evpn", MTU: 9166, Nodes: []string{"pve1"},
	})
	netlinkPhysNicMTU(g, "pve1", "eno1", 9216) // 9166+50=9216, not > 9216

	eng := findings.New(findings.Config{Graph: g})
	for i := 0; i < 5; i++ {
		if found := findByCheck(t, eng.Findings(), findings.CheckVxlanUnderlayMTU); len(found) != 0 {
			t.Fatalf("cycle %d: healthy vxlan underlay produced a finding: %+v", i, found)
		}
	}
}

// TestVxlanUnderlayMTU_ZeroMTU_NeverFires: an unset zone MTU (PVE's own
// default applies) is never flagged, mirroring checkVxlanMTU's own skip.
func TestVxlanUnderlayMTU_ZeroMTU_NeverFires(t *testing.T) {
	g := newGraphWithNodes("pve1")
	sdnZone(g, &inventory.SdnZone{
		Ref: inventory.Ref{Kind: inventory.KindSDNZone, ID: "vxlanz"},
		ID:  "vxlanz", Type: "vxlan", Nodes: []string{"pve1"},
	})
	netlinkPhysNicMTU(g, "pve1", "eno1", 1400)

	eng := findings.New(findings.Config{Graph: g})
	for i := 0; i < 3; i++ {
		if found := findByCheck(t, eng.Findings(), findings.CheckVxlanUnderlayMTU); len(found) != 0 {
			t.Fatalf("cycle %d: mtu=0 zone produced a finding: %+v", i, found)
		}
	}
}

// TestVxlanUnderlayMTU_NonVxlanZone_NeverFires: a plain vlan/simple zone
// (this check's math doesn't apply) never fires even with a small MTU.
func TestVxlanUnderlayMTU_NonVxlanZone_NeverFires(t *testing.T) {
	g := newGraphWithNodes("pve1")
	sdnZone(g, &inventory.SdnZone{
		Ref: inventory.Ref{Kind: inventory.KindSDNZone, ID: "vlanz"},
		ID:  "vlanz", Type: "vlan", MTU: 9000, Nodes: []string{"pve1"},
	})
	netlinkPhysNicMTU(g, "pve1", "eno1", 1400)

	eng := findings.New(findings.Config{Graph: g})
	for i := 0; i < 3; i++ {
		if found := findByCheck(t, eng.Findings(), findings.CheckVxlanUnderlayMTU); len(found) != 0 {
			t.Fatalf("cycle %d: non-vxlan zone produced a finding: %+v", i, found)
		}
	}
}
