package change

import (
	"net"
	"strings"

	"github.com/bgovanlu/vnprox/internal/fw"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// referentialValidate is validator class 2 (docs/features/
// change-management.md §2: "targets exist; no duplicate enslavement; name
// collisions; VID overlaps on a trunk; address overlaps with existing
// subnets"), evaluated against snap plus every earlier op in ops (the
// projection this function builds and folds forward one op at a time — see
// validate_projection.go's doc comment for why that ordering matters).
func referentialValidate(ops []Op, snap inventory.Snapshot) []Finding {
	proj := newProjection(snap)
	// Record which iface-namespace entities this changeset deletes (and at
	// which position), so the address-overlap and duplicate-enslavement
	// checks can ignore conflicts with an entity that is gone in the
	// changeset's net effect (audit-phase-2 F-03: without this, T-203 AC2's
	// mgmt-IP migration was rejected in create-first order).
	for i, op := range ops {
		switch op.Type {
		case OpBondDelete, OpBridgeDelete, OpVlanDelete:
			proj.pendingDelete[op.Target] = i
		}
	}
	var out []Finding
	for i, op := range ops {
		proj.cursor = i
		out = append(out, referentialValidateOp(proj, op)...)
		proj.fold(op)
	}
	return out
}

func referentialValidateOp(p *projection, op Op) []Finding {
	ref := refOf(op)
	var out []Finding

	switch params := op.Params.(type) {
	case *IfaceUpdateParams:
		if !p.exists(op.Target) {
			out = append(out, errorf(codeTargetNotFound, ref, "target %s does not exist", op.Target))
		}
		if params.Addresses != nil {
			checkAddressOverlap(p, op.Target, ref, *params.Addresses, &out)
		}

	case *IfaceRenameParams:
		if !p.exists(op.Target) {
			out = append(out, errorf(codeTargetNotFound, ref, "target %s does not exist", op.Target))
		}
		if newName := strings.TrimSpace(params.NewName); newName != "" && newName != op.Target.ID {
			if _, taken := p.ifaceRef(op.Target.Node, newName); taken {
				out = append(out, errorf(codeRenameTargetExists, ref, "an interface named %q already exists on node %s", newName, op.Target.Node))
			}
		}

	case *BondCreateParams:
		if p.exists(op.Target) {
			out = append(out, errorf(codeAlreadyExists, ref, "a bond named %q already exists", op.Target.ID))
		}
		checkSlaves(p, op.Target, ref, params.Slaves, &out)
		if op.Target.Kind == inventory.KindOVSBond && params.Bridge != "" {
			checkOVSBridgeParent(p, op.Target, params.Bridge, ref, &out)
		}

	case *BondUpdateParams:
		if !p.exists(op.Target) {
			out = append(out, errorf(codeTargetNotFound, ref, "bond %s does not exist", op.Target))
		}
		if params.Slaves != nil {
			checkSlaves(p, op.Target, ref, *params.Slaves, &out)
		}

	case *BondDeleteParams:
		if !p.exists(op.Target) {
			out = append(out, errorf(codeTargetNotFound, ref, "bond %s does not exist", op.Target))
		}

	case *BridgeCreateParams:
		if p.exists(op.Target) {
			out = append(out, errorf(codeAlreadyExists, ref, "a bridge named %q already exists", op.Target.ID))
		}
		checkPorts(p, op.Target, ref, params.Ports, &out)
		checkVIDOverlap(params.Vids, ref, &out)
		checkAddressOverlap(p, op.Target, ref, params.Addresses, &out)

	case *BridgeUpdateParams:
		if !p.exists(op.Target) {
			out = append(out, errorf(codeTargetNotFound, ref, "bridge %s does not exist", op.Target))
		}
		if params.Vids != nil {
			checkVIDOverlap(*params.Vids, ref, &out)
		}
		if params.Addresses != nil {
			checkAddressOverlap(p, op.Target, ref, *params.Addresses, &out)
		}

	case *BridgeDeleteParams:
		if !p.exists(op.Target) {
			out = append(out, errorf(codeTargetNotFound, ref, "bridge %s does not exist", op.Target))
		}

	case *BridgePortAddParams:
		if !p.exists(op.Target) {
			out = append(out, errorf(codeTargetNotFound, ref, "bridge %s does not exist", op.Target))
			break
		}
		portRef, ok := p.ifaceRef(op.Target.Node, params.Port)
		if !ok {
			out = append(out, errorf(codePortNotFound, ref, "port %q does not exist on node %s", params.Port, op.Target.Node))
			break
		}
		checkOVSKindCompat(op.Target, portRef, ref, &out)
		if owner, enslaved := p.enslaved[portRef]; enslaved && !p.deletedLater(owner) {
			out = append(out, errorf(codeDuplicateEnslavement, ref, "%s is already enslaved by %s", portRef, owner))
		}

	case *BridgePortRemoveParams:
		if !p.exists(op.Target) {
			out = append(out, errorf(codeTargetNotFound, ref, "bridge %s does not exist", op.Target))
			break
		}
		portRef, ok := p.ifaceRef(op.Target.Node, params.Port)
		if !ok {
			out = append(out, errorf(codePortNotFound, ref, "port %q does not exist on node %s", params.Port, op.Target.Node))
			break
		}
		if owner, enslaved := p.enslaved[portRef]; !enslaved || owner != op.Target {
			out = append(out, errorf(codePortNotAttached, ref, "%s is not currently a port of %s", portRef, op.Target))
		}

	case *VlanCreateParams:
		if p.exists(op.Target) {
			out = append(out, errorf(codeAlreadyExists, ref, "a vlan interface named %q already exists", op.Target.ID))
		}
		if params.OVS {
			// An OVS Int Port's parent must be an existing OVS bridge — the
			// symmetric cross-kind check to checkOVSKindCompat (T-407:
			// "mixing Linux-bridge ports into OVS bridges and vice versa").
			checkOVSBridgeParent(p, op.Target, params.Parent, ref, &out)
			checkVIDOverlap(params.Trunks, ref, &out)
		} else if pref, ok := p.ifaceRef(op.Target.Node, params.Parent); !ok {
			out = append(out, errorf(codeParentNotFound, ref, "parent %q does not exist on node %s", params.Parent, op.Target.Node))
		} else if pref.Kind == inventory.KindOVSBridge {
			out = append(out, errorf(codeOVSKindMismatch, ref, "parent %q is an OVS bridge; a plain VLAN sub-interface cannot attach to it (use ovs: true)", params.Parent))
		}
		// A vid of 0 is a legitimate "untagged/trunk-only" OVS Int Port
		// (params.Vid's doc comment); several of those may share the same
		// (node, parent) pair without conflicting, so the vlanKey duplicate
		// check — which exists to catch two *tagged* interfaces racing for
		// the same VID — only applies to a genuinely tagged vid.
		if !params.OVS || params.Vid != 0 {
			if _, dup := p.vlanIfaces[vlanKey{op.Target.Node, params.Parent, params.Vid}]; dup {
				out = append(out, errorf(codeVIDOverlap, ref, "vid %d is already in use on parent %q", params.Vid, params.Parent))
			}
		}
		checkAddressOverlap(p, op.Target, ref, params.Addresses, &out)

	case *VlanUpdateParams:
		if !p.exists(op.Target) {
			out = append(out, errorf(codeTargetNotFound, ref, "vlan interface %s does not exist", op.Target))
		}
		if params.Addresses != nil {
			checkAddressOverlap(p, op.Target, ref, *params.Addresses, &out)
		}

	case *VlanDeleteParams:
		if !p.exists(op.Target) {
			out = append(out, errorf(codeTargetNotFound, ref, "vlan interface %s does not exist", op.Target))
		}

	case *SdnZoneCreateParams:
		if p.exists(op.Target) {
			out = append(out, errorf(codeAlreadyExists, ref, "an sdn zone named %q already exists", op.Target.ID))
		}
		for _, n := range params.Nodes {
			if !p.nodeNames[n] {
				out = append(out, errorf(codeNodeNotFound, ref, "node %q is not a known cluster node", n))
			}
		}
		// ExitNodes (T-403, EVPN wizard) names real cluster nodes, exactly
		// like Nodes — Peers does not (it holds underlay IP addresses, not
		// node names; see params_sdn.go's SdnZoneCreateParams doc comment).
		for _, n := range params.ExitNodes {
			if !p.nodeNames[n] {
				out = append(out, errorf(codeNodeNotFound, ref, "node %q is not a known cluster node", n))
			}
		}

	case *SdnZoneUpdateParams:
		if !p.exists(op.Target) {
			out = append(out, errorf(codeTargetNotFound, ref, "sdn zone %s does not exist", op.Target))
		}
		if params.Nodes != nil {
			for _, n := range *params.Nodes {
				if !p.nodeNames[n] {
					out = append(out, errorf(codeNodeNotFound, ref, "node %q is not a known cluster node", n))
				}
			}
		}
		if params.ExitNodes != nil {
			for _, n := range *params.ExitNodes {
				if !p.nodeNames[n] {
					out = append(out, errorf(codeNodeNotFound, ref, "node %q is not a known cluster node", n))
				}
			}
		}

	case *SdnZoneDeleteParams:
		if !p.exists(op.Target) {
			out = append(out, errorf(codeTargetNotFound, ref, "sdn zone %s does not exist", op.Target))
		}

	case *SdnVnetCreateParams:
		if p.exists(op.Target) {
			out = append(out, errorf(codeAlreadyExists, ref, "an sdn vnet named %q already exists", op.Target.ID))
		}
		if _, ok := p.zoneNames[params.Zone]; !ok {
			out = append(out, errorf(codeZoneNotFound, ref, "zone %q does not exist", params.Zone))
		}

	case *SdnVnetUpdateParams:
		if !p.exists(op.Target) {
			out = append(out, errorf(codeTargetNotFound, ref, "sdn vnet %s does not exist", op.Target))
		}

	case *SdnVnetDeleteParams:
		if !p.exists(op.Target) {
			out = append(out, errorf(codeTargetNotFound, ref, "sdn vnet %s does not exist", op.Target))
		}

	case *SdnSubnetCreateParams:
		if p.exists(op.Target) {
			out = append(out, errorf(codeAlreadyExists, ref, "an sdn subnet %q already exists", op.Target.ID))
		}
		if _, ok := p.vnetNames[params.Vnet]; !ok {
			out = append(out, errorf(codeVnetNotFound, ref, "vnet %q does not exist", params.Vnet))
		}
		if _, ipnet, err := net.ParseCIDR(params.CIDR); err == nil {
			checkSiblingSubnetOverlap(p, op.Target, params.Vnet, ipnet, ref, &out)
		}

	case *SdnSubnetUpdateParams:
		if !p.exists(op.Target) {
			out = append(out, errorf(codeTargetNotFound, ref, "sdn subnet %s does not exist", op.Target))
		}

	case *SdnSubnetDeleteParams:
		if !p.exists(op.Target) {
			out = append(out, errorf(codeTargetNotFound, ref, "sdn subnet %s does not exist", op.Target))
		}

	case *SdnDnsZoneCreateParams:
		if p.exists(op.Target) {
			out = append(out, errorf(codeAlreadyExists, ref, "an sdn dns zone named %q already exists", op.Target.ID))
		}

	case *SdnDnsZoneUpdateParams:
		if !p.exists(op.Target) {
			out = append(out, errorf(codeTargetNotFound, ref, "sdn dns zone %s does not exist", op.Target))
		}

	case *SdnDnsZoneDeleteParams:
		if !p.exists(op.Target) {
			out = append(out, errorf(codeTargetNotFound, ref, "sdn dns zone %s does not exist", op.Target))
		}

	case *SdnDnsRecordCreateParams:
		if p.exists(op.Target) {
			out = append(out, errorf(codeAlreadyExists, ref, "an sdn dns record %q already exists", op.Target.ID))
		}
		if _, ok := p.dnsZones[params.Zone]; !ok {
			out = append(out, errorf(codeDNSZoneNotFound, ref, "dns zone %q does not exist", params.Zone))
		}

	case *SdnDnsRecordUpdateParams:
		if !p.exists(op.Target) {
			out = append(out, errorf(codeTargetNotFound, ref, "sdn dns record %s does not exist", op.Target))
		}

	case *SdnDnsRecordDeleteParams:
		if !p.exists(op.Target) {
			out = append(out, errorf(codeTargetNotFound, ref, "sdn dns record %s does not exist", op.Target))
		}

	case *SdnApplyParams:
		// no referential checks: cluster-wide, no single target.

	case *GuestNicUpdateParams:
		if _, ok := p.snap.Get(op.Target); !ok {
			out = append(out, errorf(codeTargetNotFound, ref, "guest nic %s does not exist", op.Target))
			break
		}
		if params.BridgeOrVnet != nil && !p.guestTargetExists(op.Target.Node, *params.BridgeOrVnet) {
			out = append(out, errorf(codeBridgeOrVnetNotFound, ref, "bridge or vnet %q does not exist", *params.BridgeOrVnet))
		}

	case *FwRuleCreateParams:
		checkFwPos(p, op.Target, params.Pos, true, false, ref, &out)

	case *FwRuleUpdateParams:
		checkFwPos(p, op.Target, params.Pos, false, true, ref, &out)

	case *FwRuleDeleteParams:
		checkFwPos(p, op.Target, params.Pos, false, true, ref, &out)

	case *FwRuleMoveParams:
		checkFwPos(p, op.Target, params.FromPos, false, true, ref, &out)
		checkFwPos(p, op.Target, params.ToPos, true, true, ref, &out)

	case *FwOptionsUpdateParams:
		// no existence requirement: cluster/node rulesets always exist in
		// real PVE and guest rulesets are created implicitly, so a missing
		// FwRuleset entity in the snapshot isn't evidence of a broken
		// reference the way it would be for a rule position (checkFwPos).

	case *FwAliasCreateParams:
		checkFwNameCollision(p, "alias", params.Name, ref, &out)

	case *FwIpsetCreateParams:
		checkFwNameCollision(p, "ipset", params.Name, ref, &out)

	case *FwGroupCreateParams:
		checkFwNameCollision(p, "group", params.Name, ref, &out)

	case *FwAliasUpdateParams, *FwIpsetUpdateParams, *FwGroupUpdateParams:
		// no snapshot-backed existence check possible for these yet — see
		// projection.fwNames's doc comment (known scope gap, in the T-202
		// report). T-502 closes the gap for the delete ops below, since
		// acceptance criterion 2 specifically needs delete usage-guarded;
		// extending it to update (rename/recomment) existence-checking too
		// is left for a future pass.

	case *FwAliasDeleteParams:
		checkFwObjectDeletable(p, fw.ObjectAlias, op.Target, params.Name, ref, &out)

	case *FwIpsetDeleteParams:
		checkFwObjectDeletable(p, fw.ObjectIPSet, op.Target, params.Name, ref, &out)

	case *FwGroupDeleteParams:
		checkFwObjectDeletable(p, fw.ObjectGroup, op.Target, params.Name, ref, &out)

	case *IpamAllocCreateParams:
		if !p.exists(op.Target) {
			out = append(out, errorf(codeTargetNotFound, ref, "subnet %s does not exist", op.Target))
			break
		}
		if _, subnetNet, err := net.ParseCIDR(op.Target.ID); err == nil {
			if _, allocNet, err2 := net.ParseCIDR(params.CIDR); err2 == nil && !subnetNet.Contains(allocNet.IP) {
				out = append(out, errorf(codeAddressOutOfSubnet, ref, "%s is not within subnet %s", params.CIDR, op.Target.ID))
			}
		}
		if dup := p.overlappingAlloc(op.Target, params.CIDR); dup != "" {
			out = append(out, errorf(codeAddressOverlap, ref, "%s overlaps allocation %s already created in this changeset", params.CIDR, dup))
		}

	case *IpamAllocDeleteParams:
		if !p.exists(op.Target) {
			out = append(out, errorf(codeTargetNotFound, ref, "subnet %s does not exist", op.Target))
		}
	}

	return out
}

// checkSlaves validates a bond's slave list: every named iface must exist
// on the bond's node and must not already be enslaved by a different
// owner. An owner this same changeset deletes later is no owner in the
// changeset's net effect, so it doesn't count (audit-phase-2 F-03).
func checkSlaves(p *projection, target inventory.Ref, ref string, slaves []string, out *[]Finding) {
	for _, s := range slaves {
		sref, ok := p.ifaceRef(target.Node, s)
		if !ok {
			*out = append(*out, errorf(codeSlaveNotFound, ref, "slave %q does not exist on node %s", s, target.Node))
			continue
		}
		checkOVSKindCompat(target, sref, ref, out)
		if owner, enslaved := p.enslaved[sref]; enslaved && owner != target && !p.deletedLater(owner) {
			*out = append(*out, errorf(codeDuplicateEnslavement, ref, "%s is already enslaved by %s", sref, owner))
		}
	}
}

// checkPorts is checkSlaves' bridge-port counterpart.
func checkPorts(p *projection, target inventory.Ref, ref string, ports []string, out *[]Finding) {
	for _, port := range ports {
		pref, ok := p.ifaceRef(target.Node, port)
		if !ok {
			*out = append(*out, errorf(codePortNotFound, ref, "port %q does not exist on node %s", port, target.Node))
			continue
		}
		checkOVSKindCompat(target, pref, ref, out)
		if owner, enslaved := p.enslaved[pref]; enslaved && owner != target && !p.deletedLater(owner) {
			*out = append(*out, errorf(codeDuplicateEnslavement, ref, "%s is already enslaved by %s", pref, owner))
		}
	}
}

// ovsL2Kinds / linuxL2Kinds classify the two kind-families the cross-kind
// port/slave/parent checks below reject mixing (docs/features/
// change-management.md §5: "mixing Linux-bridge ports into OVS bridges and
// vice versa -> error"). PhysNics and VLAN sub-interfaces/OVS Int Ports
// (which share inventory.KindVlan — see VlanIface.Virt's doc comment) carry
// no bridge/bond family of their own, so they are compatible with either
// side and never trip this check.
var ovsL2Kinds = map[inventory.Kind]bool{inventory.KindOVSBridge: true, inventory.KindOVSBond: true}
var linuxL2Kinds = map[inventory.Kind]bool{inventory.KindBridge: true, inventory.KindBond: true}

// checkOVSKindCompat flags attaching a Linux bridge/bond as a port/slave of
// an OVS bridge/bond, or an OVS bridge/bond as a port/slave of a Linux
// bridge/bond — the two symmetric halves of T-407's cross-kind mistake.
func checkOVSKindCompat(target, member inventory.Ref, ref string, out *[]Finding) {
	switch {
	case ovsL2Kinds[target.Kind] && linuxL2Kinds[member.Kind]:
		*out = append(*out, errorf(codeOVSKindMismatch, ref,
			"%s is a Linux %s and cannot be a port/slave of OVS %s %s", member, member.Kind, target.Kind, target))
	case linuxL2Kinds[target.Kind] && ovsL2Kinds[member.Kind]:
		*out = append(*out, errorf(codeOVSKindMismatch, ref,
			"%s is an OVS %s and cannot be a port/slave of Linux %s %s", member, member.Kind, target.Kind, target))
	}
}

// checkOVSBridgeParent validates an OVS bond's or OVS Int Port's ovs_bridge
// attachment: the named bridge must exist on target's node and be an OVS
// bridge (attaching to a Linux bridge is the other cross-kind mistake
// checkOVSKindCompat rejects the reverse direction of).
func checkOVSBridgeParent(p *projection, target inventory.Ref, bridgeName, ref string, out *[]Finding) {
	bref, ok := p.ifaceRef(target.Node, bridgeName)
	if !ok {
		*out = append(*out, errorf(codeParentNotFound, ref, "ovs bridge %q does not exist on node %s", bridgeName, target.Node))
		return
	}
	if bref.Kind != inventory.KindOVSBridge {
		*out = append(*out, errorf(codeOVSKindMismatch, ref, "%q is not an OVS bridge (kind %s)", bridgeName, bref.Kind))
	}
}

// checkVIDOverlap flags any pairwise-overlapping ranges within a single
// bridge's declared VLAN trunk list ("VID overlaps on a trunk").
func checkVIDOverlap(vids []VidRange, ref string, out *[]Finding) {
	for i := 0; i < len(vids); i++ {
		for j := i + 1; j < len(vids); j++ {
			a, b := vids[i], vids[j]
			if a.Low <= b.High && b.Low <= a.High {
				*out = append(*out, errorf(codeVIDOverlap, ref, "vid range %d-%d overlaps %d-%d", a.Low, a.High, b.Low, b.High))
			}
		}
	}
}

// checkAddressOverlap flags any address in addrs that overlaps another
// entity's already-declared address on the same node, or overlaps another
// address in addrs itself. Callers only reach this after the schema class
// has already validated CIDR syntax (the pipeline short-circuits on schema
// errors before referential runs), so parse errors here are defensive and
// silently skipped rather than re-reported.
func checkAddressOverlap(p *projection, target inventory.Ref, ref string, addrs []string, out *[]Finding) {
	parsed := make([]*net.IPNet, 0, len(addrs))
	for _, a := range addrs {
		_, ipnet, err := net.ParseCIDR(a)
		if err != nil {
			parsed = append(parsed, nil)
			continue
		}
		parsed = append(parsed, ipnet)
		if other := p.overlappingAddr(target.Node, ipnet, target); other != nil {
			*out = append(*out, errorf(codeAddressOverlap, ref, "%s overlaps an address already declared on %s", a, other.ref))
		}
	}
	for i := 0; i < len(parsed); i++ {
		if parsed[i] == nil {
			continue
		}
		for j := i + 1; j < len(parsed); j++ {
			if parsed[j] == nil {
				continue
			}
			if parsed[i].Contains(parsed[j].IP) || parsed[j].Contains(parsed[i].IP) {
				*out = append(*out, errorf(codeAddressOverlap, ref, "addresses %s and %s overlap each other", addrs[i], addrs[j]))
			}
		}
	}
}

// checkSiblingSubnetOverlap flags a new sdn.subnet.create whose CIDR
// overlaps another subnet already declared (in the snapshot or earlier in
// this changeset) under the same vnet.
func checkSiblingSubnetOverlap(p *projection, target inventory.Ref, vnet string, ipnet *net.IPNet, ref string, out *[]Finding) {
	for _, sibling := range p.subnetsByVnet[vnet] {
		if sibling == target {
			continue
		}
		_, sibNet, err := net.ParseCIDR(sibling.ID)
		if err != nil {
			continue
		}
		if sibNet.Contains(ipnet.IP) || ipnet.Contains(sibNet.IP) {
			*out = append(*out, errorf(codeAddressOverlap, ref, "subnet %s overlaps existing subnet %s", target.ID, sibling.ID))
		}
	}
}

// checkFwPos validates a rule position against ruleset target's *effective*
// rule count: the snapshot's own rule count (0 if the ruleset isn't in the
// snapshot at all) plus the net rule-count delta this same changeset's
// earlier fw.rule.create/delete ops have already made to it
// (p.fwRuleDelta — see its doc comment). This makes a changeset that
// creates rules and then reorders/updates/deletes one of them, all before
// ever applying, validate correctly even though the snapshot (a poll-cache,
// not live state) has no way to know about those not-yet-applied creates —
// acceptance criterion 1's "build 3 rules via the builder, then reorder
// one" workflow depends on exactly this.
//
// allowEnd permits pos == the effective count (an append/insert-at-end
// position, valid for fw.rule.create's Pos and fw.rule.move's ToPos);
// requireRuleset controls whether "neither the snapshot nor this
// changeset's own earlier ops have established this ruleset at all" is
// itself an error — true for update/delete/move, which reference an
// existing rule by position, false for create, whose ruleset may not be
// independently modeled yet (see the FwOptionsUpdateParams case's doc
// comment above for why cluster/node rulesets are assumed to always
// exist).
func checkFwPos(p *projection, target inventory.Ref, pos int, allowEnd, requireRuleset bool, ref string, out *[]Finding) {
	baseCount := 0
	rulesetKnown := false
	if e, ok := p.snap.Get(target); ok {
		if rs, ok := e.(*inventory.FwRuleset); ok {
			baseCount = len(rs.Rules)
			rulesetKnown = true
		}
	}
	delta := p.fwRuleDelta[target]
	if !rulesetKnown && delta == 0 {
		if requireRuleset {
			*out = append(*out, errorf(codeTargetNotFound, ref, "firewall ruleset %s does not exist", target))
		}
		return
	}
	count := baseCount + delta
	if count < 0 {
		count = 0
	}
	maxPos := count
	if !allowEnd {
		maxPos--
	}
	if pos < 0 || pos > maxPos {
		*out = append(*out, errorf(codeFwPosOutOfRange, ref, "pos %d out of range for ruleset with %d rule(s)", pos, count))
	}
}

// checkFwNameCollision flags a fw.alias/ipset/group create whose (kind,
// name) was already created earlier in this same changeset.
func checkFwNameCollision(p *projection, kind, name, ref string, out *[]Finding) {
	if p.fwNames[kind+"/"+name] {
		*out = append(*out, errorf(codeAlreadyExists, ref, "a %s named %q was already created earlier in this changeset", kind, name))
	}
}

// fwScopeOfRef recovers the FwScope an fw.alias/ipset/group op's Target
// names, from the same Ref convention params_fw.go documents (cluster:
// ID=="cluster"; node: ID=="node"; guest: ID=="guest/<kind>/<vmid>").
func fwScopeOfRef(target inventory.Ref) inventory.FwScope {
	switch target.ID {
	case "cluster":
		return inventory.FwScopeCluster
	case "node":
		return inventory.FwScopeNode
	default:
		return inventory.FwScopeGuest
	}
}

// checkFwObjectDeletable is T-502 acceptance criterion 2: deleting an
// alias/ipset/security-group still referenced by at least one rule
// anywhere it's visible from is blocked, with the exact reference count
// (the editor UI renders internal/fw.UsageCounts' own ReferencedBy list
// for the deep-links — no new usage-scanning logic needed here, per
// T-501's report). This also closes the "does this object even exist"
// gap for delete ops specifically (checkFwNameCollision only ever guarded
// create): a delete naming an object neither the live snapshot nor this
// changeset's own earlier creates ever produced is itself an error.
//
// It rebuilds a fresh fw.Snapshot from p.snap per call rather than caching
// one on the projection: fw ops are a small fraction of any realistic
// changeset (unlike e.g. per-node address checks run on every iface op),
// so the extra work is not worth the complexity of keeping a second
// cache in sync with intra-changeset fw.alias/ipset.create ops the way
// p.fwNames does for the collision check above (those never affect
// UsageCounts, which only reads *existing* rule references, so a
// same-changeset create-then-delete-of-a-referencing-rule sequence is a
// known, narrow edge this snapshot-only view won't catch — acceptable
// since AC2 only requires the live-fixture case to be caught).
func checkFwObjectDeletable(p *projection, kind fw.ObjectKind, target inventory.Ref, name, ref string, out *[]Finding) {
	snap := fw.BuildSnapshot(p.snap.All())
	scope := fwScopeOfRef(target)
	for _, u := range fw.UsageCounts(snap) {
		if u.Kind != kind || u.Scope != scope || u.Name != name {
			continue
		}
		if u.Count > 0 {
			*out = append(*out, errorf(codeFwObjectInUse, ref, "%s %q is referenced by %d rule(s) and cannot be deleted while referenced", kind, name, u.Count))
		}
		return
	}
	*out = append(*out, errorf(codeFwObjectNotFound, ref, "%s %q does not exist at this scope", kind, name))
}
