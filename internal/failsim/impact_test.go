package failsim

import (
	"sort"
	"testing"

	"github.com/bgovanlu/vnprox/internal/ceph"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

func refStrs(rs []inventory.Ref) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.String()
	}
	sort.Strings(out)
	return out
}

func eqStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func hasStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// TestSimulate_ThreeNodeVLAN_Golden covers AC1 for the three-node-vlan fixture:
// node-removal, bond-removal and uplink-removal scenarios, asserting the exact
// disconnectedGuests / strandedVlans sets.
func TestSimulate_ThreeNodeVLAN_Golden(t *testing.T) {
	snap, cor := threeNodeVLAN()
	in := Input{Snapshot: snap, Corosync: cor}

	cases := []struct {
		name     string
		target   inventory.Ref
		wantDisc []string
		wantVlan []string
	}{
		{
			name:     "node-removal disconnects only that node's guest",
			target:   nodeRef("pve1"),
			wantDisc: []string{"guest:pve1:101"},
			wantVlan: nil,
		},
		{
			name:     "bond-removal disconnects the guest behind that bond",
			target:   inventory.Ref{Kind: inventory.KindBond, Node: "pve2", ID: "bond0"},
			wantDisc: []string{"guest:pve2:102"},
			wantVlan: nil,
		},
		{
			name:     "uplink-removal on a redundant bond disconnects nothing",
			target:   inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve3", ID: "eno1"},
			wantDisc: nil,
			wantVlan: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			im := Simulate(in, tc.target)
			if got := refStrs(im.DisconnectedGuests); !eqStrs(got, tc.wantDisc) {
				t.Errorf("DisconnectedGuests = %v, want %v", got, tc.wantDisc)
			}
			if got := refStrs(im.StrandedVlans); !eqStrs(got, tc.wantVlan) {
				t.Errorf("StrandedVlans = %v, want %v", got, tc.wantVlan)
			}
		})
	}
}

// TestSimulate_EvpnLab_Golden covers AC1 for the evpn-lab fixture: the same
// three removal scenarios against an EVPN vnet whose underlay rides vmbr0.
func TestSimulate_EvpnLab_Golden(t *testing.T) {
	snap, cor := evpnLab()
	in := Input{Snapshot: snap, Corosync: cor}

	cases := []struct {
		name     string
		target   inventory.Ref
		wantDisc []string
		wantVlan []string
	}{
		{
			name:     "node-removal cuts that node's vnet guest; vnet survives elsewhere",
			target:   nodeRef("pve1"),
			wantDisc: []string{"guest:pve1:201"},
			wantVlan: nil,
		},
		{
			name:     "bond-removal cuts the vnet guest on that node",
			target:   inventory.Ref{Kind: inventory.KindBond, Node: "pve2", ID: "bond0"},
			wantDisc: []string{"guest:pve2:202"},
			wantVlan: nil,
		},
		{
			name:     "uplink-removal on a redundant bond disconnects nothing",
			target:   inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve3", ID: "eno1"},
			wantDisc: nil,
			wantVlan: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			im := Simulate(in, tc.target)
			if got := refStrs(im.DisconnectedGuests); !eqStrs(got, tc.wantDisc) {
				t.Errorf("DisconnectedGuests = %v, want %v", got, tc.wantDisc)
			}
			if got := refStrs(im.StrandedVlans); !eqStrs(got, tc.wantVlan) {
				t.Errorf("StrandedVlans = %v, want %v", got, tc.wantVlan)
			}
		})
	}
}

// TestSimulate_MgmtPathLoss covers AC3: a node whose only management carrier
// (vmbr0, via bond0) depends on the removed bond reports that node in
// mgmtPathLoss, and the result matches what ResolveMgmtPaths would return
// post-failure (verified indirectly by the shared-resolver reuse in
// mgmtPathLoss).
func TestSimulate_MgmtPathLoss(t *testing.T) {
	snap, cor := threeNodeVLAN()
	in := Input{Snapshot: snap, Corosync: cor}

	// Removing pve1's bond severs its only mgmt carrier (vmbr0 has bond0 as
	// its sole port).
	im := Simulate(in, inventory.Ref{Kind: inventory.KindBond, Node: "pve1", ID: "bond0"})
	if !eqStrs(sortedCopy(im.MgmtPathLoss), []string{"pve1"}) {
		t.Fatalf("MgmtPathLoss = %v, want [pve1]", im.MgmtPathLoss)
	}
	if im.Severity != SeverityCritical {
		t.Errorf("Severity = %q, want critical (mgmt-path loss)", im.Severity)
	}

	// Removing one uplink NIC of a redundant bond loses no mgmt path.
	im2 := Simulate(in, inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno1"})
	if len(im2.MgmtPathLoss) != 0 {
		t.Errorf("redundant uplink removal: MgmtPathLoss = %v, want none", im2.MgmtPathLoss)
	}
}

// TestSimulate_NotEvaluated covers AC4: with no Ceph or WireGuard data, the
// simulator reports ceph/tunnels as not-evaluated rather than a false
// cephRisk:false / no-tunnel-impact.
func TestSimulate_NotEvaluated(t *testing.T) {
	snap, cor := threeNodeVLAN()

	// No Ceph, no tunnels, but corosync present.
	im := Simulate(Input{Snapshot: snap, Corosync: cor}, nodeRef("pve1"))
	if !hasStr(im.NotEvaluated, DimCeph) {
		t.Errorf("NotEvaluated = %v, want to contain %q", im.NotEvaluated, DimCeph)
	}
	if !hasStr(im.NotEvaluated, DimTunnels) {
		t.Errorf("NotEvaluated = %v, want to contain %q", im.NotEvaluated, DimTunnels)
	}
	if im.CephRisk {
		t.Error("CephRisk = true with no Ceph data; must be a not-evaluated, never a confident false")
	}

	// No corosync either: quorum joins the not-evaluated set.
	im2 := Simulate(Input{Snapshot: snap}, nodeRef("pve1"))
	if !hasStr(im2.NotEvaluated, DimQuorum) {
		t.Errorf("NotEvaluated = %v, want to contain %q", im2.NotEvaluated, DimQuorum)
	}

	// With Ceph installed, the ceph dimension is evaluated (no longer listed).
	status := &ceph.Status{PublicNetwork: "10.0.0.0/24"}
	im3 := Simulate(Input{Snapshot: snap, Corosync: cor, Ceph: status}, nodeRef("pve1"))
	if hasStr(im3.NotEvaluated, DimCeph) {
		t.Errorf("NotEvaluated = %v, ceph should be evaluated once a Status is supplied", im3.NotEvaluated)
	}
}

func sortedCopy(ss []string) []string {
	out := append([]string(nil), ss...)
	sort.Strings(out)
	return out
}
