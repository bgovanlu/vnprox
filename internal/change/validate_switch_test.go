package change

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// switchPortOp builds a switch.port.update op targeting switchID/port.
func switchPortOp(switchID, port string, p *SwitchPortUpdateParams) Op {
	return Op{
		Type:   OpSwitchPortUpdate,
		Target: inventory.Ref{Kind: inventory.KindSwitchPort, ID: switchID + "/" + port},
		Params: p,
	}
}

// findingCode reports whether findings contains an error with the given code.
func hasFindingCode(findings []Finding, code string) bool {
	for _, f := range findings {
		if f.Code == code && f.Severity == SeverityError {
			return true
		}
	}
	return false
}

// TestSwitchValidate_FeatureFlagAndEnabled covers T-1205 AC1 (daemon flag off →
// no write accepted) and AC2 (a registered switch with enabled:false rejects
// the op; enabling it allows the same op).
func TestSwitchValidate_FeatureFlagAndEnabled(t *testing.T) {
	op := switchPortOp("sw1", "Ethernet1", &SwitchPortUpdateParams{Description: strPtr("pve1 uplink")})
	portRef := op.Target.String()

	scoped := SwitchSafetyInput{
		Switches: map[string]SwitchState{"sw1": {Enabled: true}},
		Ports:    map[string]SwitchPortState{portRef: {PVEFacing: true}},
	}

	// AC1: daemon flag off — even a fully-scoped, enabled, PVE-facing op is
	// rejected with push_disabled.
	off := scoped
	off.PushEnabled = false
	if got := switchValidate([]Op{op}, off); !hasFindingCode(got, codeSwitchPushDisabled) {
		t.Fatalf("flag off: want %s, got %+v", codeSwitchPushDisabled, got)
	}

	// AC2: flag on but switch enabled=false → not_enabled.
	disabled := scoped
	disabled.PushEnabled = true
	disabled.Switches = map[string]SwitchState{"sw1": {Enabled: false}}
	if got := switchValidate([]Op{op}, disabled); !hasFindingCode(got, codeSwitchNotEnabled) {
		t.Fatalf("switch disabled: want %s, got %+v", codeSwitchNotEnabled, got)
	}

	// AC2: enabling the same switch allows the same op (no findings).
	enabled := scoped
	enabled.PushEnabled = true
	if got := switchValidate([]Op{op}, enabled); len(got) != 0 {
		t.Fatalf("switch enabled: want no findings, got %+v", got)
	}
}

// TestSwitchValidate_PortNotPVEFacing covers T-1205 AC3: an op targeting a port
// whose LLDP neighbor is not a known PVE PhysNic is rejected before any driver
// call.
func TestSwitchValidate_PortNotPVEFacing(t *testing.T) {
	op := switchPortOp("sw1", "Ethernet9", &SwitchPortUpdateParams{Description: strPtr("not a pve port")})
	portRef := op.Target.String()

	input := SwitchSafetyInput{
		PushEnabled: true,
		Switches:    map[string]SwitchState{"sw1": {Enabled: true}},
		Ports:       map[string]SwitchPortState{portRef: {PVEFacing: false}},
	}
	if got := switchValidate([]Op{op}, input); !hasFindingCode(got, codeSwitchPortNotPVEFacing) {
		t.Fatalf("non-PVE-facing port: want %s, got %+v", codeSwitchPortNotPVEFacing, got)
	}

	// A port entirely absent from the scope (never observed facing a PVE node)
	// is likewise rejected.
	input.Ports = map[string]SwitchPortState{}
	if got := switchValidate([]Op{op}, input); !hasFindingCode(got, codeSwitchPortNotPVEFacing) {
		t.Fatalf("unscoped port: want %s, got %+v", codeSwitchPortNotPVEFacing, got)
	}
}

// TestSwitchValidate_ProtectedSwitchPort covers T-1205 AC5's interlock: a
// switch.port.update whose net effect strips the management VLAN from a
// management-path uplink port is hard-blocked with no override.
func TestSwitchValidate_ProtectedSwitchPort(t *testing.T) {
	portID := "Ethernet1"
	mgmtVLAN := 100
	baseInput := func(p *SwitchPortUpdateParams) (Op, SwitchSafetyInput) {
		op := switchPortOp("sw1", portID, p)
		return op, SwitchSafetyInput{
			PushEnabled: true,
			Switches:    map[string]SwitchState{"sw1": {Enabled: true}},
			Ports:       map[string]SwitchPortState{op.Target.String(): {PVEFacing: true, MgmtPath: true, MgmtVLAN: mgmtVLAN}},
		}
	}

	// Stripping VLAN 100 from a mgmt-path port → blocked.
	strip := &SwitchPortUpdateParams{Untagged: intPtr(20), Tagged: &[]int{10, 20}}
	op, in := baseInput(strip)
	if got := switchValidate([]Op{op}, in); !hasFindingCode(got, codeProtectedSwitchPort) {
		t.Fatalf("stripping mgmt VLAN: want %s, got %+v", codeProtectedSwitchPort, got)
	}

	// Keeping VLAN 100 (in the trunk set) → allowed.
	keep := &SwitchPortUpdateParams{Untagged: intPtr(100), Tagged: &[]int{100, 10, 20}}
	op, in = baseInput(keep)
	if got := switchValidate([]Op{op}, in); hasFindingCode(got, codeProtectedSwitchPort) {
		t.Fatalf("keeping mgmt VLAN: want no protected finding, got %+v", got)
	}

	// A description-only op does not set VLAN membership → never strips mgmt.
	descOnly := &SwitchPortUpdateParams{Description: strPtr("relabel")}
	op, in = baseInput(descOnly)
	if got := switchValidate([]Op{op}, in); len(got) != 0 {
		t.Fatalf("description-only op on mgmt-path port: want no findings, got %+v", got)
	}
}

// TestSwitchValidate_ProtectedSwitchPort_NoOverride proves AllowDangerousOps
// does NOT downgrade the protected-switch-port interlock (T-1205 AC5 / T-703's
// "no override" rule), unlike T-203's downgradable safety class.
func TestSwitchValidate_ProtectedSwitchPort_NoOverride(t *testing.T) {
	op := switchPortOp("sw1", "Ethernet1", &SwitchPortUpdateParams{Untagged: intPtr(20), Tagged: &[]int{10, 20}})
	snap := inventory.NewGraph().Snapshot()
	safety := SafetyOptions{
		AllowDangerousOps: true, // would downgrade T-203 findings — but must NOT touch this one
		Switches: SwitchSafetyInput{
			PushEnabled: true,
			Switches:    map[string]SwitchState{"sw1": {Enabled: true}},
			Ports:       map[string]SwitchPortState{op.Target.String(): {PVEFacing: true, MgmtPath: true, MgmtVLAN: 100}},
		},
	}
	findings := ValidateWithSafety([]Op{op}, snap, safety)
	if !hasFindingCode(findings, codeProtectedSwitchPort) {
		t.Fatalf("protected_switch_port must remain a blocking error under allow_dangerous_ops, got %+v", findings)
	}
}

