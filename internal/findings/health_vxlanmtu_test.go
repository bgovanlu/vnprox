// SPDX-License-Identifier: Apache-2.0

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

// fakeMTUProvider is a T-1306 findings.MTUProvider double: a fixed
// per-node measured-MTU table, ok=false for any node not present (the
// "prober hasn't reached this path yet" case).
type fakeMTUProvider struct {
	byNode map[string]int
}

func (f *fakeMTUProvider) MeasuredUnderlayMTU(node string) (int, bool) {
	if f == nil {
		return 0, false
	}
	m, ok := f.byNode[node]
	return m, ok
}

// TestVxlanUnderlayMTU_MeasuredUpgrade is AC3: the same underlay scenario
// evaluated with and without a fresh measured-MTU input produces the
// tighter (measured-based) verdict only when the reading exists — a table
// test covering both branches, extending this file's existing T-803
// coverage. The scenario: an evpn zone (mtu=9166) whose *observed* NIC MTU
// alone reads healthy (9216, matching TestVxlanUnderlayMTU_Healthy_NoFinding
// exactly) but whose *measured*, DF-probed end-to-end path MTU is actually
// tighter (9200) — a real-world case observedUnderlayMTU's local NIC read
// cannot catch (some hop along the path clamps it, the NIC itself never
// sees that).
func TestVxlanUnderlayMTU_MeasuredUpgrade(t *testing.T) {
	newFixture := func() *inventory.Graph {
		g := newGraphWithNodes("pve1")
		sdnZone(g, &inventory.SdnZone{
			Ref: inventory.Ref{Kind: inventory.KindSDNZone, ID: "evpnz"},
			ID:  "evpnz", Type: "evpn", MTU: 9166, Nodes: []string{"pve1"},
		})
		netlinkPhysNicMTU(g, "pve1", "eno1", 9216) // observed-only: healthy (9166+50=9216, not >)
		return g
	}

	tests := []struct {
		mtuProv   findings.MTUProvider
		name      string
		wantFires bool
	}{
		{
			name:      "no measured input falls back to observed (config-only branch): healthy, no finding",
			mtuProv:   nil,
			wantFires: false,
		},
		{
			name:      "provider present but no fresh reading for this node: still falls back to observed, no finding",
			mtuProv:   &fakeMTUProvider{byNode: map[string]int{"pve2": 9000}},
			wantFires: false,
		},
		{
			name:      "fresh measured reading tighter than observed: fires (the upgraded branch)",
			mtuProv:   &fakeMTUProvider{byNode: map[string]int{"pve1": 9200}}, // 9166+50=9216 > 9200
			wantFires: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := newFixture()
			eng := findings.New(findings.Config{Graph: g, MTU: tc.mtuProv})

			var found []findings.Finding
			for i := 0; i < 3; i++ {
				found = findByCheck(t, eng.Findings(), findings.CheckVxlanUnderlayMTU)
			}
			if tc.wantFires && len(found) != 1 {
				t.Fatalf("got %d findings, want exactly 1 (measured-tightened breach): %+v", len(found), found)
			}
			if !tc.wantFires && len(found) != 0 {
				t.Fatalf("got %d findings, want 0: %+v", len(found), found)
			}
			if tc.wantFires && !strings.Contains(found[0].Detail, "measured") {
				t.Errorf("detail = %q, want it to name the measured source distinctly from observed", found[0].Detail)
			}
		})
	}
}
