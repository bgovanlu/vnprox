// adapters.go implements per-Kind create/diff logic: given one
// expandedEntity's substituted Fields and the live inventory.Snapshot, it
// returns the []change.Op that converges the live state to the blueprint's
// desired state — nil/empty when the entity already matches (T-603 AC1's
// idempotency: "matching entities skipped"), a single create op when
// absent, and one or more update ops (only for the fields that actually
// differ, per kind — a bridge's port membership is expressed as
// bridge.port.add/remove rather than bridge.update, mirroring
// BridgeUpdateParams' own doc comment on why port membership isn't one of
// its fields) when present-but-divergent (AC2).
//
// Only fields actually present in the template's Fields map (after
// substitution) are ever considered "desired" — an omitted field is never
// touched on an existing entity and takes the OS/PVE/change-engine default
// on a newly created one, the same "omitempty = don't manage this" the
// rest of this codebase's *CreateParams/*UpdateParams types already use.

package blueprint

import (
	"fmt"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// diffEntity dispatches e to its kind's adapter.
func diffEntity(e expandedEntity, snap inventory.Snapshot) ([]change.Op, error) {
	switch e.kind {
	case KindBridge:
		return diffBridge(e.ref, e.fields, snap)
	case KindBond:
		return diffBond(e.ref, e.fields, snap)
	case KindVlan:
		return diffVlan(e.ref, e.fields, snap)
	case KindSdnZone:
		return diffSdnZone(e.ref, e.fields, snap)
	case KindSdnVnet:
		return diffSdnVnet(e.ref, e.fields, snap)
	case KindSdnSubnet:
		return diffSdnSubnet(e.ref, e.fields, snap)
	default:
		return nil, fmt.Errorf("unknown entity kind %q", e.kind)
	}
}

func inventoryVidsToChange(vids []inventory.VidRange) []change.VidRange {
	out := make([]change.VidRange, len(vids))
	for i, v := range vids {
		out[i] = change.VidRange{Low: v.Low, High: v.High}
	}
	return out
}

// --- bridge ---------------------------------------------------------------

func diffBridge(ref inventory.Ref, fields map[string]any, snap inventory.Snapshot) ([]change.Op, error) {
	gateway, hasGateway, err := fieldString(fields, "gateway")
	if err != nil {
		return nil, err
	}
	comments, hasComments, err := fieldString(fields, "comments")
	if err != nil {
		return nil, err
	}
	ports, hasPorts, err := fieldStringSlice(fields, "ports")
	if err != nil {
		return nil, err
	}
	vids, hasVids, err := fieldVids(fields, "vids")
	if err != nil {
		return nil, err
	}
	addresses, hasAddresses, err := fieldStringSlice(fields, "addresses")
	if err != nil {
		return nil, err
	}
	mtu, hasMTU, err := fieldInt(fields, "mtu")
	if err != nil {
		return nil, err
	}
	vlanAware, hasVlanAware, err := fieldBool(fields, "vlanAware")
	if err != nil {
		return nil, err
	}
	stp, hasSTP, err := fieldBool(fields, "stp")
	if err != nil {
		return nil, err
	}

	existing, found := snap.Get(ref)
	if !found {
		create := &change.BridgeCreateParams{}
		if hasGateway {
			create.Gateway = gateway
		}
		if hasComments {
			create.Comments = comments
		}
		if hasPorts {
			create.Ports = ports
		}
		if hasVids {
			create.Vids = vids
		}
		if hasAddresses {
			create.Addresses = addresses
		}
		if hasMTU {
			create.MTU = mtu
		}
		if hasVlanAware {
			create.VlanAware = vlanAware
		}
		if hasSTP {
			create.STP = stp
		}
		return []change.Op{{Type: change.OpBridgeCreate, Target: ref, Params: create}}, nil
	}

	br, ok := existing.(*inventory.Bridge)
	if !ok {
		return nil, fmt.Errorf("%s already exists but is not a bridge", ref)
	}

	var ops []change.Op
	upd := &change.BridgeUpdateParams{}
	changed := false
	if hasGateway && gateway != br.Gateway {
		g := gateway
		upd.Gateway = &g
		changed = true
	}
	if hasComments && comments != br.Comments {
		c := comments
		upd.Comments = &c
		changed = true
	}
	if hasVids && !vidsEqual(vids, inventoryVidsToChange(br.Vids)) {
		v := vids
		upd.Vids = &v
		changed = true
	}
	if hasAddresses && !setEqual(addresses, br.Addresses) {
		a := addresses
		upd.Addresses = &a
		changed = true
	}
	if hasMTU && mtu != br.MTUDeclared {
		m := mtu
		upd.MTU = &m
		changed = true
	}
	if hasVlanAware && vlanAware != br.VlanAware {
		v := vlanAware
		upd.VlanAware = &v
		changed = true
	}
	if hasSTP && stp != br.STP {
		s := stp
		upd.STP = &s
		changed = true
	}
	if changed {
		ops = append(ops, change.Op{Type: change.OpBridgeUpdate, Target: ref, Params: upd})
	}
	if hasPorts {
		for _, add := range missingFrom(ports, br.DeclaredPortNames) {
			ops = append(ops, change.Op{Type: change.OpBridgePortAdd, Target: ref, Params: &change.BridgePortAddParams{Port: add}})
		}
		for _, rm := range missingFrom(br.DeclaredPortNames, ports) {
			ops = append(ops, change.Op{Type: change.OpBridgePortRemove, Target: ref, Params: &change.BridgePortRemoveParams{Port: rm}})
		}
	}
	return ops, nil
}

// --- bond -------------------------------------------------------------

func diffBond(ref inventory.Ref, fields map[string]any, snap inventory.Snapshot) ([]change.Op, error) {
	mode, hasMode, err := fieldString(fields, "mode")
	if err != nil {
		return nil, err
	}
	lacpRate, hasLACPRate, err := fieldString(fields, "lacpRate")
	if err != nil {
		return nil, err
	}
	xmitHashPolicy, hasXHP, err := fieldString(fields, "xmitHashPolicy")
	if err != nil {
		return nil, err
	}
	comments, hasComments, err := fieldString(fields, "comments")
	if err != nil {
		return nil, err
	}
	slaves, hasSlaves, err := fieldStringSlice(fields, "slaves")
	if err != nil {
		return nil, err
	}
	miimon, hasMIIMon, err := fieldInt(fields, "miimon")
	if err != nil {
		return nil, err
	}
	mtu, hasMTU, err := fieldInt(fields, "mtu")
	if err != nil {
		return nil, err
	}

	existing, found := snap.Get(ref)
	if !found {
		create := &change.BondCreateParams{}
		if hasMode {
			create.Mode = mode
		}
		if hasLACPRate {
			create.LACPRate = lacpRate
		}
		if hasXHP {
			create.XmitHashPolicy = xmitHashPolicy
		}
		if hasComments {
			create.Comments = comments
		}
		if hasSlaves {
			create.Slaves = slaves
		}
		if hasMIIMon {
			create.MIIMon = miimon
		}
		if hasMTU {
			create.MTU = mtu
		}
		return []change.Op{{Type: change.OpBondCreate, Target: ref, Params: create}}, nil
	}

	bd, ok := existing.(*inventory.Bond)
	if !ok {
		return nil, fmt.Errorf("%s already exists but is not a bond", ref)
	}

	upd := &change.BondUpdateParams{}
	changed := false
	if hasMode && mode != bd.Mode {
		m := mode
		upd.Mode = &m
		changed = true
	}
	if hasSlaves && !setEqual(slaves, bd.DeclaredSlaves) {
		s := slaves
		upd.Slaves = &s
		changed = true
	}
	if hasLACPRate && lacpRate != bd.LACPRate {
		l := lacpRate
		upd.LACPRate = &l
		changed = true
	}
	if hasXHP && xmitHashPolicy != bd.XmitHashPolicy {
		x := xmitHashPolicy
		upd.XmitHashPolicy = &x
		changed = true
	}
	if hasMTU && mtu != bd.MTUDeclared {
		m := mtu
		upd.MTU = &m
		changed = true
	}
	// Comments/MIIMon are not tracked by inventory.Bond (no observed
	// baseline exists to diff against — see the T-603 report), so they
	// are never compared here; they still apply on create above.
	if !changed {
		return nil, nil
	}
	return []change.Op{{Type: change.OpBondUpdate, Target: ref, Params: upd}}, nil
}

// --- vlan ---------------------------------------------------------------

func diffVlan(ref inventory.Ref, fields map[string]any, snap inventory.Snapshot) ([]change.Op, error) {
	parent, _, err := fieldString(fields, "parent")
	if err != nil {
		return nil, err
	}
	vid, _, err := fieldInt(fields, "vid")
	if err != nil {
		return nil, err
	}
	addresses, hasAddresses, err := fieldStringSlice(fields, "addresses")
	if err != nil {
		return nil, err
	}
	mtu, hasMTU, err := fieldInt(fields, "mtu")
	if err != nil {
		return nil, err
	}

	existing, found := snap.Get(ref)
	if !found {
		create := &change.VlanCreateParams{Parent: parent, Vid: vid}
		if hasAddresses {
			create.Addresses = addresses
		}
		if hasMTU {
			create.MTU = mtu
		}
		return []change.Op{{Type: change.OpVlanCreate, Target: ref, Params: create}}, nil
	}

	vl, ok := existing.(*inventory.VlanIface)
	if !ok {
		return nil, fmt.Errorf("%s already exists but is not a vlan interface", ref)
	}

	// Parent/Vid are not editable on an existing vlan interface (see
	// VlanUpdateParams' doc comment: re-parenting/re-tagging is a
	// delete+create) — a template that disagrees with the live parent/vid
	// for this same ref id has no update path; only addresses/mtu diff.
	upd := &change.VlanUpdateParams{}
	changed := false
	if hasAddresses && !setEqual(addresses, vl.Addresses) {
		a := addresses
		upd.Addresses = &a
		changed = true
	}
	if hasMTU && mtu != vl.MTUDeclared {
		m := mtu
		upd.MTU = &m
		changed = true
	}
	if !changed {
		return nil, nil
	}
	return []change.Op{{Type: change.OpVlanUpdate, Target: ref, Params: upd}}, nil
}

// --- sdn zone -------------------------------------------------------------

func diffSdnZone(ref inventory.Ref, fields map[string]any, snap inventory.Snapshot) ([]change.Op, error) {
	zoneType, _, err := fieldString(fields, "type")
	if err != nil {
		return nil, err
	}
	bridge, hasBridge, err := fieldString(fields, "bridge")
	if err != nil {
		return nil, err
	}
	controller, hasController, err := fieldString(fields, "controller")
	if err != nil {
		return nil, err
	}
	ipam, hasIPAM, err := fieldString(fields, "ipam")
	if err != nil {
		return nil, err
	}
	nodes, hasNodes, err := fieldStringSlice(fields, "nodes")
	if err != nil {
		return nil, err
	}
	vrfVxlan, hasVrfVxlan, err := fieldInt(fields, "vrfVxlan")
	if err != nil {
		return nil, err
	}
	mtu, hasMTU, err := fieldInt(fields, "mtu")
	if err != nil {
		return nil, err
	}

	existing, found := snap.Get(ref)
	if !found {
		create := &change.SdnZoneCreateParams{Type: zoneType}
		if hasBridge {
			create.Bridge = bridge
		}
		if hasController {
			create.Controller = controller
		}
		if hasIPAM {
			create.IPAM = ipam
		}
		if hasNodes {
			create.Nodes = nodes
		}
		if hasVrfVxlan {
			create.VrfVxlan = vrfVxlan
		}
		if hasMTU {
			create.MTU = mtu
		}
		return []change.Op{{Type: change.OpSdnZoneCreate, Target: ref, Params: create}}, nil
	}

	z, ok := existing.(*inventory.SdnZone)
	if !ok {
		return nil, fmt.Errorf("%s already exists but is not an sdn zone", ref)
	}

	// Type is not editable (SdnZoneUpdateParams has no Type field; see its
	// doc comment: changing zone type is a delete+create in real PVE too).
	upd := &change.SdnZoneUpdateParams{}
	changed := false
	if hasBridge && bridge != z.Bridge {
		b := bridge
		upd.Bridge = &b
		changed = true
	}
	if hasController && controller != z.Controller {
		c := controller
		upd.Controller = &c
		changed = true
	}
	if hasIPAM && ipam != z.IPAM {
		i := ipam
		upd.IPAM = &i
		changed = true
	}
	if hasNodes && !setEqual(nodes, z.Nodes) {
		n := nodes
		upd.Nodes = &n
		changed = true
	}
	if hasVrfVxlan && vrfVxlan != z.VrfVxlan {
		v := vrfVxlan
		upd.VrfVxlan = &v
		changed = true
	}
	if hasMTU && mtu != z.MTU {
		m := mtu
		upd.MTU = &m
		changed = true
	}
	if !changed {
		return nil, nil
	}
	return []change.Op{{Type: change.OpSdnZoneUpdate, Target: ref, Params: upd}}, nil
}

// --- sdn vnet -------------------------------------------------------------

func diffSdnVnet(ref inventory.Ref, fields map[string]any, snap inventory.Snapshot) ([]change.Op, error) {
	zone, _, err := fieldString(fields, "zone")
	if err != nil {
		return nil, err
	}
	alias, hasAlias, err := fieldString(fields, "alias")
	if err != nil {
		return nil, err
	}
	tag, hasTag, err := fieldInt(fields, "tag")
	if err != nil {
		return nil, err
	}
	vlanAware, hasVlanAware, err := fieldBool(fields, "vlanAware")
	if err != nil {
		return nil, err
	}

	existing, found := snap.Get(ref)
	if !found {
		create := &change.SdnVnetCreateParams{Zone: zone}
		if hasAlias {
			create.Alias = alias
		}
		if hasTag {
			create.Tag = tag
		}
		if hasVlanAware {
			create.VlanAware = vlanAware
		}
		return []change.Op{{Type: change.OpSdnVnetCreate, Target: ref, Params: create}}, nil
	}

	v, ok := existing.(*inventory.SdnVnet)
	if !ok {
		return nil, fmt.Errorf("%s already exists but is not an sdn vnet", ref)
	}

	// Zone is not editable (SdnVnetUpdateParams has no Zone field).
	upd := &change.SdnVnetUpdateParams{}
	changed := false
	if hasAlias && alias != v.Alias {
		a := alias
		upd.Alias = &a
		changed = true
	}
	if hasTag && tag != v.Tag {
		t := tag
		upd.Tag = &t
		changed = true
	}
	if hasVlanAware && vlanAware != v.VlanAware {
		va := vlanAware
		upd.VlanAware = &va
		changed = true
	}
	if !changed {
		return nil, nil
	}
	return []change.Op{{Type: change.OpSdnVnetUpdate, Target: ref, Params: upd}}, nil
}

// --- sdn subnet -----------------------------------------------------------

func diffSdnSubnet(ref inventory.Ref, fields map[string]any, snap inventory.Snapshot) ([]change.Op, error) {
	vnet, _, err := fieldString(fields, "vnet")
	if err != nil {
		return nil, err
	}
	cidr, _, err := fieldString(fields, "cidr")
	if err != nil {
		return nil, err
	}
	gateway, hasGateway, err := fieldString(fields, "gateway")
	if err != nil {
		return nil, err
	}
	dnsZonePrefix, hasDNSZonePrefix, err := fieldString(fields, "dnsZonePrefix")
	if err != nil {
		return nil, err
	}
	dhcpRanges, hasDHCPRanges, err := fieldStringSlice(fields, "dhcpRanges")
	if err != nil {
		return nil, err
	}
	snat, hasSNAT, err := fieldBool(fields, "snat")
	if err != nil {
		return nil, err
	}

	existing, found := snap.Get(ref)
	if !found {
		create := &change.SdnSubnetCreateParams{Vnet: vnet, CIDR: cidr}
		if hasGateway {
			create.Gateway = gateway
		}
		if hasDNSZonePrefix {
			create.DNSZonePrefix = dnsZonePrefix
		}
		if hasDHCPRanges {
			create.DHCPRanges = dhcpRanges
		}
		if hasSNAT {
			create.SNAT = snat
		}
		return []change.Op{{Type: change.OpSdnSubnetCreate, Target: ref, Params: create}}, nil
	}

	s, ok := existing.(*inventory.SdnSubnet)
	if !ok {
		return nil, fmt.Errorf("%s already exists but is not an sdn subnet", ref)
	}

	// Vnet/CIDR are not editable (SdnSubnetUpdateParams has neither field
	// — the CIDR *is* the ref's identity).
	upd := &change.SdnSubnetUpdateParams{}
	changed := false
	if hasGateway && gateway != s.Gateway {
		g := gateway
		upd.Gateway = &g
		changed = true
	}
	if hasDNSZonePrefix && dnsZonePrefix != s.DNSZonePrefix {
		d := dnsZonePrefix
		upd.DNSZonePrefix = &d
		changed = true
	}
	if hasDHCPRanges && !setEqual(dhcpRanges, s.DHCPRanges) {
		d := dhcpRanges
		upd.DHCPRanges = &d
		changed = true
	}
	if hasSNAT && snat != s.SNAT {
		sn := snat
		upd.SNAT = &sn
		changed = true
	}
	if !changed {
		return nil, nil
	}
	return []change.Op{{Type: change.OpSdnSubnetUpdate, Target: ref, Params: upd}}, nil
}
