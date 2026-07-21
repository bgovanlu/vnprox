package failsim

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// TestPreflight_WorstOf: Preflight returns the highest-severity Impact among a
// changeset's touched refs.
func TestPreflight_WorstOf(t *testing.T) {
	snap, cor := threeNodeVLAN()
	in := Input{Snapshot: snap, Corosync: cor}

	// One touched ref is a redundant NIC (harmless), the other is the sole-port
	// bond (mgmt-path loss) — the bond's critical impact must win.
	refs := []inventory.Ref{
		{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno1"},
		{Kind: inventory.KindBond, Node: "pve1", ID: "bond0"},
	}
	im := Preflight(in, refs)
	if im.Severity != SeverityCritical {
		t.Fatalf("Preflight severity = %q, want critical (worst of the touched refs)", im.Severity)
	}
	if len(im.MgmtPathLoss) == 0 {
		t.Errorf("worst impact should carry the bond's mgmt-path loss, got %+v", im)
	}
}

// TestPreflightUnsafe covers the additive veto decision consumed by the
// scheduler: quorum risk and mgmt-path loss block; guest disconnection alone
// does not.
func TestPreflightUnsafe(t *testing.T) {
	threeSnap, threeCor := threeNodeVLAN()
	threeIn := Input{Snapshot: threeSnap, Corosync: threeCor}

	switchSnap, switchCor := sharedSwitchCluster(3, true)
	switchIn := Input{Snapshot: switchSnap, Corosync: switchCor}

	cases := []struct {
		target     inventory.Ref
		name       string
		wantReason string
		in         Input
		wantUnsafe bool
	}{
		{
			name:       "mgmt-path loss blocks",
			in:         threeIn,
			target:     inventory.Ref{Kind: inventory.KindBond, Node: "pve1", ID: "bond0"},
			wantUnsafe: true,
			wantReason: ReasonMgmtPathLoss,
		},
		{
			name:       "quorum risk blocks",
			in:         switchIn,
			target:     inventory.Ref{Kind: inventory.KindSwitchPort, ID: "sw-core"},
			wantUnsafe: true,
			wantReason: ReasonQuorumRisk,
		},
		{
			name:       "redundant uplink is safe",
			in:         threeIn,
			target:     inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno1"},
			wantUnsafe: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			im := Simulate(tc.in, tc.target)
			unsafe, reason := PreflightUnsafe(im)
			if unsafe != tc.wantUnsafe {
				t.Fatalf("PreflightUnsafe = %v, want %v (impact %+v)", unsafe, tc.wantUnsafe, im)
			}
			if tc.wantUnsafe && reason != tc.wantReason {
				t.Errorf("reason = %q, want %q", reason, tc.wantReason)
			}
		})
	}
}

// TestHonestyInventory asserts the grep-able audit inventory (AC7) covers
// every not-evaluated dimension code, keeping code and report in lockstep.
func TestHonestyInventory(t *testing.T) {
	inv := HonestyInventory()
	want := []string{DimQuorum, DimCeph, DimTunnels, DimGuestConnectivity}
	for _, code := range want {
		found := false
		for _, row := range inv {
			if row.Code == code {
				found = true
			}
		}
		if !found {
			t.Errorf("honesty inventory missing dimension %q", code)
		}
	}
}
