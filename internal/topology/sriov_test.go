package topology_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// TestResolveVFAssignments is T-1506 acceptance criterion 2: a fixture
// guest with hostpci passthrough config correlates to the correct VF, and a
// VF with no matching guest (or on the wrong node) stays unassigned — never
// a wrong guess.
func TestResolveVFAssignments(t *testing.T) {
	g := inventory.NewGraph()
	pfRef := inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno1"}
	assignedVF := inventory.VirtualFunction{
		Ref:     inventory.Ref{Kind: inventory.KindVF, Node: "pve1", ID: "eno1/vf0"},
		PF:      pfRef,
		MacAddr: "aa:bb:cc:dd:ee:00", PCIAddr: "0000:01:00.1",
	}
	unassignedVF := inventory.VirtualFunction{
		Ref:     inventory.Ref{Kind: inventory.KindVF, Node: "pve1", ID: "eno1/vf1"},
		PF:      pfRef,
		MacAddr: "aa:bb:cc:dd:ee:01", PCIAddr: "0000:01:00.2",
	}
	noAddrVF := inventory.VirtualFunction{
		Ref: inventory.Ref{Kind: inventory.KindVF, Node: "pve1", ID: "eno1/vf2"},
		PF:  pfRef,
	}
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: "pve1"}, []inventory.Entity{
		&inventory.PhysNic{Ref: pfRef, Name: "eno1", SRIOVVFs: []inventory.VirtualFunction{assignedVF, unassignedVF, noAddrVF}},
	})
	g.ApplyPoll(inventory.SourcePVEGuest, inventory.Scope{}, []inventory.Entity{
		&inventory.Guest{
			Ref: inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "200"}, VMID: 200,
			Name: "gpu-vm", Type: "qemu", Node: "pve1", Status: "running",
			HostPCI: map[string]string{"hostpci0": "0000:01:00.1,pcie=1"},
		},
		// A guest on a *different* node whose hostpci happens to name the
		// same short-form PCI address must not cross-correlate — hostpci
		// passthrough is always node-local.
		&inventory.Guest{
			Ref: inventory.Ref{Kind: inventory.KindGuest, Node: "pve2", ID: "201"}, VMID: 201,
			Name: "other", Type: "qemu", Node: "pve2", Status: "running",
			HostPCI: map[string]string{"hostpci0": "01:00.2"},
		},
	})
	snap := g.Snapshot()

	got := topology.ResolveVFAssignments(snap, pfRef)
	if len(got) != 3 {
		t.Fatalf("ResolveVFAssignments returned %d VFs, want 3", len(got))
	}
	byID := map[string]inventory.VirtualFunction{}
	for _, vf := range got {
		byID[vf.ID] = vf
	}

	wantGuest := inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "200"}
	if g := byID["eno1/vf0"].AssignedGuest; g != wantGuest {
		t.Errorf("vf0 AssignedGuest = %+v, want %+v", g, wantGuest)
	}
	if g := byID["eno1/vf1"].AssignedGuest; !g.IsZero() {
		t.Errorf("vf1 (unmatched PCI addr) AssignedGuest = %+v, want zero Ref (never guessed)", g)
	}
	if g := byID["eno1/vf2"].AssignedGuest; !g.IsZero() {
		t.Errorf("vf2 (no PCI addr) AssignedGuest = %+v, want zero Ref", g)
	}
}

// TestResolveVFAssignments_UnknownOrEmptyPF covers the "no VFs to resolve"
// edge cases: a ref that isn't a PhysNic at all, and a PhysNic with none.
func TestResolveVFAssignments_UnknownOrEmptyPF(t *testing.T) {
	g := inventory.NewGraph()
	pfRef := inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno1"}
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: "pve1"}, []inventory.Entity{
		&inventory.PhysNic{Ref: pfRef, Name: "eno1"},
	})
	snap := g.Snapshot()

	if got := topology.ResolveVFAssignments(snap, pfRef); got != nil {
		t.Errorf("ResolveVFAssignments on a VF-less PF = %v, want nil", got)
	}
	unknown := inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno99"}
	if got := topology.ResolveVFAssignments(snap, unknown); got != nil {
		t.Errorf("ResolveVFAssignments on an unknown ref = %v, want nil", got)
	}
}

// TestVFPolicyMismatch is T-1506 acceptance criterion 3's pure-function
// half: a VF's VLAN/spoof-check policy compared against its PF's bridge.
func TestVFPolicyMismatch(t *testing.T) {
	vlanAwareBridge := &inventory.Bridge{
		VlanAware: true, Vids: []inventory.VidRange{{Low: 100, High: 200}},
	}
	accessBridge := &inventory.Bridge{VlanAware: false}

	tests := []struct {
		bridge *inventory.Bridge
		name   string
		vf     inventory.VirtualFunction
		want   bool
	}{
		{
			name: "no bridge at all: nothing to compare",
			vf:   inventory.VirtualFunction{VLAN: 100, SpoofCheck: false},
			want: false,
		},
		{
			name:   "vlan-aware bridge, spoofcheck on, vlan in range: consistent",
			vf:     inventory.VirtualFunction{VLAN: 150, SpoofCheck: true},
			bridge: vlanAwareBridge,
			want:   false,
		},
		{
			name:   "vlan-aware bridge, spoofcheck off: mismatch",
			vf:     inventory.VirtualFunction{VLAN: 150, SpoofCheck: false},
			bridge: vlanAwareBridge,
			want:   true,
		},
		{
			name:   "vlan-aware bridge, vlan outside declared VID set: mismatch",
			vf:     inventory.VirtualFunction{VLAN: 999, SpoofCheck: true},
			bridge: vlanAwareBridge,
			want:   true,
		},
		{
			name:   "vlan-aware bridge, untagged VF, spoofcheck on: consistent",
			vf:     inventory.VirtualFunction{VLAN: 0, SpoofCheck: true},
			bridge: vlanAwareBridge,
			want:   false,
		},
		{
			name:   "access-mode bridge, untagged VF: consistent",
			vf:     inventory.VirtualFunction{VLAN: 0, SpoofCheck: false},
			bridge: accessBridge,
			want:   false,
		},
		{
			name:   "access-mode bridge, tagged VF: mismatch",
			vf:     inventory.VirtualFunction{VLAN: 50, SpoofCheck: true},
			bridge: accessBridge,
			want:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := topology.VFPolicyMismatch(tt.vf, tt.bridge); got != tt.want {
				t.Errorf("VFPolicyMismatch = %v, want %v", got, tt.want)
			}
		})
	}
}
