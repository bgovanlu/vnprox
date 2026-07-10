package change

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// --- shared fixtures for this file ------------------------------------

// mgmtProtected is a ProtectedSet naming pve1's vmbr0 as the sole protected
// interface, the shape safetyValidate consumes directly (as opposed to
// ProtectedConfig, protected.go's on-disk/wire shape).
func mgmtProtected() ProtectedSet {
	return ProtectedSet{"pve1": {testRef(inventory.KindBridge, "pve1", "vmbr0")}}
}

// baseMgmtSnapshot is a one-node snapshot with vmbr0 carrying the node's
// management IP on a single physical port, and an unrelated second bridge
// vmbr1 with no address at all — the T-203 acceptance criterion 1 fixture
// ("deleting an unrelated bridge -> no interlock finding").
func baseMgmtSnapshot() inventory.Snapshot {
	return buildSnapshot(
		&inventory.Node{Ref: testRef(inventory.KindNode, "pve1", "pve1"), Name: "pve1", IP: "10.10.0.1"},
		&inventory.PhysNic{Ref: testRef(inventory.KindPhysNic, "pve1", "eno1"), Name: "eno1"},
		&inventory.Bridge{
			Ref: testRef(inventory.KindBridge, "pve1", "vmbr0"), Name: "vmbr0",
			// PortNames (not Ports): Bridge.Ports is resolved from
			// PortNames/DeclaredPortNames by internal/inventory's linking
			// pass (link.go), not settable directly on the raw entity fed
			// into ApplyPoll.
			PortNames: []string{"eno1"},
			Addresses: []string{"10.10.0.1/24"},
		},
		&inventory.Bridge{Ref: testRef(inventory.KindBridge, "pve1", "vmbr1"), Name: "vmbr1"},
	)
}

// --- acceptance criterion 1: protected-interface connectivity ------------

func TestSafetyValidate_ProtectedInterface(t *testing.T) {
	protected := mgmtProtected()
	vmbr0 := testRef(inventory.KindBridge, "pve1", "vmbr0")
	vmbr1 := testRef(inventory.KindBridge, "pve1", "vmbr1")

	tests := []struct {
		name string
		ops  []Op
		want []wantFinding
	}{
		{
			name: "deleting vmbr0 (carries mgmt IP) errors",
			ops:  []Op{mkOp(OpBridgeDelete, vmbr0, &BridgeDeleteParams{})},
			want: []wantFinding{{SeverityError, codeProtectedInterface, vmbr0.String()}},
		},
		{
			name: "re-addressing vmbr0 to a different IP errors",
			ops: []Op{mkOp(OpBridgeUpdate, vmbr0, &BridgeUpdateParams{
				Addresses: strsPtr("10.10.0.99/24"),
			})},
			want: []wantFinding{{SeverityError, codeProtectedInterface, vmbr0.String()}},
		},
		{
			name: "re-addressing vmbr0 to the same IP with a different mask does not error",
			ops: []Op{mkOp(OpBridgeUpdate, vmbr0, &BridgeUpdateParams{
				Addresses: strsPtr("10.10.0.1/23"),
			})},
			want: nil,
		},
		{
			name: "deleting an unrelated bridge (vmbr1) has no interlock finding",
			ops:  []Op{mkOp(OpBridgeDelete, vmbr1, &BridgeDeleteParams{})},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := safetyValidate(tt.ops, baseMgmtSnapshot(), SafetyOptions{Protected: protected})
			assertFindings(t, findings, tt.want)
		})
	}
}

// TestSafetyValidate_BridgePathDetachment covers docs/security.md's "...or
// deletes/detaches its bridge path" clause: the protected bridge's address
// is untouched, but every physical port carrying it has been removed.
func TestSafetyValidate_BridgePathDetachment(t *testing.T) {
	protected := mgmtProtected()
	vmbr0 := testRef(inventory.KindBridge, "pve1", "vmbr0")

	twoPortSnap := buildSnapshot(
		&inventory.Node{Ref: testRef(inventory.KindNode, "pve1", "pve1"), Name: "pve1", IP: "10.10.0.1"},
		&inventory.PhysNic{Ref: testRef(inventory.KindPhysNic, "pve1", "eno1"), Name: "eno1"},
		&inventory.PhysNic{Ref: testRef(inventory.KindPhysNic, "pve1", "eno2"), Name: "eno2"},
		&inventory.Bridge{
			Ref: vmbr0, Name: "vmbr0",
			PortNames: []string{"eno1", "eno2"},
			Addresses: []string{"10.10.0.1/24"},
		},
	)

	t.Run("removing every port detaches the bridge path", func(t *testing.T) {
		ops := []Op{
			mkOp(OpBridgePortRemove, vmbr0, &BridgePortRemoveParams{Port: "eno1"}),
			mkOp(OpBridgePortRemove, vmbr0, &BridgePortRemoveParams{Port: "eno2"}),
		}
		findings := safetyValidate(ops, twoPortSnap, SafetyOptions{Protected: protected})
		assertFindings(t, findings, []wantFinding{{SeverityError, codeProtectedInterface, vmbr0.String()}})
	})

	t.Run("removing only one of two ports leaves connectivity intact", func(t *testing.T) {
		ops := []Op{mkOp(OpBridgePortRemove, vmbr0, &BridgePortRemoveParams{Port: "eno1"})}
		findings := safetyValidate(ops, twoPortSnap, SafetyOptions{Protected: protected})
		assertFindings(t, findings, nil)
	})
}

// TestSafetyValidate_ChainAnalysis is T-203 acceptance criterion 2: moving
// the mgmt IP to a new bridge and deleting the old one in the same
// changeset validates clean (connectivity preserved overall).
func TestSafetyValidate_ChainAnalysis(t *testing.T) {
	protected := mgmtProtected()
	vmbr0 := testRef(inventory.KindBridge, "pve1", "vmbr0")
	vmbr2 := testRef(inventory.KindBridge, "pve1", "vmbr2")

	ops := []Op{
		mkOp(OpBridgeCreate, vmbr2, &BridgeCreateParams{
			Ports:     []string{"eno1"},
			Addresses: []string{"10.10.0.1/24"},
		}),
		mkOp(OpBridgeDelete, vmbr0, &BridgeDeleteParams{}),
	}
	findings := safetyValidate(ops, baseMgmtSnapshot(), SafetyOptions{Protected: protected})
	assertFindings(t, findings, nil)
}

func TestSafetyValidate_NoProtectedSet_NoFindings(t *testing.T) {
	vmbr0 := testRef(inventory.KindBridge, "pve1", "vmbr0")
	ops := []Op{mkOp(OpBridgeDelete, vmbr0, &BridgeDeleteParams{})}
	findings := safetyValidate(ops, baseMgmtSnapshot(), SafetyOptions{})
	assertFindings(t, findings, nil)
}

// --- acceptance criterion 3: guest-bearing bridge deletion ----------------

func guestBearingSnapshot() inventory.Snapshot {
	vmbr2 := testRef(inventory.KindBridge, "pve1", "vmbr2")
	return buildSnapshot(
		&inventory.Bridge{Ref: vmbr2, Name: "vmbr2"},
		// vmbr3 exists as a genuine reattachment target: since the F-01
		// remediation the reattach check is net-effect-based, so a
		// guest.nic.update only clears the error if its target actually
		// survives the changeset.
		&inventory.Bridge{Ref: testRef(inventory.KindBridge, "pve1", "vmbr3"), Name: "vmbr3"},
		&inventory.Guest{Ref: testRef(inventory.KindGuest, "pve1", "100"), Name: "web01", VMID: 100, Status: "running"},
		&inventory.GuestNic{
			Ref:   testRef(inventory.KindGuestNic, "pve1", "100/net0"),
			Guest: testRef(inventory.KindGuest, "pve1", "100"), Key: "net0",
			BridgeOrVnet: vmbr2,
		},
		&inventory.Guest{Ref: testRef(inventory.KindGuest, "pve1", "101"), Name: "web02", VMID: 101, Status: "running"},
		&inventory.GuestNic{
			Ref:   testRef(inventory.KindGuestNic, "pve1", "101/net0"),
			Guest: testRef(inventory.KindGuest, "pve1", "101"), Key: "net0",
			BridgeOrVnet: vmbr2,
		},
		&inventory.Guest{Ref: testRef(inventory.KindGuest, "pve1", "102"), Name: "stopped01", VMID: 102, Status: "stopped"},
		&inventory.GuestNic{
			Ref:   testRef(inventory.KindGuestNic, "pve1", "102/net0"),
			Guest: testRef(inventory.KindGuest, "pve1", "102"), Key: "net0",
			BridgeOrVnet: vmbr2,
		},
	)
}

func TestSafetyValidate_GuestBearingBridgeDeletion(t *testing.T) {
	vmbr2 := testRef(inventory.KindBridge, "pve1", "vmbr2")
	deleteOp := mkOp(OpBridgeDelete, vmbr2, &BridgeDeleteParams{})

	t.Run("deleting a bridge with running guests errors, stopped guests don't count", func(t *testing.T) {
		findings := safetyValidate([]Op{deleteOp}, guestBearingSnapshot(), SafetyOptions{})
		if len(findings) != 1 {
			t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
		}
		f := findings[0]
		if f.Severity != SeverityError || f.Code != codeGuestBearingBridge {
			t.Errorf("finding = %+v, want severity=error code=%s", f, codeGuestBearingBridge)
		}
		for _, guest := range []string{"web01", "web02"} {
			if !strings.Contains(f.Message, guest) {
				t.Errorf("message %q does not mention running guest %q", f.Message, guest)
			}
		}
		if strings.Contains(f.Message, "stopped01") {
			t.Errorf("message %q must not mention the stopped guest", f.Message)
		}
	})

	t.Run("reattaching every running guest clears the error", func(t *testing.T) {
		vmbr3 := testRef(inventory.KindBridge, "pve1", "vmbr3")
		ops := []Op{
			deleteOp,
			mkOp(OpGuestNicUpdate, testRef(inventory.KindGuestNic, "pve1", "100/net0"),
				&GuestNicUpdateParams{BridgeOrVnet: strPtr(vmbr3.ID)}),
			mkOp(OpGuestNicUpdate, testRef(inventory.KindGuestNic, "pve1", "101/net0"),
				&GuestNicUpdateParams{BridgeOrVnet: strPtr(vmbr3.ID)}),
		}
		findings := safetyValidate(ops, guestBearingSnapshot(), SafetyOptions{})
		assertFindings(t, findings, nil)
	})

	t.Run("reattaching only one of two running guests leaves the error naming the other", func(t *testing.T) {
		vmbr3 := testRef(inventory.KindBridge, "pve1", "vmbr3")
		ops := []Op{
			deleteOp,
			mkOp(OpGuestNicUpdate, testRef(inventory.KindGuestNic, "pve1", "100/net0"),
				&GuestNicUpdateParams{BridgeOrVnet: strPtr(vmbr3.ID)}),
		}
		findings := safetyValidate(ops, guestBearingSnapshot(), SafetyOptions{})
		if len(findings) != 1 {
			t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
		}
		if strings.Contains(findings[0].Message, "web01") {
			t.Errorf("message %q should no longer mention the reattached guest web01", findings[0].Message)
		}
		if !strings.Contains(findings[0].Message, "web02") {
			t.Errorf("message %q must still mention the not-yet-reattached guest web02", findings[0].Message)
		}
	})
}

// --- acceptance criterion 4: allow_dangerous_ops downgrade ----------------

func TestSafetyValidate_AllowDangerousOps_Downgrades(t *testing.T) {
	protected := mgmtProtected()
	vmbr0 := testRef(inventory.KindBridge, "pve1", "vmbr0")
	deleteOp := mkOp(OpBridgeDelete, vmbr0, &BridgeDeleteParams{})

	findings := safetyValidate([]Op{deleteOp}, baseMgmtSnapshot(), SafetyOptions{Protected: protected, AllowDangerousOps: true})
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
	if findings[0].Severity != SeverityWarning {
		t.Errorf("severity = %s, want warning (allow_dangerous_ops=true)", findings[0].Severity)
	}
	if findings[0].Code != codeProtectedInterface {
		t.Errorf("code = %s, want %s (code unchanged by the downgrade)", findings[0].Code, codeProtectedInterface)
	}
}

// TestValidateWithSafety_WarningDoesNotShortCircuitAdvisory proves the
// downgraded safety warning behaves like every other class's warnings: it
// does not block the advisory class from running afterward (only
// SeverityError findings short-circuit, per validate.go's hasError gate).
func TestValidateWithSafety_WarningDoesNotShortCircuitAdvisory(t *testing.T) {
	protected := mgmtProtected()
	vmbr0 := testRef(inventory.KindBridge, "pve1", "vmbr0")

	ops := []Op{
		mkOp(OpBridgeDelete, vmbr0, &BridgeDeleteParams{}),
		// A single-slave bond create triggers an advisory warning
		// (codeAdvisorySingleSlave) — if the safety class's downgraded
		// warning incorrectly short-circuited, this finding would be
		// missing from the result.
		mkOp(OpBondCreate, testRef(inventory.KindBond, "pve1", "bond0"),
			&BondCreateParams{Mode: "active-backup", Slaves: []string{"eno1"}}),
	}

	findings := ValidateWithSafety(ops, baseMgmtSnapshot(), SafetyOptions{Protected: protected, AllowDangerousOps: true})

	var sawSafetyWarning, sawAdvisoryWarning bool
	for _, f := range findings {
		if f.Code == codeProtectedInterface && f.Severity == SeverityWarning {
			sawSafetyWarning = true
		}
		if f.Code == codeAdvisorySingleSlave {
			sawAdvisoryWarning = true
		}
	}
	if !sawSafetyWarning {
		t.Errorf("expected a downgraded safety.protected_interface warning, got %+v", findings)
	}
	if !sawAdvisoryWarning {
		t.Errorf("advisory class did not run after the downgraded safety warning (should not short-circuit): %+v", findings)
	}
}
