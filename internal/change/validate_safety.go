// SPDX-License-Identifier: Apache-2.0

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
	out = append(out, ifaceRenameGuestFindings(ops, snap)...)
	out = append(out, vnetDeletionGuardFindings(ops, snap)...)
	out = append(out, subnetDeletionGuardFindings(ops, safety.Allocations)...)
	out = append(out, dnsZoneDeletionGuardFindings(ops, snap)...)

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

// --- guest-bearing sdn vnet deletion (T-402) -------------------------------

// vnetDeletionGuardFindings is guestBearingBridgeFindings' sdn.vnet.delete
// counterpart (T-402, deliberately mirroring T-203's interlock pattern
// exactly, including net-effect analysis and reattach-in-same-changeset
// clearing — the task card's own words: "Deleting a vnet with attached
// guest NICs → blocked with attachment list (reattach-in-same-changeset
// clears it, mirroring T-203 pattern)"): flags every sdn.vnet.delete op
// whose net effect leaves a running guest's NIC attached to nothing that
// survives the changeset.
//
// This is a separate function rather than a generalization of
// guestBearingBridgeFindings because bridges are per-node (keyed by
// (node,name) in that function's ifaceKey) while sdn-vnets are
// cluster-scoped (keyed by plain vnet ID here) — different enough key
// shapes that sharing one function body would need a false node dimension
// threaded through the vnet-only path.
func vnetDeletionGuardFindings(ops []Op, snap inventory.Snapshot) []Finding {
	// deleteOps: vnet id -> index of its (last) sdn.vnet.delete op.
	deleteOps := map[string]int{}
	for i, op := range ops {
		if op.Type == OpSdnVnetDelete {
			deleteOps[op.Target.ID] = i
		}
	}
	if len(deleteOps) == 0 {
		return nil
	}

	proj := newProjection(snap)
	for _, op := range ops {
		proj.fold(op)
	}

	finalAttach := map[inventory.Ref]string{}
	for _, op := range ops {
		if op.Type != OpGuestNicUpdate {
			continue
		}
		if params, ok := op.Params.(*GuestNicUpdateParams); ok && params.BridgeOrVnet != nil {
			finalAttach[op.Target] = *params.BridgeOrVnet
		}
	}

	stranded := map[int][]string{}
	for _, e := range snap.All() {
		nic, ok := e.(*inventory.GuestNic)
		if !ok {
			continue
		}
		nicRef := nic.GetRef()
		node := nicRef.Node

		origDeleted := false
		if nic.BridgeOrVnet.Kind == inventory.KindSDNVnet {
			_, origDeleted = deleteOps[nic.BridgeOrVnet.ID]
		}

		finalName, updated := finalAttach[nicRef]
		if !updated {
			if !origDeleted {
				continue // attachment untouched and its vnet survives
			}
			finalName = nic.BridgeOrVnet.ID
		}
		if proj.guestTargetExists(node, finalName) {
			continue // net effect: attached to a surviving bridge/vnet
		}

		opIdx, ok := deleteOps[finalName]
		if !ok {
			if !origDeleted {
				continue
			}
			opIdx = deleteOps[nic.BridgeOrVnet.ID]
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
			"vnet %s still has %d running guest(s) attached in this changeset's final state: %s — add guest.nic.update ops reattaching all of them to a surviving bridge or vnet before deleting it",
			op.Target.ID, len(attached), strings.Join(attached, "; ")))
	}
	return out
}

// --- subnet-with-allocations deletion (T-402's other listed deletion guard,
// closed out here once T-405 gave this package a live IPAM data feed to
// check against) ------------------------------------------------------------

// dnsZoneDeletionGuardFindings flags every sdn.dns.zone.delete op whose
// target zone still has one or more DNS records that this same changeset
// does not also delete (T-1204 acceptance criterion 3). Deleting the zone
// would orphan those records in PowerDNS, so the changeset must cascade a
// sdn.dns.record.delete for each. Net-effect-aware exactly like the subnet/
// vnet deletion guards: a record removed elsewhere in the same changeset
// clears it from the count, so "delete every record, then delete the
// now-empty zone" validates clean in one changeset. Records live in the
// inventory snapshot (SdnDnsRecord entities, ingested by the SDN poll), the
// same authoritative source the referential/projection checks read.
func dnsZoneDeletionGuardFindings(ops []Op, snap inventory.Snapshot) []Finding {
	// deletedZones: domain -> index of its (last) sdn.dns.zone.delete op.
	deletedZones := map[string]int{}
	for i, op := range ops {
		if op.Type == OpSdnDnsZoneDelete {
			deletedZones[op.Target.ID] = i
		}
	}
	if len(deletedZones) == 0 {
		return nil
	}

	// deletedRecords: record Ref.ID ("<zone>/<name>/<type>") this changeset
	// removes via sdn.dns.record.delete.
	deletedRecords := map[string]bool{}
	for _, op := range ops {
		if op.Type == OpSdnDnsRecordDelete {
			deletedRecords[op.Target.ID] = true
		}
	}

	// remaining: op index -> the still-present records blocking that zone's
	// delete.
	remaining := map[int][]string{}
	for _, e := range snap.All() {
		rec, ok := e.(*inventory.SdnDnsRecord)
		if !ok {
			continue
		}
		opIdx, ok := deletedZones[rec.Zone]
		if !ok {
			continue
		}
		if deletedRecords[rec.GetRef().ID] {
			continue
		}
		remaining[opIdx] = append(remaining[opIdx], rec.Name+"/"+rec.Type)
	}

	var out []Finding
	for i, op := range ops {
		hits := remaining[i]
		if len(hits) == 0 {
			continue
		}
		sort.Strings(hits)
		examples := hits
		if len(examples) > 3 {
			examples = examples[:3]
		}
		out = append(out, errorf(codeDNSZoneHasRecords, refOf(op),
			"dns zone %s still has %d record(s), for example: %s — delete them (sdn.dns.record.delete) in this changeset before deleting the zone",
			op.Target.ID, len(hits), strings.Join(examples, ", ")))
	}
	return out
}

// subnetDeletionGuardFindings flags every sdn.subnet.delete op whose target
// subnet still has one or more active IPAM allocations, per T-402's task
// card ("deletion guards: vnet with attached guests / subnet with
// allocations" — vnetDeletionGuardFindings above is the sibling; this was
// deferred at T-402 time, self-reported in that task's report, because
// internal/inventory has no dedicated IpAllocation entity kind
// (validate_projection.go's allocsBySubnet doc comment) so there was no
// data to check against from inside this package.
//
// T-405 (Visual IPAM) added exactly the missing data source in a different
// shape: AllocationsSource / SafetyOptions.Allocations, a live, cluster-wide
// read of every PVE-IPAM allocation (internal/ipam.Service.AllAllocations,
// adapted in cmd/vnproxd's dhcpAllocationsAdapter — the same production feed
// T-406's checkDHCPRangeOverlap advisory check already consumes, threaded
// into every s.validate call via Service.dhcpAllocations). "Cluster-wide"
// matters here: IPAM allocations are carved from an SDN vnet/subnet, not
// tied to any one node, and a VM holding one may be running on any peer —
// internal/ipam's PVEReader reads the whole cluster's SDN/IPAM tree per
// request, never a localhost-only view, so this guard sees allocations for
// guests on any node.
//
// Net-effect based, exactly like vnetDeletionGuardFindings and
// guestBearingBridgeFindings: an ipam.alloc.delete releasing an allocation
// elsewhere in the same changeset clears it from the count, so "release the
// last allocation, then delete the now-empty subnet" validates clean in one
// changeset. Gateway addresses never count towards the guard — the shared
// Allocations feed already excludes them (dhcpAllocationsAdapter's doc
// comment: "excluding gateway entries... it isn't a reservation at all"),
// so a subnet carrying only its configured gateway and no other
// reservations is not blocked.
//
// This is a hard block (SeverityError, downgradable only via
// AllowDangerousOps like every other finding in this class), matching
// vnetDeletionGuardFindings' severity: deleting a subnet out from under live
// allocations orphans them the same way deleting a vnet out from under
// attached guest NICs does, and the task card gives no reason to treat the
// two differently.
func subnetDeletionGuardFindings(ops []Op, allocations []DHCPRangeAllocation) []Finding {
	if len(allocations) == 0 {
		return nil
	}

	// deleteOps: subnet CIDR -> index of its (last) sdn.subnet.delete op.
	deleteOps := map[string]int{}
	for i, op := range ops {
		if op.Type == OpSdnSubnetDelete {
			deleteOps[op.Target.ID] = i
		}
	}
	if len(deleteOps) == 0 {
		return nil
	}

	// released: per subnet CIDR, the host addresses this same changeset
	// releases via ipam.alloc.delete — subtracted from the live allocation
	// count so the changeset's net effect, not just the pre-changeset
	// snapshot, decides whether the delete is blocked.
	released := map[string]map[string]bool{}
	for _, op := range ops {
		if op.Type != OpIpamAllocDelete {
			continue
		}
		params, ok := op.Params.(*IpamAllocDeleteParams)
		if !ok {
			continue
		}
		ip := hostAddrOf(params.CIDR)
		if ip == "" {
			continue
		}
		if released[op.Target.ID] == nil {
			released[op.Target.ID] = map[string]bool{}
		}
		released[op.Target.ID][ip] = true
	}

	remaining := map[int][]DHCPRangeAllocation{}
	for _, a := range allocations {
		opIdx, ok := deleteOps[a.Subnet]
		if !ok {
			continue
		}
		if released[a.Subnet][a.IP] {
			continue
		}
		remaining[opIdx] = append(remaining[opIdx], a)
	}

	var out []Finding
	for i, op := range ops {
		hits := remaining[i]
		if len(hits) == 0 {
			continue
		}
		sort.Slice(hits, func(a, b int) bool { return hits[a].IP < hits[b].IP })
		examples := hits
		if len(examples) > 3 {
			examples = examples[:3]
		}
		out = append(out, errorf(codeSubnetHasAllocations, refOf(op),
			"subnet %s still has %d active IPAM allocation(s), for example: %s — release them (ipam.alloc.delete) in this changeset before deleting the subnet",
			op.Target.ID, len(hits), describeDHCPAllocations(examples)))
	}
	return out
}

// hostAddrOf returns cidr's host address (net.ParseCIDR's first return
// value, stringified), or "" if cidr doesn't parse — IpamAllocDeleteParams.
// CIDR is schema-validated elsewhere (validate_schema.go's validCIDR) before
// this ever runs against a real changeset, so a parse failure here only
// happens when this function is exercised directly (e.g. from a test) with
// input schemaValidate would itself have already rejected.
func hostAddrOf(cidr string) string {
	ip, _, err := net.ParseCIDR(cidr)
	if err != nil {
		return ""
	}
	return ip.String()
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
// ifaceRenameGuestFindings blocks renaming a bridge/vlan that still has
// running guests attached to its old name in the changeset's net effect
// (issue #2): a rename rewrites /etc/network/interfaces but not PVE guest
// config, so guests bound by bridge=<oldName> would be orphaned. A
// guest.nic.update reattaching the NIC elsewhere (including to the new name)
// in the same changeset clears the finding — mirroring guestBearingBridge's
// net-effect treatment. AllowDangerousOps downgrades it to a warning via the
// safetyValidate wrapper, exactly like the sibling safety checks.
func ifaceRenameGuestFindings(ops []Op, snap inventory.Snapshot) []Finding {
	renames := map[ifaceKey]int{}
	for i, op := range ops {
		if op.Type == OpIfaceRename {
			renames[ifaceKey{op.Target.Node, op.Target.ID}] = i
		}
	}
	if len(renames) == 0 {
		return nil
	}

	finalAttach := map[inventory.Ref]string{}
	for _, op := range ops {
		if op.Type == OpGuestNicUpdate {
			if p, ok := op.Params.(*GuestNicUpdateParams); ok && p.BridgeOrVnet != nil {
				finalAttach[op.Target] = *p.BridgeOrVnet
			}
		}
	}

	attached := map[int][]string{}
	for _, e := range snap.All() {
		nic, ok := e.(*inventory.GuestNic)
		if !ok {
			continue
		}
		opIdx, renamed := renames[ifaceKey{nic.BridgeOrVnet.Node, nic.BridgeOrVnet.ID}]
		if !renamed {
			continue
		}
		if newAttach, updated := finalAttach[nic.GetRef()]; updated && newAttach != nic.BridgeOrVnet.ID {
			continue // reattached away (or to the new name) in this changeset
		}
		guestEntity, ok := snap.Get(nic.Guest)
		if !ok {
			continue
		}
		guest, ok := guestEntity.(*inventory.Guest)
		if !ok || guest.Status != "running" {
			continue
		}
		attached[opIdx] = append(attached[opIdx], fmt.Sprintf("%s (vmid %d, %s)", guest.Name, guest.VMID, nic.Key))
	}

	var out []Finding
	for i, op := range ops {
		list := attached[i]
		if len(list) == 0 {
			continue
		}
		sort.Strings(list)
		out = append(out, errorf(codeRenameGuestsAttached, refOf(op),
			"interface %s still has %d running guest(s) attached in this changeset's final state: %s — renaming it would orphan their network bindings (a rename does not rewrite guest bridge= config); reattach them to the new name (guest.nic.update) or detach/migrate them first",
			op.Target.ID, len(list), strings.Join(list, "; ")))
	}
	return out
}

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
