package spec

import (
	"sort"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// Export renders a Spec from a live inventory snapshot. It reads the same
// *declared* fields the Import diff compares against (Bridge.DeclaredPortNames,
// Bond.DeclaredSlaves, *.MTUDeclared, ...), so an unmodified round-trip —
// Export(live) then Import(spec, live) — yields zero ops (the reconcile
// identity that makes GitOps viable; T-1101 AC2).
//
// Only non-zero/non-empty declared fields are emitted (the omitempty
// convention blueprint's capture.go uses): an omitted field means "not
// managed by this spec", so Import never touches it on an existing entity.
// Every slice is sorted by a stable key so two Exports of identical state are
// byte-identical once marshaled (AC4); the SDN section is omitted entirely
// when the cluster has no SDN objects.
//
// Signature note (frozen for T-1102/T-1105/T-1106/T-1107): Export takes an
// inventory.Snapshot alone. SDN zones/vnets/subnets are inventory entities
// already, so no separate sdn/firewall/ipam view is needed; firewall and IPAM
// allocations are out of the v1 spec's scope (see doc.go / docs/data-model.md
// §5).
func Export(snap inventory.Snapshot) Spec {
	nodesByName := map[string]*NodeSpec{}
	nodeName := func(name string) *NodeSpec {
		n := nodesByName[name]
		if n == nil {
			n = &NodeSpec{Name: name}
			nodesByName[name] = n
		}
		return n
	}

	var sdn SDNSpec
	for _, e := range snap.All() {
		switch v := e.(type) {
		case *inventory.Bond:
			if v.Kind != inventory.KindBond {
				continue // OVS bonds are out of scope for the v1 spec
			}
			n := nodeName(v.Node)
			n.Bonds = append(n.Bonds, exportBond(v))
		case *inventory.Bridge:
			if v.Kind != inventory.KindBridge {
				continue // OVS bridges are out of scope for the v1 spec
			}
			n := nodeName(v.Node)
			n.Bridges = append(n.Bridges, exportBridge(v))
		case *inventory.VlanIface:
			if v.Kind != inventory.KindVlan || v.Virt == "ovs" {
				continue // OVS Int Ports are out of scope for the v1 spec
			}
			n := nodeName(v.Node)
			n.VLANs = append(n.VLANs, exportVLAN(v))
		case *inventory.SdnZone:
			sdn.Zones = append(sdn.Zones, exportZone(v))
		case *inventory.SdnVnet:
			sdn.Vnets = append(sdn.Vnets, exportVnet(v))
		case *inventory.SdnSubnet:
			sdn.Subnets = append(sdn.Subnets, exportSubnet(v))
		}
	}

	nodes := make([]NodeSpec, 0, len(nodesByName))
	for _, n := range nodesByName {
		sort.Slice(n.Bonds, func(i, j int) bool { return n.Bonds[i].Name < n.Bonds[j].Name })
		sort.Slice(n.Bridges, func(i, j int) bool { return n.Bridges[i].Name < n.Bridges[j].Name })
		sort.Slice(n.VLANs, func(i, j int) bool { return n.VLANs[i].Name < n.VLANs[j].Name })
		nodes = append(nodes, *n)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })

	out := Spec{SpecVersion: Version}
	if len(nodes) > 0 {
		out.Nodes = nodes
	}
	if len(sdn.Zones)+len(sdn.Vnets)+len(sdn.Subnets) > 0 {
		sort.Slice(sdn.Zones, func(i, j int) bool { return sdn.Zones[i].ID < sdn.Zones[j].ID })
		sort.Slice(sdn.Vnets, func(i, j int) bool { return sdn.Vnets[i].ID < sdn.Vnets[j].ID })
		sort.Slice(sdn.Subnets, func(i, j int) bool { return sdn.Subnets[i].ID < sdn.Subnets[j].ID })
		out.SDN = &sdn
	}
	return out
}

func exportBond(b *inventory.Bond) BondSpec {
	return BondSpec{
		Name:           b.Name,
		Mode:           b.Mode,
		Slaves:         sortedCopy(b.DeclaredSlaves),
		LACPRate:       b.LACPRate,
		XmitHashPolicy: b.XmitHashPolicy,
		MTU:            b.MTUDeclared,
	}
}

func exportBridge(b *inventory.Bridge) BridgeSpec {
	return BridgeSpec{
		Name:      b.Name,
		Ports:     sortedCopy(b.DeclaredPortNames),
		VlanAware: b.VlanAware,
		Vids:      vidStrings(b.Vids),
		Addresses: append([]string(nil), b.Addresses...),
		Gateway:   b.Gateway,
		MTU:       b.MTUDeclared,
		STP:       b.STP,
		Comments:  b.Comments,
	}
}

func exportVLAN(v *inventory.VlanIface) VLANSpec {
	return VLANSpec{
		Name:      v.Name,
		Parent:    v.ParentName,
		Vid:       v.Vid,
		Addresses: append([]string(nil), v.Addresses...),
		MTU:       v.MTUDeclared,
	}
}

func exportZone(z *inventory.SdnZone) ZoneSpec {
	return ZoneSpec{
		ID:         z.Ref.ID,
		Type:       z.Type,
		Bridge:     z.Bridge,
		Controller: z.Controller,
		IPAM:       z.IPAM,
		Nodes:      sortedCopy(z.Nodes),
		ExitNodes:  sortedCopy(z.ExitNodes),
		Peers:      sortedCopy(z.Peers),
		VrfVxlan:   z.VrfVxlan,
		MTU:        z.MTU,
	}
}

func exportVnet(v *inventory.SdnVnet) VnetSpec {
	return VnetSpec{
		ID:        v.Ref.ID,
		Zone:      v.Zone,
		Alias:     v.Alias,
		Tag:       v.Tag,
		VlanAware: v.VlanAware,
	}
}

func exportSubnet(s *inventory.SdnSubnet) SubnetSpec {
	return SubnetSpec{
		ID:            s.Ref.ID,
		Vnet:          s.Vnet,
		Gateway:       s.Gateway,
		DNSZonePrefix: s.DNSZonePrefix,
		DHCPRanges:    sortedCopy(s.DHCPRanges),
		SNAT:          s.SNAT,
	}
}

// vidStrings renders a bridge's VID ranges as sorted string forms
// ("100", "2-4094"). nil for an empty range so omitempty drops the field.
func vidStrings(vids []inventory.VidRange) []string {
	if len(vids) == 0 {
		return nil
	}
	out := make([]string, len(vids))
	for i, v := range vids {
		out[i] = v.String()
	}
	sort.Strings(out)
	return out
}

// sortedCopy returns a sorted copy of ss (order-insensitive set fields:
// ports, slaves, zone nodes, dhcp ranges), nil for empty so omitempty drops
// the field. Sorting keeps two exports of identical state byte-identical
// regardless of the order the inventory resolver happened to return.
func sortedCopy(ss []string) []string {
	if len(ss) == 0 {
		return nil
	}
	out := append([]string(nil), ss...)
	sort.Strings(out)
	return out
}
