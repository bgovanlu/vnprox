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
				continue
			}
			// The address survives — but check what carries it now. If it
			// ended up on a bridge other than its original protected
			// carrier, that bridge must have a physical path (>= 1 port) in
			// the final state: parking the management IP on a port-less
			// bridge severs connectivity just as surely as removing the
			// address (audit-phase-2 F-02: "delete vmbr0; create vmbr9 with
			// the same IP and no ports" validated clean).
			for _, e := range proj.addrs[node] {
				if e.ref == pip.ref || !e.hostIP.Equal(pip.ip) {
					continue
				}
				if e.ref.Kind != inventory.KindBridge && e.ref.Kind != inventory.KindOVSBridge {
					continue
				}
				if proj.portCount(e.ref) == 0 {
					out = append(out, errorf(codeProtectedInterface, pip.ref.String(),
						"this changeset moves protected address %s from %s to bridge %s, which would have no ports — nothing physical would carry it",
						pip.raw, pip.ref, e.ref))
				}
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

// guestBearingBridgeFindings flags every bridge.delete op whose *net
// effect* leaves a running guest's NIC attached to nothing that survives
// the changeset (docs/features/change-management.md §5's
// delete-with-reattach flow). The analysis is net-effect-based
// (audit-phase-2 F-01): a guest.nic.update only clears the error if the
// NIC's *final* attachment exists in the changeset's final projection —
// "reattaching" to the doomed bridge itself, or to a bridge/vnet the same
// changeset also deletes, does not.
func guestBearingBridgeFindings(ops []Op, snap inventory.Snapshot) []Finding {
	// deleteOps: (node, name) of every bridge this changeset deletes →
	// index of its (last) bridge.delete op, for attributing findings.
	deleteOps := map[ifaceKey]int{}
	for i, op := range ops {
		if op.Type == OpBridgeDelete {
			deleteOps[ifaceKey{op.Target.Node, op.Target.ID}] = i
		}
	}
	if len(deleteOps) == 0 {
		return nil
	}

	// Final projection: the base snapshot with every op folded in — what
	// actually exists once the whole changeset has applied.
	proj := newProjection(snap)
	for _, op := range ops {
		proj.fold(op)
	}

	// finalAttach: where each guest.nic.update leaves its NIC pointing (the
	// last update naming a bridge/vnet wins).
	finalAttach := map[inventory.Ref]string{}
	for _, op := range ops {
		if op.Type != OpGuestNicUpdate {
			continue
		}
		if params, ok := op.Params.(*GuestNicUpdateParams); ok && params.BridgeOrVnet != nil {
			finalAttach[op.Target] = *params.BridgeOrVnet
		}
	}

	// stranded: per bridge.delete op index, the running-guest NICs whose
	// final attachment that delete dooms.
	stranded := map[int][]string{}
	for _, e := range snap.All() {
		nic, ok := e.(*inventory.GuestNic)
		if !ok {
			continue
		}
		nicRef := nic.GetRef()
		node := nicRef.Node

		var origKey ifaceKey
		origDeleted := false
		if nic.BridgeOrVnet.Kind == inventory.KindBridge || nic.BridgeOrVnet.Kind == inventory.KindOVSBridge {
			origKey = ifaceKey{nic.BridgeOrVnet.Node, nic.BridgeOrVnet.ID}
			_, origDeleted = deleteOps[origKey]
		}

		finalName, updated := finalAttach[nicRef]
		if !updated {
			if !origDeleted {
				continue // attachment untouched and its bridge survives
			}
			finalName = nic.BridgeOrVnet.ID
		}
		if proj.guestTargetExists(node, finalName) {
			continue // net effect: attached to a surviving bridge/vnet
		}

		// The NIC ends up attached to nothing that survives. Attribute the
		// finding to the delete op that dooms the final target, else to the
		// one that dooms the original bridge. (A dangling reattach target
		// with no bridge.delete involved is the referential class's
		// bridge_or_vnet_not_found, not a safety finding.)
		opIdx, ok := deleteOps[ifaceKey{node, finalName}]
		if !ok {
			if !origDeleted {
				continue
			}
			opIdx = deleteOps[origKey]
		}

		guestEntity, ok := snap.Get(nic.Guest)
		if !ok {
			continue
		}
		guest, ok := guestEntity.(*inventory.Guest)
		if !ok || guest.Status != "running" {
			continue
		}
		stranded[opIdx] = append(stranded[opIdx], fmt.Sprintf("%s (vmid %d, %s)", guest.Name, guest.VMID, nic.Key))
	}

	var out []Finding
	for i, op := range ops {
		attached := stranded[i]
		if len(attached) == 0 {
			continue
		}
		sort.Strings(attached)
		out = append(out, errorf(codeGuestBearingBridge, refOf(op),
			"bridge %s still has %d running guest(s) attached in this changeset's final state: %s — add guest.nic.update ops reattaching all of them to a surviving bridge or vnet before deleting it",
			op.Target.ID, len(attached), strings.Join(attached, "; ")))
	}
	return out
}
