package change

import (
	"context"
	"strings"
)

// SwitchState is one registered switch's push-relevant state for validation:
// whether it is enabled for push (per-switch explicit opt-in, docs/security.md).
type SwitchState struct {
	Enabled bool
}

// SwitchPortState is one switch port's scoping/interlock facts for validation.
type SwitchPortState struct {
	// PVEFacing is true iff this port's LLDP-observed neighbor is a known PVE
	// node's PhysNic (internal/topology's LLDP-neighbor merge). Only PVE-facing
	// ports are writable; any other port target is rejected before any driver
	// call (T-1205 AC3).
	PVEFacing bool
	// MgmtPath is true iff this uplink port carries a node's resolved
	// management path (internal/topology.ResolveMgmtPaths extended one hop onto
	// the switch port). A switch.port.update whose net effect strips MgmtVLAN
	// from such a port is hard-blocked with no override (T-1205 AC5).
	MgmtPath bool
	// MgmtVLAN is the management VLAN that must not be stripped from a MgmtPath
	// port. 0 means "not resolved" — the protected-switch-port interlock only
	// evaluates when it is known (>0), documented in switchValidate.
	MgmtVLAN int
}

// SwitchSafetyInput is T-1205's switch-push validation context (SafetyOptions.
// Switches): the daemon feature flag, the registered/enabled switches, and the
// per-port scoping/interlock facts, all keyed the way switchValidate consumes
// them. A zero value fails every switch.port.* op closed.
type SwitchSafetyInput struct {
	Switches    map[string]SwitchState
	Ports       map[string]SwitchPortState
	PushEnabled bool
}

// SwitchScopeSource supplies switchValidate's per-request scoping context (the
// registered/enabled switches and per-port PVE-facing/mgmt-path facts). The
// daemon builds it from the switches app-store table plus internal/topology's
// LLDP-neighbor merge and management-path resolver. Optional on Config: a nil
// source (or a read error) degrades to "nothing scoped", so every switch.port.*
// op is rejected — fail-closed. PushEnabled from the returned value is ignored;
// the daemon flag on Service is authoritative.
type SwitchScopeSource interface {
	SwitchScope(ctx context.Context) (SwitchSafetyInput, error)
}

// switchSafetyInput builds the SwitchSafetyInput for one validation call:
// PushEnabled from the daemon flag (authoritative), plus the live scoping from
// the optional SwitchScopeSource. A nil source or a failed read yields no
// scoped switches/ports, so every switch.port.* op is rejected — the same
// fail-closed default that keeps switch push dark.
func (s *Service) switchSafetyInput(ctx context.Context) SwitchSafetyInput {
	in := SwitchSafetyInput{PushEnabled: s.switchPushEnabled}
	if s.switchScope == nil {
		return in
	}
	scoped, err := s.switchScope.SwitchScope(ctx)
	if err != nil {
		s.log.Warn("change: reading switch scope for validation failed, treating as nothing scoped", "error", err)
		return in
	}
	in.Switches = scoped.Switches
	in.Ports = scoped.Ports
	return in
}

// switchValidate is T-1205's switch-push authorization + interlock class. For
// each switch.port.* op it enforces, in order:
//
//  1. push_disabled — the daemon [switches] flag is off (feature ships dark);
//  2. not_enabled — the target switch is unregistered or enabled=false;
//  3. port_not_pve_facing — the target port's LLDP neighbor is not a PVE node;
//  4. protected_switch_port — the op's net effect strips the management VLAN
//     from a management-path uplink port (no override).
//
// The first three are blocking authorization gates; a failure short-circuits
// the rest for that op (there is no point checking scoping on a switch that
// cannot be pushed to). The fourth is a no-override safety interlock,
// deliberately emitted from this class rather than safetyValidate so
// AllowDangerousOps never downgrades it (docs/security.md).
func switchValidate(ops []Op, input SwitchSafetyInput) []Finding {
	var out []Finding
	for _, op := range ops {
		if op.Type != OpSwitchPortUpdate {
			continue
		}
		portRef := op.Target.String()
		switchID := switchIDOf(op.Target.ID)

		if !input.PushEnabled {
			out = append(out, errorf(codeSwitchPushDisabled, portRef,
				"switch push is disabled on this daemon ([switches] enabled = false); no switch.port.update can be applied until it is turned on"))
			continue
		}
		if sw, ok := input.Switches[switchID]; !ok || !sw.Enabled {
			out = append(out, errorf(codeSwitchNotEnabled, portRef,
				"switch %s is not registered or not enabled for push; enable it explicitly before targeting its ports", switchID))
			continue
		}
		port, ok := input.Ports[portRef]
		if !ok || !port.PVEFacing {
			out = append(out, errorf(codeSwitchPortNotPVEFacing, portRef,
				"port %s does not face a known PVE node (its LLDP neighbor is not a cluster node's physical NIC); switch push is scoped strictly to PVE-facing uplink ports", op.Target.ID))
			continue
		}
		if port.MgmtPath && port.MgmtVLAN > 0 {
			if params, ok := op.Params.(*SwitchPortUpdateParams); ok &&
				params.setsVLANMembership() && !params.carriesVLAN(port.MgmtVLAN) {
				out = append(out, errorf(codeProtectedSwitchPort, portRef,
					"this changeset would strip management VLAN %d from switch port %s, which carries a node's management path — refused (no override): severing it could cut connectivity to hardware vnprox cannot itself recover",
					port.MgmtVLAN, op.Target.ID))
			}
		}
	}
	return out
}

// switchIDOf extracts the switch app-store id from a switch-port Ref id
// ("<switchID>/<port>"). A ref id with no '/' (malformed) yields the whole
// string, which simply won't match any registered switch.
func switchIDOf(refID string) string {
	if i := strings.IndexByte(refID, '/'); i > 0 {
		return refID[:i]
	}
	return refID
}
