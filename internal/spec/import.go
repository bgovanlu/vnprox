package spec

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// managedKinds is the closed set of inventory kinds this spec reconciles.
// It is exactly the set internal/blueprint's diff engine covers, so a spec
// import and a blueprint instantiation share one op vocabulary. Firewall
// rulesets, guests, physical NICs, LLDP neighbours and OVS bridges/bonds are
// deliberately NOT managed — an entity of an unmanaged kind never produces an
// op and never appears in notInSpec (it isn't the spec's concern).
var managedKinds = map[inventory.Kind]bool{
	inventory.KindBond:      true,
	inventory.KindBridge:    true,
	inventory.KindVlan:      true,
	inventory.KindSDNZone:   true,
	inventory.KindSDNVnet:   true,
	inventory.KindSDNSubnet: true,
}

// Import diffs a parsed spec against live inventory and returns the ordered
// change ops that would converge live state to the spec, plus notInSpec: the
// refs of managed-kind entities present live but absent from the spec.
//
// Import NEVER applies and NEVER deletes. The ops are all create/update/
// port-add/port-remove — the same absent→create / divergent→update /
// matching→noop pattern internal/blueprint uses (adapters.go), extended
// cluster-wide. notInSpec is a report, not a delete list: there is no
// implicit prune (T-1101 AC5), so an operator who removed an entity from the
// spec sees it flagged and decides explicitly, rather than having live state
// silently torn down. The caller feeds the ops to change.Service.Create to
// produce a DRAFT changeset (docs/api.md POST /spec/import).
//
// Signature note (frozen for T-1102/T-1105/T-1106/T-1107):
// Import(Spec, inventory.Snapshot) ([]change.Op, []inventory.Ref, error).
// Ops are emitted in a deterministic order (nodes sorted by name, then bonds,
// bridges, vlans; then SDN zones, vnets, subnets — each sorted by identity)
// so a golden-ops test is stable regardless of the document's own ordering.
func Import(s Spec, live inventory.Snapshot) ([]change.Op, []inventory.Ref, error) {
	if err := validateVersion(s); err != nil {
		return nil, nil, err
	}

	var ops []change.Op
	inSpec := map[inventory.Ref]bool{}

	nodes := append([]NodeSpec(nil), s.Nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	for _, n := range nodes {
		bonds := append([]BondSpec(nil), n.Bonds...)
		sort.Slice(bonds, func(i, j int) bool { return bonds[i].Name < bonds[j].Name })
		for _, b := range bonds {
			ref := inventory.Ref{Kind: inventory.KindBond, Node: n.Name, ID: b.Name}
			inSpec[ref] = true
			o, err := diffBond(ref, b, live)
			if err != nil {
				return nil, nil, err
			}
			ops = append(ops, o...)
		}

		bridges := append([]BridgeSpec(nil), n.Bridges...)
		sort.Slice(bridges, func(i, j int) bool { return bridges[i].Name < bridges[j].Name })
		for _, br := range bridges {
			ref := inventory.Ref{Kind: inventory.KindBridge, Node: n.Name, ID: br.Name}
			inSpec[ref] = true
			o, err := diffBridge(ref, br, live)
			if err != nil {
				return nil, nil, err
			}
			ops = append(ops, o...)
		}

		vlans := append([]VLANSpec(nil), n.VLANs...)
		sort.Slice(vlans, func(i, j int) bool { return vlans[i].Name < vlans[j].Name })
		for _, vl := range vlans {
			ref := inventory.Ref{Kind: inventory.KindVlan, Node: n.Name, ID: vl.Name}
			inSpec[ref] = true
			o, err := diffVLAN(ref, vl, live)
			if err != nil {
				return nil, nil, err
			}
			ops = append(ops, o...)
		}
	}

	if s.SDN != nil {
		zones := append([]ZoneSpec(nil), s.SDN.Zones...)
		sort.Slice(zones, func(i, j int) bool { return zones[i].ID < zones[j].ID })
		for _, z := range zones {
			ref := inventory.Ref{Kind: inventory.KindSDNZone, ID: z.ID}
			inSpec[ref] = true
			o, err := diffZone(ref, z, live)
			if err != nil {
				return nil, nil, err
			}
			ops = append(ops, o...)
		}

		vnets := append([]VnetSpec(nil), s.SDN.Vnets...)
		sort.Slice(vnets, func(i, j int) bool { return vnets[i].ID < vnets[j].ID })
		for _, v := range vnets {
			ref := inventory.Ref{Kind: inventory.KindSDNVnet, ID: v.ID}
			inSpec[ref] = true
			o, err := diffVnet(ref, v, live)
			if err != nil {
				return nil, nil, err
			}
			ops = append(ops, o...)
		}

		subnets := append([]SubnetSpec(nil), s.SDN.Subnets...)
		sort.Slice(subnets, func(i, j int) bool { return subnets[i].ID < subnets[j].ID })
		for _, sn := range subnets {
			ref := inventory.Ref{Kind: inventory.KindSDNSubnet, ID: sn.ID}
			inSpec[ref] = true
			o, err := diffSubnet(ref, sn, live)
			if err != nil {
				return nil, nil, err
			}
			ops = append(ops, o...)
		}
	}

	notInSpec := computeNotInSpec(live, inSpec)
	return ops, notInSpec, nil
}

// computeNotInSpec collects the refs of managed-kind entities present in live
// but absent from the spec, sorted by ref string for a stable report. OVS
// bridges/bonds and OVS Int Ports are skipped (unmanaged), matching Export.
func computeNotInSpec(live inventory.Snapshot, inSpec map[inventory.Ref]bool) []inventory.Ref {
	var out []inventory.Ref
	for _, e := range live.All() {
		ref := e.GetRef()
		if !managedKinds[ref.Kind] || inSpec[ref] {
			continue
		}
		if vl, ok := e.(*inventory.VlanIface); ok && vl.Virt == "ovs" {
			continue
		}
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// --- per-entity diffs (mirror internal/blueprint/adapters.go) -------------

func diffBond(ref inventory.Ref, b BondSpec, live inventory.Snapshot) ([]change.Op, error) {
	existing, found := live.Get(ref)
	if !found {
		create := &change.BondCreateParams{Mode: b.Mode, Slaves: b.Slaves}
		create.LACPRate = b.LACPRate
		create.XmitHashPolicy = b.XmitHashPolicy
		create.MTU = b.MTU
		return []change.Op{{Type: change.OpBondCreate, Target: ref, Params: create}}, nil
	}
	bd, ok := existing.(*inventory.Bond)
	if !ok {
		return nil, fmt.Errorf("spec: %s already exists but is not a bond", ref)
	}

	upd := &change.BondUpdateParams{}
	changed := false
	if b.Mode != "" && b.Mode != bd.Mode {
		m := b.Mode
		upd.Mode = &m
		changed = true
	}
	if len(b.Slaves) > 0 && !setEqual(b.Slaves, bd.DeclaredSlaves) {
		sl := b.Slaves
		upd.Slaves = &sl
		changed = true
	}
	if b.LACPRate != "" && b.LACPRate != bd.LACPRate {
		l := b.LACPRate
		upd.LACPRate = &l
		changed = true
	}
	if b.XmitHashPolicy != "" && b.XmitHashPolicy != bd.XmitHashPolicy {
		x := b.XmitHashPolicy
		upd.XmitHashPolicy = &x
		changed = true
	}
	if b.MTU != 0 && b.MTU != bd.MTUDeclared {
		m := b.MTU
		upd.MTU = &m
		changed = true
	}
	if !changed {
		return nil, nil
	}
	return []change.Op{{Type: change.OpBondUpdate, Target: ref, Params: upd}}, nil
}

func diffBridge(ref inventory.Ref, b BridgeSpec, live inventory.Snapshot) ([]change.Op, error) {
	vids, err := parseVids(b.Vids)
	if err != nil {
		return nil, fmt.Errorf("spec: bridge %s: %w", ref, err)
	}

	existing, found := live.Get(ref)
	if !found {
		create := &change.BridgeCreateParams{
			Gateway: b.Gateway, Comments: b.Comments, Ports: b.Ports,
			Vids: vids, Addresses: b.Addresses, MTU: b.MTU,
			VlanAware: b.VlanAware, STP: b.STP,
		}
		return []change.Op{{Type: change.OpBridgeCreate, Target: ref, Params: create}}, nil
	}
	br, ok := existing.(*inventory.Bridge)
	if !ok {
		return nil, fmt.Errorf("spec: %s already exists but is not a bridge", ref)
	}

	var ops []change.Op
	upd := &change.BridgeUpdateParams{}
	changed := false
	if b.Gateway != "" && b.Gateway != br.Gateway {
		g := b.Gateway
		upd.Gateway = &g
		changed = true
	}
	if b.Comments != "" && b.Comments != br.Comments {
		c := b.Comments
		upd.Comments = &c
		changed = true
	}
	if len(vids) > 0 && !vidsEqual(vids, inventoryVids(br.Vids)) {
		v := vids
		upd.Vids = &v
		changed = true
	}
	if len(b.Addresses) > 0 && !setEqual(b.Addresses, br.Addresses) {
		a := b.Addresses
		upd.Addresses = &a
		changed = true
	}
	if b.MTU != 0 && b.MTU != br.MTUDeclared {
		m := b.MTU
		upd.MTU = &m
		changed = true
	}
	// Booleans follow the same omitempty=unmanaged rule as every scalar
	// field here: a false/omitted flag means "don't manage this", so only a
	// spec value of true that diverges from live produces an op. Reconciling
	// a flag back to false is therefore not expressible in v1 (documented in
	// docs/data-model.md §5) — the conservative choice keeps a partial
	// hand-edit from silently generating a disable op.
	if b.VlanAware && !br.VlanAware {
		v := true
		upd.VlanAware = &v
		changed = true
	}
	if b.STP && !br.STP {
		s := true
		upd.STP = &s
		changed = true
	}
	if changed {
		ops = append(ops, change.Op{Type: change.OpBridgeUpdate, Target: ref, Params: upd})
	}
	if len(b.Ports) > 0 {
		for _, add := range missingFrom(b.Ports, br.DeclaredPortNames) {
			ops = append(ops, change.Op{Type: change.OpBridgePortAdd, Target: ref, Params: &change.BridgePortAddParams{Port: add}})
		}
		for _, rm := range missingFrom(br.DeclaredPortNames, b.Ports) {
			ops = append(ops, change.Op{Type: change.OpBridgePortRemove, Target: ref, Params: &change.BridgePortRemoveParams{Port: rm}})
		}
	}
	return ops, nil
}

func diffVLAN(ref inventory.Ref, v VLANSpec, live inventory.Snapshot) ([]change.Op, error) {
	existing, found := live.Get(ref)
	if !found {
		create := &change.VlanCreateParams{Parent: v.Parent, Vid: v.Vid, Addresses: v.Addresses, MTU: v.MTU}
		return []change.Op{{Type: change.OpVlanCreate, Target: ref, Params: create}}, nil
	}
	vl, ok := existing.(*inventory.VlanIface)
	if !ok {
		return nil, fmt.Errorf("spec: %s already exists but is not a vlan interface", ref)
	}

	// Parent/Vid are not editable on an existing vlan (delete+create); only
	// addresses/mtu diff — mirrors blueprint's diffVlan.
	upd := &change.VlanUpdateParams{}
	changed := false
	if len(v.Addresses) > 0 && !setEqual(v.Addresses, vl.Addresses) {
		a := v.Addresses
		upd.Addresses = &a
		changed = true
	}
	if v.MTU != 0 && v.MTU != vl.MTUDeclared {
		m := v.MTU
		upd.MTU = &m
		changed = true
	}
	if !changed {
		return nil, nil
	}
	return []change.Op{{Type: change.OpVlanUpdate, Target: ref, Params: upd}}, nil
}

func diffZone(ref inventory.Ref, z ZoneSpec, live inventory.Snapshot) ([]change.Op, error) {
	existing, found := live.Get(ref)
	if !found {
		create := &change.SdnZoneCreateParams{
			Type: z.Type, Bridge: z.Bridge, Controller: z.Controller, IPAM: z.IPAM,
			Nodes: z.Nodes, ExitNodes: z.ExitNodes, Peers: z.Peers, VrfVxlan: z.VrfVxlan, MTU: z.MTU,
		}
		return []change.Op{{Type: change.OpSdnZoneCreate, Target: ref, Params: create}}, nil
	}
	zn, ok := existing.(*inventory.SdnZone)
	if !ok {
		return nil, fmt.Errorf("spec: %s already exists but is not an sdn zone", ref)
	}

	upd := &change.SdnZoneUpdateParams{}
	changed := false
	if z.Bridge != "" && z.Bridge != zn.Bridge {
		b := z.Bridge
		upd.Bridge = &b
		changed = true
	}
	if z.Controller != "" && z.Controller != zn.Controller {
		c := z.Controller
		upd.Controller = &c
		changed = true
	}
	if z.IPAM != "" && z.IPAM != zn.IPAM {
		i := z.IPAM
		upd.IPAM = &i
		changed = true
	}
	if len(z.Nodes) > 0 && !setEqual(z.Nodes, zn.Nodes) {
		n := z.Nodes
		upd.Nodes = &n
		changed = true
	}
	if len(z.ExitNodes) > 0 && !setEqual(z.ExitNodes, zn.ExitNodes) {
		e := z.ExitNodes
		upd.ExitNodes = &e
		changed = true
	}
	if len(z.Peers) > 0 && !setEqual(z.Peers, zn.Peers) {
		p := z.Peers
		upd.Peers = &p
		changed = true
	}
	if z.VrfVxlan != 0 && z.VrfVxlan != zn.VrfVxlan {
		v := z.VrfVxlan
		upd.VrfVxlan = &v
		changed = true
	}
	if z.MTU != 0 && z.MTU != zn.MTU {
		m := z.MTU
		upd.MTU = &m
		changed = true
	}
	if !changed {
		return nil, nil
	}
	return []change.Op{{Type: change.OpSdnZoneUpdate, Target: ref, Params: upd}}, nil
}

func diffVnet(ref inventory.Ref, v VnetSpec, live inventory.Snapshot) ([]change.Op, error) {
	existing, found := live.Get(ref)
	if !found {
		create := &change.SdnVnetCreateParams{Zone: v.Zone, Alias: v.Alias, Tag: v.Tag, VlanAware: v.VlanAware}
		return []change.Op{{Type: change.OpSdnVnetCreate, Target: ref, Params: create}}, nil
	}
	vn, ok := existing.(*inventory.SdnVnet)
	if !ok {
		return nil, fmt.Errorf("spec: %s already exists but is not an sdn vnet", ref)
	}

	upd := &change.SdnVnetUpdateParams{}
	changed := false
	if v.Alias != "" && v.Alias != vn.Alias {
		a := v.Alias
		upd.Alias = &a
		changed = true
	}
	if v.Tag != 0 && v.Tag != vn.Tag {
		t := v.Tag
		upd.Tag = &t
		changed = true
	}
	if v.VlanAware && !vn.VlanAware {
		va := true
		upd.VlanAware = &va
		changed = true
	}
	if !changed {
		return nil, nil
	}
	return []change.Op{{Type: change.OpSdnVnetUpdate, Target: ref, Params: upd}}, nil
}

func diffSubnet(ref inventory.Ref, s SubnetSpec, live inventory.Snapshot) ([]change.Op, error) {
	existing, found := live.Get(ref)
	if !found {
		create := &change.SdnSubnetCreateParams{
			Vnet: s.Vnet, CIDR: s.ID, Gateway: s.Gateway,
			DNSZonePrefix: s.DNSZonePrefix, DHCPRanges: s.DHCPRanges, SNAT: s.SNAT,
		}
		return []change.Op{{Type: change.OpSdnSubnetCreate, Target: ref, Params: create}}, nil
	}
	sn, ok := existing.(*inventory.SdnSubnet)
	if !ok {
		return nil, fmt.Errorf("spec: %s already exists but is not an sdn subnet", ref)
	}

	upd := &change.SdnSubnetUpdateParams{}
	changed := false
	if s.Gateway != "" && s.Gateway != sn.Gateway {
		g := s.Gateway
		upd.Gateway = &g
		changed = true
	}
	if s.DNSZonePrefix != "" && s.DNSZonePrefix != sn.DNSZonePrefix {
		d := s.DNSZonePrefix
		upd.DNSZonePrefix = &d
		changed = true
	}
	if len(s.DHCPRanges) > 0 && !setEqual(s.DHCPRanges, sn.DHCPRanges) {
		d := s.DHCPRanges
		upd.DHCPRanges = &d
		changed = true
	}
	if s.SNAT && !sn.SNAT {
		snat := true
		upd.SNAT = &snat
		changed = true
	}
	if !changed {
		return nil, nil
	}
	return []change.Op{{Type: change.OpSdnSubnetUpdate, Target: ref, Params: upd}}, nil
}

// --- helpers (mirror internal/blueprint/fieldutil.go) ---------------------

func inventoryVids(vids []inventory.VidRange) []change.VidRange {
	out := make([]change.VidRange, len(vids))
	for i, v := range vids {
		out[i] = change.VidRange{Low: v.Low, High: v.High}
	}
	return out
}

// parseVids parses a bridge spec's VID string forms ("100", "2-4094") into
// change.VidRange values. It is the inverse of export.go's vidStrings.
func parseVids(ss []string) ([]change.VidRange, error) {
	if len(ss) == 0 {
		return nil, nil
	}
	out := make([]change.VidRange, len(ss))
	for i, s := range ss {
		lo, hi, found := strings.Cut(s, "-")
		low, err := strconv.Atoi(strings.TrimSpace(lo))
		if err != nil {
			return nil, fmt.Errorf("invalid vid %q", s)
		}
		high := low
		if found {
			high, err = strconv.Atoi(strings.TrimSpace(hi))
			if err != nil {
				return nil, fmt.Errorf("invalid vid range %q", s)
			}
		}
		out[i] = change.VidRange{Low: low, High: high}
	}
	return out, nil
}

func setEqual(a, b []string) bool { return canonSet(a) == canonSet(b) }

func canonSet(ss []string) string {
	cp := append([]string(nil), ss...)
	sort.Strings(cp)
	return strings.Join(cp, "\x00")
}

func vidsEqual(a, b []change.VidRange) bool { return canonVids(a) == canonVids(b) }

func canonVids(vids []change.VidRange) string {
	ss := make([]string, len(vids))
	for i, v := range vids {
		if v.Low == v.High {
			ss[i] = strconv.Itoa(v.Low)
		} else {
			ss[i] = strconv.Itoa(v.Low) + "-" + strconv.Itoa(v.High)
		}
	}
	sort.Strings(ss)
	return strings.Join(ss, ",")
}

// missingFrom returns the elements of want not present in have, preserving
// want's order.
func missingFrom(want, have []string) []string {
	haveSet := make(map[string]bool, len(have))
	for _, h := range have {
		haveSet[h] = true
	}
	var out []string
	for _, w := range want {
		if !haveSet[w] {
			out = append(out, w)
		}
	}
	return out
}
