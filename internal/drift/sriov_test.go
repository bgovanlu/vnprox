// SPDX-License-Identifier: Apache-2.0

package drift_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/drift"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// buildVFGraph builds a one-node graph with a VLAN-aware bridge (declared
// VIDs 100-100) whose port is PF "eno1", carrying vf's declared state.
func buildVFGraph(t *testing.T, vf inventory.VirtualFunction) *inventory.Graph {
	t.Helper()
	g := inventory.NewGraph()
	pfRef := inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno1"}
	bridgeRef := inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr1"}
	vf.PF = pfRef
	if vf.Kind == "" {
		vf.Ref = inventory.Ref{Kind: inventory.KindVF, Node: "pve1", ID: "eno1/vf0"}
	}
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: "pve1"}, []inventory.Entity{
		&inventory.PhysNic{Ref: pfRef, Name: "eno1", SRIOVVFs: []inventory.VirtualFunction{vf}},
		&inventory.Bridge{
			Ref: bridgeRef, Name: "vmbr1", Virt: inventory.BridgeLinux,
			VlanAware: true, VlanAwareSet: true,
			Vids:      []inventory.VidRange{{Low: 100, High: 100}},
			PortNames: []string{"eno1"},
		},
	})
	return g
}

// TestVFSpoofcheckMismatch_Fires is T-1506 acceptance criterion 3's
// standing-drift half: an already-diverged live VF (VLAN outside the
// bridge's declared VID set) fires vf_spoofcheck_mismatch.
func TestVFSpoofcheckMismatch_Fires(t *testing.T) {
	g := buildVFGraph(t, inventory.VirtualFunction{VLAN: 999, SpoofCheck: true})
	svc := drift.New(drift.Config{Graph: g})
	found := findByCheck(t, svc.Findings(), drift.CheckVFSpoofcheckMismatch)
	if len(found) != 1 {
		t.Fatalf("got %d vf_spoofcheck_mismatch findings, want 1: %+v", len(found), found)
	}
	if len(found[0].Refs) != 1 || found[0].Refs[0] != "vf:pve1:eno1/vf0" {
		t.Errorf("finding refs = %v, want [vf:pve1:eno1/vf0]", found[0].Refs)
	}
	if found[0].Fixable {
		t.Error("vf_spoofcheck_mismatch should be detection-only (not Fixable) — fixing it is a real infrastructure action outside the v1 op vocabulary")
	}
}

// TestVFSpoofcheckMismatch_SilentWhenConsistent is the same check's
// negative direction: a VF whose VLAN/spoof-check matches its PF's
// bridge policy never fires.
func TestVFSpoofcheckMismatch_SilentWhenConsistent(t *testing.T) {
	g := buildVFGraph(t, inventory.VirtualFunction{VLAN: 100, SpoofCheck: true})
	svc := drift.New(drift.Config{Graph: g})
	if found := findByCheck(t, svc.Findings(), drift.CheckVFSpoofcheckMismatch); len(found) != 0 {
		t.Fatalf("unexpected vf_spoofcheck_mismatch findings for a consistent VF: %+v", found)
	}
}

// TestVFSpoofcheckMismatch_SilentWithNoBridge: a PF not attached to any
// bridge has no policy to diverge from — never flagged.
func TestVFSpoofcheckMismatch_SilentWithNoBridge(t *testing.T) {
	g := inventory.NewGraph()
	pfRef := inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno1"}
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: "pve1"}, []inventory.Entity{
		&inventory.PhysNic{Ref: pfRef, Name: "eno1", SRIOVVFs: []inventory.VirtualFunction{
			{Ref: inventory.Ref{Kind: inventory.KindVF, Node: "pve1", ID: "eno1/vf0"}, PF: pfRef, VLAN: 999, SpoofCheck: false},
		}},
	})
	svc := drift.New(drift.Config{Graph: g})
	if found := findByCheck(t, svc.Findings(), drift.CheckVFSpoofcheckMismatch); len(found) != 0 {
		t.Fatalf("unexpected vf_spoofcheck_mismatch findings for a PF with no bridge: %+v", found)
	}
}
