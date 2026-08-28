// SPDX-License-Identifier: Apache-2.0

package spec

// delta.go runs import.go's direction backwards: it renders a changeset's ops
// INTO a spec document, so a change staged in the GUI can be proposed against
// the repository that holds the cluster's intent (T-2702).
//
// The pairing with Import is the whole point, and it is checked rather than
// asserted: for a base document B, live snapshot L and op list O,
//
//	Import(ApplyOps(B, O), L)  ==  Import(B, L) + O
//
// T-2702's Proposer verifies exactly that equality before it commits anything
// (gitsync/propose.go), which is why this file's job is only to be faithful,
// never clever. Where a faithful rendering is impossible it says so:
//
//   - An op type the document has no vocabulary for (every delete, and every
//     firewall/IPAM/QoS/WireGuard/raw-file op) returns ErrOpNotExpressible.
//     The spec is a declarative description of managed entities; Import
//     itself never emits a delete (an entity dropped from the document is
//     REPORTED as notInSpec, never pruned), so there is no rendering of
//     "delete this" that would round-trip.
//   - An update whose target the document does not declare returns
//     ErrTargetNotInSpec. Seeding it from live would silently widen what the
//     document manages — the operator asked to change an entity, not to adopt
//     it — so it is refused with the ref named instead.
//
// Both are ordinary, expected outcomes with a precise message, not failures
// to paper over: a changeset that cannot become a spec commit must be told to
// its author, since the alternative is a pull request that does not mean what
// the changeset meant.

import (
	"errors"
	"fmt"
	"sort"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// ErrOpNotExpressible is returned by ApplyOps for an op the declarative
// document has no way to represent. It names the op type and target.
var ErrOpNotExpressible = errors.New("spec: op cannot be represented in the declarative spec")

// ErrTargetNotInSpec is returned by ApplyOps for an update/port op whose
// target entity the document does not declare.
var ErrTargetNotInSpec = errors.New("spec: op targets an entity the spec does not declare")

// ApplyOps returns a copy of base with ops applied, leaving base untouched.
//
// Ops are applied in the order given, so a changeset that edits the same
// entity twice ends with the last write — the same order the change engine
// would apply them in. The result is re-sorted exactly the way Export sorts,
// so a proposed document and an exported one are byte-comparable and the
// git diff shows only what actually changed.
func ApplyOps(base Spec, ops []change.Op) (Spec, error) {
	out := cloneSpec(base)
	if out.SpecVersion == 0 {
		out.SpecVersion = Version
	}
	for _, op := range ops {
		if err := applyOp(&out, op); err != nil {
			return Spec{}, err
		}
	}
	sortSpec(&out)
	return out, nil
}

//nolint:gocyclo // one dispatch table over the op vocabulary; splitting it by kind would hide the closed set this file is responsible for covering.
func applyOp(s *Spec, op change.Op) error {
	switch p := op.Params.(type) {
	case *change.BondCreateParams:
		return applyBondCreate(s, op.Target, p)
	case *change.BondUpdateParams:
		return applyBondUpdate(s, op.Target, p)
	case *change.BridgeCreateParams:
		return applyBridgeCreate(s, op.Target, p)
	case *change.BridgeUpdateParams:
		return applyBridgeUpdate(s, op.Target, p)
	case *change.BridgePortAddParams:
		return applyBridgePort(s, op.Target, p.Port, true)
	case *change.BridgePortRemoveParams:
		return applyBridgePort(s, op.Target, p.Port, false)
	case *change.VlanCreateParams:
		return applyVlanCreate(s, op.Target, p)
	case *change.VlanUpdateParams:
		return applyVlanUpdate(s, op.Target, p)
	case *change.SdnZoneCreateParams:
		return applyZoneCreate(s, op.Target, p)
	case *change.SdnZoneUpdateParams:
		return applyZoneUpdate(s, op.Target, p)
	case *change.SdnVnetCreateParams:
		return applyVnetCreate(s, op.Target, p)
	case *change.SdnVnetUpdateParams:
		return applyVnetUpdate(s, op.Target, p)
	case *change.SdnSubnetCreateParams:
		return applySubnetCreate(s, op.Target, p)
	case *change.SdnSubnetUpdateParams:
		return applySubnetUpdate(s, op.Target, p)
	default:
		return fmt.Errorf("%w: %s %s", ErrOpNotExpressible, op.Type, refLabel(op.Target))
	}
}

// --- bonds ----------------------------------------------------------------

func applyBondCreate(s *Spec, ref inventory.Ref, p *change.BondCreateParams) error {
	if ref.Kind != inventory.KindBond {
		return fmt.Errorf("%w: %s (only Linux bonds are in the spec's vocabulary)", ErrOpNotExpressible, refLabel(ref))
	}
	n := nodeFor(s, ref.Node)
	if findBond(n, ref.ID) != nil {
		return fmt.Errorf("spec: bond %s is already declared; a create op would duplicate it", refLabel(ref))
	}
	n.Bonds = append(n.Bonds, BondSpec{
		Name: ref.ID, Mode: p.Mode, Slaves: sortedCopy(p.Slaves),
		LACPRate: p.LACPRate, XmitHashPolicy: p.XmitHashPolicy, MTU: p.MTU,
	})
	return nil
}

func applyBondUpdate(s *Spec, ref inventory.Ref, p *change.BondUpdateParams) error {
	n := findNode(s, ref.Node)
	var b *BondSpec
	if n != nil {
		b = findBond(n, ref.ID)
	}
	if b == nil {
		return fmt.Errorf("%w: %s", ErrTargetNotInSpec, refLabel(ref))
	}
	setString(&b.Mode, p.Mode)
	setStrings(&b.Slaves, p.Slaves)
	setString(&b.LACPRate, p.LACPRate)
	setString(&b.XmitHashPolicy, p.XmitHashPolicy)
	setInt(&b.MTU, p.MTU)
	return nil
}

// --- bridges --------------------------------------------------------------

func applyBridgeCreate(s *Spec, ref inventory.Ref, p *change.BridgeCreateParams) error {
	if ref.Kind != inventory.KindBridge {
		return fmt.Errorf("%w: %s (only Linux bridges are in the spec's vocabulary)", ErrOpNotExpressible, refLabel(ref))
	}
	n := nodeFor(s, ref.Node)
	if findBridge(n, ref.ID) != nil {
		return fmt.Errorf("spec: bridge %s is already declared; a create op would duplicate it", refLabel(ref))
	}
	n.Bridges = append(n.Bridges, BridgeSpec{
		Name: ref.ID, Ports: sortedCopy(p.Ports), VlanAware: p.VlanAware,
		Vids: vidRangeStrings(p.Vids), Addresses: append([]string(nil), p.Addresses...),
		Gateway: p.Gateway, MTU: p.MTU, STP: p.STP, Comments: p.Comments,
	})
	return nil
}

func applyBridgeUpdate(s *Spec, ref inventory.Ref, p *change.BridgeUpdateParams) error {
	b, err := bridgeIn(s, ref)
	if err != nil {
		return err
	}
	setBool(&b.VlanAware, p.VlanAware)
	if p.Vids != nil {
		b.Vids = vidRangeStrings(*p.Vids)
	}
	setStrings(&b.Addresses, p.Addresses)
	setString(&b.Gateway, p.Gateway)
	setInt(&b.MTU, p.MTU)
	setBool(&b.STP, p.STP)
	setString(&b.Comments, p.Comments)
	return nil
}

// applyBridgePort adds or removes one port from a declared bridge's port
// list. Removing a port that is not declared, and adding one that already is,
// are both no-ops rather than errors: the document already says what the op
// wanted it to say, and the Proposer's round-trip check is what decides
// whether the resulting plan still matches the changeset.
func applyBridgePort(s *Spec, ref inventory.Ref, port string, add bool) error {
	b, err := bridgeIn(s, ref)
	if err != nil {
		return err
	}
	out := make([]string, 0, len(b.Ports)+1)
	for _, existing := range b.Ports {
		if existing == port {
			continue
		}
		out = append(out, existing)
	}
	if add {
		out = append(out, port)
	}
	sort.Strings(out)
	if len(out) == 0 {
		out = nil
	}
	b.Ports = out
	return nil
}

func bridgeIn(s *Spec, ref inventory.Ref) (*BridgeSpec, error) {
	n := findNode(s, ref.Node)
	var b *BridgeSpec
	if n != nil {
		b = findBridge(n, ref.ID)
	}
	if b == nil {
		return nil, fmt.Errorf("%w: %s", ErrTargetNotInSpec, refLabel(ref))
	}
	return b, nil
}

// --- vlans ----------------------------------------------------------------

func applyVlanCreate(s *Spec, ref inventory.Ref, p *change.VlanCreateParams) error {
	if p.OVS {
		return fmt.Errorf("%w: %s (an OVS int port is not in the spec's vocabulary)", ErrOpNotExpressible, refLabel(ref))
	}
	n := nodeFor(s, ref.Node)
	if findVLAN(n, ref.ID) != nil {
		return fmt.Errorf("spec: vlan %s is already declared; a create op would duplicate it", refLabel(ref))
	}
	n.VLANs = append(n.VLANs, VLANSpec{
		Name: ref.ID, Parent: p.Parent, Vid: p.Vid,
		Addresses: append([]string(nil), p.Addresses...), MTU: p.MTU,
	})
	return nil
}

func applyVlanUpdate(s *Spec, ref inventory.Ref, p *change.VlanUpdateParams) error {
	n := findNode(s, ref.Node)
	var v *VLANSpec
	if n != nil {
		v = findVLAN(n, ref.ID)
	}
	if v == nil {
		return fmt.Errorf("%w: %s", ErrTargetNotInSpec, refLabel(ref))
	}
	setStrings(&v.Addresses, p.Addresses)
	setInt(&v.MTU, p.MTU)
	return nil
}

// --- sdn ------------------------------------------------------------------

func applyZoneCreate(s *Spec, ref inventory.Ref, p *change.SdnZoneCreateParams) error {
	sdn := sdnFor(s)
	for i := range sdn.Zones {
		if sdn.Zones[i].ID == ref.ID {
			return fmt.Errorf("spec: sdn zone %s is already declared; a create op would duplicate it", ref.ID)
		}
	}
	sdn.Zones = append(sdn.Zones, ZoneSpec{
		ID: ref.ID, Type: p.Type, Bridge: p.Bridge, Controller: p.Controller, IPAM: p.IPAM,
		Nodes: sortedCopy(p.Nodes), ExitNodes: sortedCopy(p.ExitNodes), Peers: sortedCopy(p.Peers),
		VrfVxlan: p.VrfVxlan, MTU: p.MTU,
	})
	return nil
}

func applyZoneUpdate(s *Spec, ref inventory.Ref, p *change.SdnZoneUpdateParams) error {
	if s.SDN == nil {
		return fmt.Errorf("%w: %s", ErrTargetNotInSpec, refLabel(ref))
	}
	for i := range s.SDN.Zones {
		if s.SDN.Zones[i].ID != ref.ID {
			continue
		}
		z := &s.SDN.Zones[i]
		setString(&z.Bridge, p.Bridge)
		setString(&z.Controller, p.Controller)
		setString(&z.IPAM, p.IPAM)
		setStrings(&z.Nodes, p.Nodes)
		setStrings(&z.ExitNodes, p.ExitNodes)
		setStrings(&z.Peers, p.Peers)
		setInt(&z.VrfVxlan, p.VrfVxlan)
		setInt(&z.MTU, p.MTU)
		return nil
	}
	return fmt.Errorf("%w: %s", ErrTargetNotInSpec, refLabel(ref))
}

func applyVnetCreate(s *Spec, ref inventory.Ref, p *change.SdnVnetCreateParams) error {
	sdn := sdnFor(s)
	for i := range sdn.Vnets {
		if sdn.Vnets[i].ID == ref.ID {
			return fmt.Errorf("spec: sdn vnet %s is already declared; a create op would duplicate it", ref.ID)
		}
	}
	sdn.Vnets = append(sdn.Vnets, VnetSpec{ID: ref.ID, Zone: p.Zone, Alias: p.Alias, Tag: p.Tag, VlanAware: p.VlanAware})
	return nil
}

func applyVnetUpdate(s *Spec, ref inventory.Ref, p *change.SdnVnetUpdateParams) error {
	if s.SDN == nil {
		return fmt.Errorf("%w: %s", ErrTargetNotInSpec, refLabel(ref))
	}
	for i := range s.SDN.Vnets {
		if s.SDN.Vnets[i].ID != ref.ID {
			continue
		}
		v := &s.SDN.Vnets[i]
		setString(&v.Alias, p.Alias)
		setInt(&v.Tag, p.Tag)
		setBool(&v.VlanAware, p.VlanAware)
		return nil
	}
	return fmt.Errorf("%w: %s", ErrTargetNotInSpec, refLabel(ref))
}

func applySubnetCreate(s *Spec, ref inventory.Ref, p *change.SdnSubnetCreateParams) error {
	sdn := sdnFor(s)
	for i := range sdn.Subnets {
		if sdn.Subnets[i].ID == ref.ID {
			return fmt.Errorf("spec: sdn subnet %s is already declared; a create op would duplicate it", ref.ID)
		}
	}
	sdn.Subnets = append(sdn.Subnets, SubnetSpec{
		ID: ref.ID, Vnet: p.Vnet, Gateway: p.Gateway, DNSZonePrefix: p.DNSZonePrefix,
		DHCPRanges: sortedCopy(p.DHCPRanges), SNAT: p.SNAT,
	})
	return nil
}

func applySubnetUpdate(s *Spec, ref inventory.Ref, p *change.SdnSubnetUpdateParams) error {
	if s.SDN == nil {
		return fmt.Errorf("%w: %s", ErrTargetNotInSpec, refLabel(ref))
	}
	for i := range s.SDN.Subnets {
		if s.SDN.Subnets[i].ID != ref.ID {
			continue
		}
		sn := &s.SDN.Subnets[i]
		setString(&sn.Gateway, p.Gateway)
		setString(&sn.DNSZonePrefix, p.DNSZonePrefix)
		setStrings(&sn.DHCPRanges, p.DHCPRanges)
		setBool(&sn.SNAT, p.SNAT)
		return nil
	}
	return fmt.Errorf("%w: %s", ErrTargetNotInSpec, refLabel(ref))
}

// --- lookup / mutation helpers --------------------------------------------

func findNode(s *Spec, name string) *NodeSpec {
	for i := range s.Nodes {
		if s.Nodes[i].Name == name {
			return &s.Nodes[i]
		}
	}
	return nil
}

// nodeFor returns the named node, appending an empty one if the document does
// not have it yet — a create op for a node absent from the spec is ordinary
// (a first bridge on a node the document never mentioned).
func nodeFor(s *Spec, name string) *NodeSpec {
	if n := findNode(s, name); n != nil {
		return n
	}
	s.Nodes = append(s.Nodes, NodeSpec{Name: name})
	return &s.Nodes[len(s.Nodes)-1]
}

func sdnFor(s *Spec) *SDNSpec {
	if s.SDN == nil {
		s.SDN = &SDNSpec{}
	}
	return s.SDN
}

func findBond(n *NodeSpec, name string) *BondSpec {
	for i := range n.Bonds {
		if n.Bonds[i].Name == name {
			return &n.Bonds[i]
		}
	}
	return nil
}

func findBridge(n *NodeSpec, name string) *BridgeSpec {
	for i := range n.Bridges {
		if n.Bridges[i].Name == name {
			return &n.Bridges[i]
		}
	}
	return nil
}

func findVLAN(n *NodeSpec, name string) *VLANSpec {
	for i := range n.VLANs {
		if n.VLANs[i].Name == name {
			return &n.VLANs[i]
		}
	}
	return nil
}

func setString(dst *string, v *string) {
	if v != nil {
		*dst = *v
	}
}

func setInt(dst *int, v *int) {
	if v != nil {
		*dst = *v
	}
}

func setBool(dst *bool, v *bool) {
	if v != nil {
		*dst = *v
	}
}

func setStrings(dst *[]string, v *[]string) {
	if v != nil {
		*dst = sortedCopy(*v)
	}
}

// vidRangeStrings renders change.VidRange values the way export.go renders
// inventory ones, so a proposed document and an exported one spell the same
// range identically.
func vidRangeStrings(vids []change.VidRange) []string {
	if len(vids) == 0 {
		return nil
	}
	out := make([]string, len(vids))
	for i, v := range vids {
		out[i] = inventory.VidRange{Low: v.Low, High: v.High}.String()
	}
	sort.Strings(out)
	return out
}

func refLabel(ref inventory.Ref) string {
	if ref.IsZero() {
		return "(no target)"
	}
	return ref.String()
}

// sortSpec re-establishes Export's ordering after a mutation, so two
// documents describing the same intent are byte-identical once marshaled.
func sortSpec(s *Spec) {
	for i := range s.Nodes {
		n := &s.Nodes[i]
		sort.Slice(n.Bonds, func(a, b int) bool { return n.Bonds[a].Name < n.Bonds[b].Name })
		sort.Slice(n.Bridges, func(a, b int) bool { return n.Bridges[a].Name < n.Bridges[b].Name })
		sort.Slice(n.VLANs, func(a, b int) bool { return n.VLANs[a].Name < n.VLANs[b].Name })
	}
	sort.Slice(s.Nodes, func(a, b int) bool { return s.Nodes[a].Name < s.Nodes[b].Name })
	if s.SDN == nil {
		return
	}
	sort.Slice(s.SDN.Zones, func(a, b int) bool { return s.SDN.Zones[a].ID < s.SDN.Zones[b].ID })
	sort.Slice(s.SDN.Vnets, func(a, b int) bool { return s.SDN.Vnets[a].ID < s.SDN.Vnets[b].ID })
	sort.Slice(s.SDN.Subnets, func(a, b int) bool { return s.SDN.Subnets[a].ID < s.SDN.Subnets[b].ID })
	if len(s.SDN.Zones)+len(s.SDN.Vnets)+len(s.SDN.Subnets) == 0 {
		s.SDN = nil
	}
}

// cloneSpec deep-copies every slice in the document. ApplyOps must never
// write through into its caller's Spec: the Proposer holds the base document
// and the proposed one side by side to render the diff between them.
func cloneSpec(s Spec) Spec {
	out := Spec{SpecVersion: s.SpecVersion}
	if len(s.Nodes) > 0 {
		out.Nodes = make([]NodeSpec, len(s.Nodes))
		for i, n := range s.Nodes {
			cp := NodeSpec{Name: n.Name}
			if len(n.Bonds) > 0 {
				cp.Bonds = make([]BondSpec, len(n.Bonds))
				for j, b := range n.Bonds {
					b.Slaves = append([]string(nil), b.Slaves...)
					cp.Bonds[j] = b
				}
			}
			if len(n.Bridges) > 0 {
				cp.Bridges = make([]BridgeSpec, len(n.Bridges))
				for j, b := range n.Bridges {
					b.Ports = append([]string(nil), b.Ports...)
					b.Vids = append([]string(nil), b.Vids...)
					b.Addresses = append([]string(nil), b.Addresses...)
					cp.Bridges[j] = b
				}
			}
			if len(n.VLANs) > 0 {
				cp.VLANs = make([]VLANSpec, len(n.VLANs))
				for j, v := range n.VLANs {
					v.Addresses = append([]string(nil), v.Addresses...)
					cp.VLANs[j] = v
				}
			}
			out.Nodes[i] = cp
		}
	}
	if s.SDN != nil {
		sdn := SDNSpec{}
		for _, z := range s.SDN.Zones {
			z.Nodes = append([]string(nil), z.Nodes...)
			z.ExitNodes = append([]string(nil), z.ExitNodes...)
			z.Peers = append([]string(nil), z.Peers...)
			sdn.Zones = append(sdn.Zones, z)
		}
		sdn.Vnets = append(sdn.Vnets, s.SDN.Vnets...)
		for _, sn := range s.SDN.Subnets {
			sn.DHCPRanges = append([]string(nil), sn.DHCPRanges...)
			sdn.Subnets = append(sdn.Subnets, sn)
		}
		out.SDN = &sdn
	}
	return out
}
