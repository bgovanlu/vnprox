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
// that owns it, for the address-overlap referential check.
type addrEntry struct {
	ipnet *net.IPNet
	ref   inventory.Ref
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
}

// newProjection seeds a projection from snap alone (before any op in the
// changeset has been folded in).
func newProjection(snap inventory.Snapshot) *projection {
	p := &projection{
		snap:           snap,
		names:          map[ifaceKey]inventory.Ref{},
		enslaved:       map[inventory.Ref]inventory.Ref{},
		vlanIfaces:     map[vlanKey]inventory.Ref{},
		addrs:          map[string][]addrEntry{},
		zoneNames:      map[string]inventory.Ref{},
		vnetNames:      map[string]inventory.Ref{},
		subnetNames:    map[string]inventory.Ref{},
		subnetsByVnet:  map[string][]inventory.Ref{},
		allocsBySubnet: map[inventory.Ref][]string{},
		nodeNames:      map[string]bool{},
		fwNames:        map[string]bool{},
	}

	for _, e := range snap.All() {
		ref := e.GetRef()
		switch v := e.(type) {
		case *inventory.Node:
			p.nodeNames[v.Name] = true
		case *inventory.PhysNic:
			p.names[ifaceKey{ref.Node, ref.ID}] = ref
		case *inventory.Bond:
			p.names[ifaceKey{ref.Node, ref.ID}] = ref
			for _, s := range firstNonEmpty(v.Slaves, v.DeclaredSlaves) {
				if sref, ok := p.names[ifaceKey{ref.Node, s}]; ok {
					p.enslaved[sref] = ref
				}
			}
		case *inventory.Bridge:
			p.names[ifaceKey{ref.Node, ref.ID}] = ref
			for _, pr := range v.Ports {
				p.enslaved[pr] = ref
			}
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
		if _, ipnet, err := net.ParseCIDR(a); err == nil {
			p.addrs[ref.Node] = append(p.addrs[ref.Node], addrEntry{ref: ref, ipnet: ipnet})
		}
	}
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
// selfRef) whose CIDR overlaps ipnet, or nil if none.
func (p *projection) overlappingAddr(node string, ipnet *net.IPNet, selfRef inventory.Ref) *addrEntry {
	for i, e := range p.addrs[node] {
		if e.ref == selfRef {
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
// delete op), clearing any enslavement it held or granted and any
// addresses it declared, so later ops in the same changeset see it as
// gone.
func (p *projection) deleteIface(ref inventory.Ref) {
	delete(p.names, ifaceKey{ref.Node, ref.ID})
	delete(p.enslaved, ref)
	for k, owner := range p.enslaved {
		if owner == ref {
			delete(p.enslaved, k)
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
		p.vlanIfaces[vlanKey{op.Target.Node, params.Parent, params.Vid}] = op.Target
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

	case *IpamAllocCreateParams:
		p.allocsBySubnet[op.Target] = append(p.allocsBySubnet[op.Target], params.CIDR)

	case *FwAliasCreateParams:
		p.fwNames["alias/"+params.Name] = true

	case *FwIpsetCreateParams:
		p.fwNames["ipset/"+params.Name] = true

	case *FwGroupCreateParams:
		p.fwNames["group/"+params.Name] = true
	}
}
