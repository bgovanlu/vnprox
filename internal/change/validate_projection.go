// SPDX-License-Identifier: Apache-2.0

package change

import (
	"net"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// ifaceKey identifies an entry in a node's flat interfaces(5) namespace:
// physnics, bonds, bridges, and VLAN sub-interfaces all share one name
// space per node (a bond and a bridge on the same node can never share a
// name), which is why ops reference them by plain string name (bond.create's
// Slaves, bridge.port.add's Port) rather than a full Ref.
type ifaceKey struct {
	node string
	name string
}

// vlanKey identifies a VLAN sub-interface by the (node, parent, vid) triple
// that determines its conventional name (ifaces.VlanName) — used to detect
// a duplicate vlan.create for the same parent+vid pair (referential.go's
// "VID overlap" check).
type vlanKey struct {
	node   string
	parent string
	vid    int
}

// addrEntry is one declared address CIDR on some node, tagged with the Ref
// that owns it, for the address-overlap referential check. hostIP is the
// address's actual host bits (net.ParseCIDR's first return value), kept
// alongside ipnet (the masked network, used for the overlap check) so
// T-203's safety-interlock class can ask "is this exact IP still assigned
// to something on this node" — ipnet.IP alone (the network address) loses
// that information for anything but a /32.
type addrEntry struct {
	ipnet  *net.IPNet
	ref    inventory.Ref
	hostIP net.IP
}

// projection is the evolving "as of this point in the ops list" view every
// referential check runs against: the base inventory.Snapshot folded
// forward through every op already processed in this changeset, in order.
// This is what lets op N reference an entity op N-1 created in the same
// changeset (T-202 acceptance criterion 2) while an op referencing
// something only a *later* op creates still correctly fails to find it —
// projectOps below walks ops in array order, checking each one against the
// projection as it stood *before* that op, then folding the op's effect in
// before moving to the next.
type projection struct {
	snap inventory.Snapshot

	// names indexes every iface-namespace entity (physnic, bond, bridge,
	// ovs-bond, ovs-bridge, vlan) known to exist "as of now" by (node,
	// name); Ref.ID is used as the canonical name (matches how every op
	// targets these kinds — see op.go's per-op doc comments).
	names map[ifaceKey]inventory.Ref

	// enslaved indexes the current owner (a bond or bridge Ref) of each
	// enslaved iface Ref, "as of now".
	enslaved map[inventory.Ref]inventory.Ref

	// vlanIfaces indexes VLAN sub-interfaces by (node, parent, vid), "as
	// of now".
	vlanIfaces map[vlanKey]inventory.Ref

	// addrs is every currently-declared address CIDR per node, "as of
	// now", for the address-overlap check.
	addrs map[string][]addrEntry

	// zoneNames/vnetNames/subnetNames index cluster-scoped SDN objects by
	// their ID (Ref.ID), "as of now".
	zoneNames   map[string]inventory.Ref
	vnetNames   map[string]inventory.Ref
	subnetNames map[string]inventory.Ref

	// subnetsByVnet indexes sdn-subnet Refs by their owning vnet ID, "as of
	// now", for the sibling-subnet CIDR-overlap check.
	subnetsByVnet map[string][]inventory.Ref

	// dnsZones/dnsRecords index cluster-scoped SDN DNS objects (T-1204) "as
	// of now": dnsZones by domain (Ref.ID), dnsRecords by the
	// "<zone>/<name>/<type>" composite Ref.ID. Seeded from the snapshot and
	// folded from sdn.dns.* create/delete ops, so a record.create can
	// reference a zone this same changeset's earlier zone.create established.
	dnsZones map[string]inventory.Ref
	// dnsPlugins indexes /cluster/sdn/dns server connections created by this
	// changeset. It is seeded empty and never from inventory: a connection
	// has no inventory entity (T-4112 — inventory's SdnDnsZone is a DNS
	// domain). See existsRef's KindSDNDnsZone case.
	dnsPlugins map[string]inventory.Ref
	dnsRecords map[string]inventory.Ref

	// controllerNames indexes cluster-scoped SDN controllers (T-3102) by id
	// (Ref.ID), "as of now" — seeded from the snapshot (a controller IS a
	// live-polled inventory entity, unlike a fabric — see KindSDNController's
	// doc comment) and folded from sdn.controller.create/delete ops, the
	// same "as of now" discipline zoneNames/vnetNames use.
	controllerNames map[string]inventory.Ref

	// ipamNames indexes cluster-scoped SDN ipam plugin instances (T-3104) by
	// id (Ref.ID), "as of now" — seeded from the snapshot (an ipam instance
	// IS a live-polled inventory entity, unlike a fabric — see KindSDNIpam's
	// doc comment, the same reasoning controllerNames' doc comment gives for
	// Controller) and folded from sdn.ipam.create/delete ops, the same "as
	// of now" discipline controllerNames uses.
	ipamNames map[string]inventory.Ref

	// allocsBySubnet tracks ipam.alloc.create CIDRs created earlier in
	// this same changeset, keyed by the owning subnet Ref. IpAllocation
	// has no dedicated inventory.Kind (docs/data-model.md's ER diagram
	// models it, but internal/inventory's entity list does not), so only
	// intra-changeset sibling-overlap detection is possible.
	allocsBySubnet map[inventory.Ref][]string

	// nodeNames is every known cluster node hostname, from the snapshot
	// only (ops never create/delete Node entities).
	nodeNames map[string]bool

	// fwNames tracks fw.alias/ipset/group (kind, name) pairs created
	// earlier in this same changeset. Aliases/ipsets/groups have no
	// dedicated inventory.Kind (see params_fw.go's doc comment), so only
	// intra-changeset duplicate-create detection is possible — there is
	// no snapshot-backed existence check for their update/delete ops.
	fwNames map[string]bool

	// fwRuleDelta tracks the net rule-count change fw.rule.create/delete
	// ops earlier in this same changeset have made to each ruleset target
	// (T-502), so checkFwPos's position-bounds check reflects the
	// changeset's own net effect, not only the (possibly several-seconds-
	// stale) snapshot's rule count. Acceptance criterion 1's "build 3
	// rules via the builder, then reorder one" workflow creates and moves
	// rules within the very same changeset — without this, a fw.rule.move
	// referencing a position only this changeset's own earlier creates
	// established would always fail validation.
	fwRuleDelta map[inventory.Ref]int

	// pendingDelete maps every iface-namespace Ref that some delete op in
	// this changeset targets to the index of its *last* delete op, and
	// cursor is the index of the op currently being checked (maintained by
	// referentialValidate's walk). Together they make the address-overlap
	// and duplicate-enslavement checks net-effect-aware (audit-phase-2
	// F-03): a conflict with an entity this same changeset deletes *later*
	// is no conflict in the changeset's net effect, so e.g. the AC2
	// mgmt-IP-migration chain validates clean in create-first order too.
	// A doomed entity that is re-created after its delete re-enters the
	// projection via fold, so conflicts with the re-created entity are
	// still caught (the recreate op's index is past the delete's).
	// Both fields are zero for projections built outside referentialValidate
	// (e.g. safetyValidate's final-state fold), where no suppression applies.
	pendingDelete map[inventory.Ref]int
	cursor        int
}

// deletedLater reports whether ref is deleted by an op *after* the one
// currently being checked (see pendingDelete's doc comment).
func (p *projection) deletedLater(ref inventory.Ref) bool {
	idx, ok := p.pendingDelete[ref]
	return ok && idx > p.cursor
}

// newProjection seeds a projection from snap alone (before any op in the
// changeset has been folded in).
func newProjection(snap inventory.Snapshot) *projection {
	p := &projection{
		snap:            snap,
		names:           map[ifaceKey]inventory.Ref{},
		enslaved:        map[inventory.Ref]inventory.Ref{},
		vlanIfaces:      map[vlanKey]inventory.Ref{},
		addrs:           map[string][]addrEntry{},
		zoneNames:       map[string]inventory.Ref{},
		vnetNames:       map[string]inventory.Ref{},
		subnetNames:     map[string]inventory.Ref{},
		subnetsByVnet:   map[string][]inventory.Ref{},
		dnsZones:        map[string]inventory.Ref{},
		dnsPlugins:      map[string]inventory.Ref{},
		dnsRecords:      map[string]inventory.Ref{},
		controllerNames: map[string]inventory.Ref{},
		ipamNames:       map[string]inventory.Ref{},
		allocsBySubnet:  map[inventory.Ref][]string{},
		nodeNames:       map[string]bool{},
		fwNames:         map[string]bool{},
		fwRuleDelta:     map[inventory.Ref]int{},
		pendingDelete:   map[inventory.Ref]int{},
	}

	// Bond slaves are plain *names* that must be resolved through p.names,
	// but snap.All() sorts by Ref.String() — "bond:…" sorts before
	// "physnic:…" — so slave resolution cannot happen in the same pass that
	// builds the name index (audit-phase-2 F-04: a single pass left the
	// enslavement index empty for every snapshot bond, and duplicate
	// enslavement never fired against pre-existing bonds). Index every name
	// first, then resolve bond slaves in a second pass. Bridge ports are
	// already resolved Refs, so they need no second pass, but they are moved
	// there anyway for uniformity.
	var bonds []*inventory.Bond
	var bridges []*inventory.Bridge
	for _, e := range snap.All() {
		ref := e.GetRef()
		switch v := e.(type) {
		case *inventory.Node:
			p.nodeNames[v.Name] = true
		case *inventory.PhysNic:
			p.names[ifaceKey{ref.Node, ref.ID}] = ref
		case *inventory.Bond:
			p.names[ifaceKey{ref.Node, ref.ID}] = ref
			bonds = append(bonds, v)
		case *inventory.Bridge:
			p.names[ifaceKey{ref.Node, ref.ID}] = ref
			bridges = append(bridges, v)
			p.addAddrs(ref, v.Addresses)
		case *inventory.VlanIface:
			p.names[ifaceKey{ref.Node, ref.ID}] = ref
			p.vlanIfaces[vlanKey{ref.Node, v.ParentName, v.Vid}] = ref
			p.addAddrs(ref, v.Addresses)
		case *inventory.SdnZone:
			p.zoneNames[v.ID] = ref
		case *inventory.SdnVnet:
			p.vnetNames[v.ID] = ref
		case *inventory.SdnSubnet:
			p.subnetNames[v.ID] = ref
			p.subnetsByVnet[v.Vnet] = append(p.subnetsByVnet[v.Vnet], ref)
		case *inventory.SdnDnsZone:
			p.dnsZones[v.ID] = ref
		case *inventory.SdnDnsRecord:
			p.dnsRecords[ref.ID] = ref
		case *inventory.SdnController:
			p.controllerNames[v.ID] = ref
		case *inventory.SdnIpam:
			p.ipamNames[v.ID] = ref
		}
	}

	// Second pass: every name is indexed now, so slave/port membership can
	// resolve regardless of snap.All()'s iteration order.
	for _, b := range bonds {
		ref := b.GetRef()
		for _, s := range firstNonEmpty(b.Slaves, b.DeclaredSlaves) {
			if sref, ok := p.names[ifaceKey{ref.Node, s}]; ok {
				p.enslaved[sref] = ref
			}
		}
	}
	for _, b := range bridges {
		for _, pr := range b.Ports {
			p.enslaved[pr] = b.GetRef()
		}
	}
	return p
}

func firstNonEmpty(a, b []string) []string {
	if len(a) > 0 {
		return a
	}
	return b
}

func (p *projection) addAddrs(ref inventory.Ref, addrs []string) {
	for _, a := range addrs {
		if ip, ipnet, err := net.ParseCIDR(a); err == nil {
			p.addrs[ref.Node] = append(p.addrs[ref.Node], addrEntry{ref: ref, ipnet: ipnet, hostIP: ip})
		}
	}
}

// hasHostIP reports whether ip is currently assigned (as of this point in
// the walk) to any entity on node — used by the safety-interlock class to
// check whether a protected address is still reachable via *some*
// interface, regardless of whether the specific entity that originally
// carried it still exists (docs/features/change-management.md §2 class 3's
// "chain analysis": moving an address to a new bridge and deleting the old
// one in the same changeset must validate clean).
func (p *projection) hasHostIP(node string, ip net.IP) bool {
	for _, e := range p.addrs[node] {
		if e.hostIP.Equal(ip) {
			return true
		}
	}
	return false
}

// hasHostIPOnRef is hasHostIP narrowed to entries still owned by ref
// specifically (used by the bridge-path-detachment check, which only cares
// whether the *original* protected bridge itself still carries the
// address, not whether it moved elsewhere).
func (p *projection) hasHostIPOnRef(ref inventory.Ref, ip net.IP) bool {
	for _, e := range p.addrs[ref.Node] {
		if e.ref == ref && e.hostIP.Equal(ip) {
			return true
		}
	}
	return false
}

// portCount returns how many entries in p.enslaved are currently owned by
// owner — the number of ports/slaves owner has "as of now".
func (p *projection) portCount(owner inventory.Ref) int {
	n := 0
	for _, o := range p.enslaved {
		if o == owner {
			n++
		}
	}
	return n
}

// removeAddrsOf drops every addrEntry owned by ref (used before folding in
// an update/delete that replaces or removes ref's address list).
func (p *projection) removeAddrsOf(node string, ref inventory.Ref) {
	kept := p.addrs[node][:0]
	for _, e := range p.addrs[node] {
		if e.ref != ref {
			kept = append(kept, e)
		}
	}
	p.addrs[node] = kept
}

// overlappingAddr returns the first existing addrEntry on node (other than
// selfRef) whose CIDR overlaps ipnet, or nil if none. Entries owned by an
// entity this changeset deletes later than the op currently being checked
// are skipped — no overlap survives in the changeset's net effect (see
// pendingDelete's doc comment; audit-phase-2 F-03).
func (p *projection) overlappingAddr(node string, ipnet *net.IPNet, selfRef inventory.Ref) *addrEntry {
	for i, e := range p.addrs[node] {
		if e.ref == selfRef || p.deletedLater(e.ref) {
			continue
		}
		if e.ipnet.Contains(ipnet.IP) || ipnet.Contains(e.ipnet.IP) {
			return &p.addrs[node][i]
		}
	}
	return nil
}

// ifaceRef resolves a plain interface name within node's flat namespace, as
// of this point in the walk.
func (p *projection) ifaceRef(node, name string) (inventory.Ref, bool) {
	r, ok := p.names[ifaceKey{node, name}]
	return r, ok
}

// exists reports whether ref is known to exist "as of now", dispatching to
// the right index for ref.Kind — the iface-namespace kinds (looked up by
// (node, ID)), the cluster-scoped SDN kinds (looked up by ID alone), or,
// for every other kind (guest, guest-nic, fw-ruleset, node, lldp-neighbor —
// none of which any op in the v1 vocabulary creates or deletes), a direct
// snapshot lookup.
func (p *projection) exists(ref inventory.Ref) bool {
	switch ref.Kind {
	case inventory.KindPhysNic, inventory.KindBond, inventory.KindOVSBond,
		inventory.KindBridge, inventory.KindOVSBridge, inventory.KindVlan:
		_, ok := p.names[ifaceKey{ref.Node, ref.ID}]
		return ok
	case inventory.KindSDNZone:
		_, ok := p.zoneNames[ref.ID]
		return ok
	case inventory.KindSDNVnet:
		_, ok := p.vnetNames[ref.ID]
		return ok
	case inventory.KindSDNSubnet:
		_, ok := p.subnetNames[ref.ID]
		return ok
	case inventory.KindSDNDnsZone:
		// Two different things share this kind (T-4112, filed as T-4114).
		// dnsZones indexes DNS DOMAINS, which is what inventory holds and
		// what a record op's Zone refers to. dnsPlugins indexes the
		// /cluster/sdn/dns server CONNECTIONS that sdn.dns.zone.* actually
		// manages, which have no inventory entity at all — so a connection
		// is only known here if this changeset created it.
		//
		// Existence accepts either, because both are legitimately addressed
		// through this kind today. The record check below deliberately does
		// NOT: it asks dnsZones alone, so a connection id can never satisfy
		// "does this record's zone exist".
		if _, ok := p.dnsZones[ref.ID]; ok {
			return true
		}
		_, ok := p.dnsPlugins[ref.ID]
		return ok
	case inventory.KindSDNDnsRecord:
		_, ok := p.dnsRecords[ref.ID]
		return ok
	case inventory.KindSDNController:
		_, ok := p.controllerNames[ref.ID]
		return ok
	case inventory.KindSDNIpam:
		_, ok := p.ipamNames[ref.ID]
		return ok
	default:
		_, ok := p.snap.Get(ref)
		return ok
	}
}

// guestTargetExists reports whether name resolves to an existing plain
// bridge on node or a cluster-scoped sdn-vnet — the two things a guest
// NIC's bridgeOrVnet may name (mirrors internal/inventory/link.go's
// resolveGuestNic preference order: a same-node plain bridge wins over a
// same-named vnet).
func (p *projection) guestTargetExists(node, name string) bool {
	if ref, ok := p.ifaceRef(node, name); ok && (ref.Kind == inventory.KindBridge || ref.Kind == inventory.KindOVSBridge) {
		return true
	}
	_, ok := p.vnetNames[name]
	return ok
}

// overlappingAlloc returns the CIDR of a sibling ipam.alloc.create already
// folded into subnet's allocation list that overlaps cidr, or "" if none.
func (p *projection) overlappingAlloc(subnet inventory.Ref, cidr string) string {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return ""
	}
	for _, existing := range p.allocsBySubnet[subnet] {
		_, exNet, err2 := net.ParseCIDR(existing)
		if err2 != nil {
			continue
		}
		if exNet.Contains(ipnet.IP) || ipnet.Contains(exNet.IP) {
			return existing
		}
	}
	return ""
}

// enslaveAll marks every name in names, resolved within owner's node, as
// enslaved by owner.
func (p *projection) enslaveAll(owner inventory.Ref, names []string) {
	for _, n := range names {
		if ref, ok := p.ifaceRef(owner.Node, n); ok {
			p.enslaved[ref] = owner
		}
	}
}

// deleteIface removes ref from the iface namespace (a bond/bridge/vlan
// delete op), clearing any enslavement it held or granted, any addresses
// it declared, and any (node, parent, vid) VLAN index entry it occupied,
// so later ops in the same changeset see it as gone. The vlanIfaces sweep
// is what lets a delete-then-recreate draft for the same parent+vid pass
// (audit-phase-2 F-05: the stale index caused a false, apply-blocking
// vid_overlap on recreate).
func (p *projection) deleteIface(ref inventory.Ref) {
	delete(p.names, ifaceKey{ref.Node, ref.ID})
	delete(p.enslaved, ref)
	for k, owner := range p.enslaved {
		if owner == ref {
			delete(p.enslaved, k)
		}
	}
	for k, v := range p.vlanIfaces {
		if v == ref {
			delete(p.vlanIfaces, k)
		}
	}
	p.removeAddrsOf(ref.Node, ref)
}

// fold applies op's effect to the projection so subsequent ops in the same
// changeset see its result — this is what makes intra-changeset ordering
// work (T-202 acceptance criterion 2). It is called unconditionally after
// each op's referential checks run, even if those checks found an error:
// the user's draft still expresses the intent that the entity ends up
// existing, so a later op referencing it should not cascade a second,
// redundant "not found" finding on top of the first op's own error.
func (p *projection) fold(op Op) {
	switch params := op.Params.(type) {
	case *IfaceUpdateParams:
		if params.Addresses != nil {
			p.removeAddrsOf(op.Target.Node, op.Target)
			p.addAddrs(op.Target, *params.Addresses)
		} else if params.RemoveAddress {
			// T-703: an explicit "clear the address" (ifaces.IfaceUpdate's
			// RemoveAddress semantics) must project the same way an empty
			// replacement list does, or the safety class would keep seeing
			// the removed address as surviving.
			p.removeAddrsOf(op.Target.Node, op.Target)
		}

	case *BondCreateParams:
		p.names[ifaceKey{op.Target.Node, op.Target.ID}] = op.Target
		p.enslaveAll(op.Target, params.Slaves)

	case *BondUpdateParams:
		if params.Slaves != nil {
			p.enslaveAll(op.Target, *params.Slaves)
		}

	case *BondDeleteParams:
		p.deleteIface(op.Target)

	case *BridgeCreateParams:
		p.names[ifaceKey{op.Target.Node, op.Target.ID}] = op.Target
		p.enslaveAll(op.Target, params.Ports)
		p.addAddrs(op.Target, params.Addresses)

	case *BridgeUpdateParams:
		if params.Addresses != nil {
			p.removeAddrsOf(op.Target.Node, op.Target)
			p.addAddrs(op.Target, *params.Addresses)
		}

	case *BridgeDeleteParams:
		p.deleteIface(op.Target)

	case *BridgePortAddParams:
		if pref, ok := p.ifaceRef(op.Target.Node, params.Port); ok {
			p.enslaved[pref] = op.Target
		}

	case *BridgePortRemoveParams:
		if pref, ok := p.ifaceRef(op.Target.Node, params.Port); ok {
			delete(p.enslaved, pref)
		}

	case *VlanCreateParams:
		p.names[ifaceKey{op.Target.Node, op.Target.ID}] = op.Target
		// See referentialValidateOp's VlanCreateParams case: an untagged/
		// trunk-only OVS Int Port (OVS && Vid == 0) does not occupy the
		// (node, parent, vid) slot the way a tagged VLAN/Int Port does, so
		// several may coexist on the same parent without folding over one
		// another here.
		if !params.OVS || params.Vid != 0 {
			p.vlanIfaces[vlanKey{op.Target.Node, params.Parent, params.Vid}] = op.Target
		}
		p.addAddrs(op.Target, params.Addresses)

	case *VlanUpdateParams:
		if params.Addresses != nil {
			p.removeAddrsOf(op.Target.Node, op.Target)
			p.addAddrs(op.Target, *params.Addresses)
		}

	case *VlanDeleteParams:
		p.deleteIface(op.Target)

	case *SdnZoneCreateParams:
		p.zoneNames[op.Target.ID] = op.Target

	case *SdnZoneDeleteParams:
		delete(p.zoneNames, op.Target.ID)

	case *SdnVnetCreateParams:
		p.vnetNames[op.Target.ID] = op.Target

	case *SdnVnetDeleteParams:
		delete(p.vnetNames, op.Target.ID)

	case *SdnSubnetCreateParams:
		p.subnetNames[op.Target.ID] = op.Target
		p.subnetsByVnet[params.Vnet] = append(p.subnetsByVnet[params.Vnet], op.Target)

	case *SdnSubnetDeleteParams:
		delete(p.subnetNames, op.Target.ID)
		// Also drop the subnet from the per-vnet sibling index — a stale
		// entry there caused false address_overlap findings on
		// delete-then-recreate drafts (audit-phase-2 F-05). The delete op
		// doesn't carry the owning vnet, so sweep every list.
		for vnet, refs := range p.subnetsByVnet {
			kept := refs[:0]
			for _, r := range refs {
				if r != op.Target {
					kept = append(kept, r)
				}
			}
			p.subnetsByVnet[vnet] = kept
		}

	case *SdnDnsZoneCreateParams:
		// Into dnsPlugins, not dnsZones: this op creates a PowerDNS server
		// connection. Putting it in dnsZones fabricated a DNS domain named
		// after the connection, which the record check below would then
		// accept as a record's zone — a changeset that validated clean and
		// wrote records to a domain that does not exist.
		p.dnsPlugins[op.Target.ID] = op.Target

	case *SdnDnsZoneDeleteParams:
		delete(p.dnsPlugins, op.Target.ID)

	case *SdnDnsRecordCreateParams:
		p.dnsRecords[op.Target.ID] = op.Target

	case *SdnDnsRecordDeleteParams:
		delete(p.dnsRecords, op.Target.ID)

	case *SdnControllerCreateParams:
		p.controllerNames[op.Target.ID] = op.Target

	case *SdnControllerDeleteParams:
		delete(p.controllerNames, op.Target.ID)

	case *SdnIpamCreateParams:
		p.ipamNames[op.Target.ID] = op.Target

	case *SdnIpamDeleteParams:
		delete(p.ipamNames, op.Target.ID)

	case *IpamAllocCreateParams:
		p.allocsBySubnet[op.Target] = append(p.allocsBySubnet[op.Target], params.CIDR)

	case *FwRuleCreateParams:
		p.fwRuleDelta[op.Target]++

	case *FwRuleDeleteParams:
		p.fwRuleDelta[op.Target]--

	case *FwAliasCreateParams:
		p.fwNames["alias/"+params.Name] = true

	case *FwIpsetCreateParams:
		p.fwNames["ipset/"+params.Name] = true

	case *FwGroupCreateParams:
		p.fwNames["group/"+params.Name] = true
	}
}
