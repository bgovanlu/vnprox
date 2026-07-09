package change

import (
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// safetyValidate is validator class 3 (docs/features/change-management.md
// §2 item 3; docs/security.md "Safety interlocks"): the change engine
// refuses any changeset whose net effect would (a) remove/re-address a
// protected interface's connectivity or detach its bridge path, or (b)
// delete a bridge with running guests still attached, unless the same
// changeset reattaches every one. Every finding here is SeverityError
// unless safety.AllowDangerousOps downgrades the whole class to
// SeverityWarning (docs/security.md: "override only via config flag
// allow_dangerous_ops") — Service is responsible for auditing that an
// override was exercised (see service.go's auditSafetyOverride).
func safetyValidate(ops []Op, snap inventory.Snapshot, safety SafetyOptions) []Finding {
	var out []Finding
	out = append(out, protectedInterfaceFindings(ops, snap, safety.Protected)...)
	out = append(out, guestBearingBridgeFindings(ops, snap)...)

	if safety.AllowDangerousOps {
		for i := range out {
			out[i].Severity = SeverityWarning
		}
	}
	return out
}

// --- protected interfaces (management IP, corosync links) -----------------

// protectedIP is one address a protected ref carries in the base snapshot
// — the exact thing this changeset must not strand. raw is the original
// declared CIDR string (for the finding message); ip is its parsed host
// address (mask-insensitive identity: re-addressing 10.0.0.5/24 to
// 10.0.0.5/23 is not a connectivity loss, changing the IP itself is).
type protectedIP struct {
	ref inventory.Ref
	raw string
	ip  net.IP
}

// protectedInterfaceFindings evaluates every protected ref's connectivity
// and bridge-path survival against the ops-so-far projection (reusing
// validate_projection.go's projection type, exactly as T-202's report
// suggested). protected is nil/empty when onboarding hasn't confirmed a
// protected set yet, in which case there is nothing to check.
func protectedInterfaceFindings(ops []Op, snap inventory.Snapshot, protected ProtectedSet) []Finding {
	if len(protected) == 0 {
		return nil
	}

	proj := newProjection(snap)
	for _, op := range ops {
		proj.fold(op)
	}

	var out []Finding
	for node, refs := range protected {
		pips := protectedIPsForNode(snap, refs)
		if len(pips) == 0 {
			continue
		}

		for _, pip := range pips {
			if !proj.hasHostIP(node, pip.ip) {
				out = append(out, errorf(codeProtectedInterface, pip.ref.String(),
					"this changeset would remove or re-address %s, currently assigned to protected interface %s on node %s (management IP or corosync link) — no interface would carry it afterward",
					pip.raw, pip.ref, node))
			}
		}

		out = append(out, protectedBridgePathFindings(snap, proj, refs, pips)...)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Ref != out[j].Ref {
			return out[i].Ref < out[j].Ref
		}
		return out[i].Message < out[j].Message
	})
	return out
}

// protectedIPsForNode resolves every ref in refs against the base snapshot
// (before any op in this changeset), collecting the addresses each one
// currently carries. Only inventory.Bridge and inventory.VlanIface declare
// an Addresses field in the current data model (entity.go) — a
// protected.json entry naming a PhysNic/Bond is a known scope gap (see
// this package's T-203 report), not silently mis-handled: such a ref
// simply contributes no protectedIPs, so only its bridge-path (port-count)
// aspect could ever be checked, and PhysNic/Bond have no separate "path"
// notion beyond existing, so nothing further applies to them today.
func protectedIPsForNode(snap inventory.Snapshot, refs []inventory.Ref) []protectedIP {
	var out []protectedIP
	for _, ref := range refs {
		e, ok := snap.Get(ref)
		if !ok {
			continue
		}
		var addrs []string
		switch v := e.(type) {
		case *inventory.Bridge:
			addrs = v.Addresses
		case *inventory.VlanIface:
			addrs = v.Addresses
		default:
			continue
		}
		for _, a := range addrs {
			ip, _, err := net.ParseCIDR(a)
			if err != nil {
				continue
			}
			out = append(out, protectedIP{ip: ip, raw: a, ref: ref})
		}
	}
	return out
}

// protectedBridgePathFindings flags a protected bridge/ovs-bridge that
// still exists and still carries its protected address (so the address-
// connectivity check above passed) but has had every one of its ports
// removed by this changeset — "deletes/detaches its bridge path" from
// docs/security.md, distinct from the address itself being removed: the
// bridge is still configured with the right IP, but nothing physical
// carries it anywhere anymore.
func protectedBridgePathFindings(snap inventory.Snapshot, proj *projection, refs []inventory.Ref, pips []protectedIP) []Finding {
	var out []Finding
	for _, ref := range refs {
		if ref.Kind != inventory.KindBridge && ref.Kind != inventory.KindOVSBridge {
			continue
		}
		orig, ok := snap.Get(ref)
		if !ok {
			continue
		}
		br, ok := orig.(*inventory.Bridge)
		if !ok || len(br.Ports) == 0 {
			continue // nothing was attached in the base state to detach
		}
		if !proj.exists(ref) {
			continue // deleted outright: already covered by the connectivity check above
		}

		carriesProtectedAddr := false
		for _, pip := range pips {
			if pip.ref == ref && proj.hasHostIPOnRef(ref, pip.ip) {
				carriesProtectedAddr = true
				break
			}
		}
		if !carriesProtectedAddr {
			continue
		}

		if proj.portCount(ref) == 0 {
			out = append(out, errorf(codeProtectedInterface, ref.String(),
				"this changeset detaches every port from protected bridge %s, severing its network path even though its address is unchanged",
				ref))
		}
	}
	return out
}

// --- guest-bearing bridge deletion -----------------------------------------

// guestBearingBridgeFindings flags a bridge.delete op targeting a bridge
// that still has running guests attached (docs/features/change-management.md
// §5's delete-with-reattach flow), unless the same changeset also reattaches
// every one of those guests' NICs elsewhere via a guest.nic.update op.
func guestBearingBridgeFindings(ops []Op, snap inventory.Snapshot) []Finding {
	var out []Finding

	// reattached is every GuestNic ref this same changeset reassigns to a
	// (possibly different) bridge/vnet — any guest.nic.update touching
	// BridgeOrVnet counts, matching docs/features/change-management.md §5's
	// "reattachment ops" wording.
	reattached := map[inventory.Ref]bool{}
	for _, op := range ops {
		if op.Type != OpGuestNicUpdate {
			continue
		}
		if params, ok := op.Params.(*GuestNicUpdateParams); ok && params.BridgeOrVnet != nil {
			reattached[op.Target] = true
		}
	}

	for _, op := range ops {
		if op.Type != OpBridgeDelete {
			continue
		}
		target := op.Target

		var attached []string
		for _, e := range snap.All() {
			nic, ok := e.(*inventory.GuestNic)
			if !ok || nic.BridgeOrVnet != target {
				continue
			}
			if reattached[nic.GetRef()] {
				continue
			}
			guestEntity, ok := snap.Get(nic.Guest)
			if !ok {
				continue
			}
			guest, ok := guestEntity.(*inventory.Guest)
			if !ok || guest.Status != "running" {
				continue
			}
			attached = append(attached, fmt.Sprintf("%s (vmid %d, %s)", guest.Name, guest.VMID, nic.Key))
		}

		if len(attached) == 0 {
			continue
		}
		sort.Strings(attached)
		out = append(out, errorf(codeGuestBearingBridge, refOf(op),
			"bridge %s still has %d running guest(s) attached: %s — add guest.nic.update ops reattaching all of them before deleting it",
			target.ID, len(attached), strings.Join(attached, "; ")))
	}
	return out
}
