// SPDX-License-Identifier: Apache-2.0

// preview.go implements T-2605's post-apply topology preview: the map an
// operator would see if this changeset were applied, computed in memory from
// the ops plus the live inventory graph.
//
// THE GAP THIS CLOSES. T-2404's impact answers "what breaks". The diff answers
// "what fields change". Neither answers the question an operator actually forms
// before clicking apply: *what will the map look like*.
//
// WHY THIS IS A SECOND PROJECTION, NOT validate_projection.go's.
// validate_projection.go already folds ops forward over a snapshot — but what it
// folds into is a set of KEY MAPS (does this name exist on this node yet; which
// CIDRs are declared; which iface is enslaved by which owner). It never
// materializes an entity, because a referential check never needs one: it asks
// existence and overlap questions, and answers them from indexes that are
// strictly cheaper than entities. This file folds into ENTITIES, because a
// renderable snapshot is exactly the thing key maps threw away. Extracting one
// from the other would mean either making the validator carry entity state it
// has no use for — on the hot path of every draft mutation, in the file whose
// correctness is the apply-blocking safety guarantee — or making the preview
// re-derive entities from indexes that deliberately do not retain them. Both
// trade a real cost for a cosmetic single-file-ness, so the two projections are
// deliberately built alongside each other, over the same inventory.Snapshot,
// and pinned together by TestPreview_EveryOpTypeIsProjectedOrDisclosed: every op
// type in the vocabulary either has a projection rule here or is disclosed by
// name with a reason. A new op cannot be added to the vocabulary and silently
// skipped by this file.
//
// THREE PROPERTIES, each load-bearing:
//
//   - BEST-EFFORT, AND IT SAYS SO. An op whose effect this file cannot express
//     is listed in Unprojectable BY NAME with a reason. Silently dropping it
//     would make the preview a lie of omission at exactly the moment someone is
//     deciding whether to apply.
//
//   - REMOVED IS MARKED, NOT OMITTED. A deletion is reported as a `removed`
//     change rather than by the entity simply being absent, so "this bridge
//     disappears" is something the response SAYS rather than something a client
//     has to notice.
//
//   - READ-ONLY AND SIDE-EFFECT FREE. Nothing here touches the store, a node
//     agent, a PVE gateway, or the live graph. It reads a snapshot and returns
//     values; inventory.ProjectSnapshot clones every entity on the way in, so
//     not even linkAll's in-place Ref resolution can reach the live graph.

package change

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// UnprojectableOp names one op whose effect the projection could not express,
// and why. The reason is not optional: an op listed with no explanation reads
// as an unexplained gap in the preview, which is the thing this list exists to
// prevent.
type UnprojectableOp struct {
	OpID   string `json:"opId,omitempty"`
	Op     string `json:"op"`
	Target string `json:"target,omitempty"`
	Reason string `json:"reason"`
}

// PreviewChange is one entity's difference between the live snapshot and the
// projected one. It is topology.EntityDiff minus its attribution block: a
// preview describes something that has NOT happened, so "who did this" has no
// honest value to carry — the answer would be the changeset being previewed, on
// every single row.
type PreviewChange struct {
	Ref    string                 `json:"ref"`
	Kind   string                 `json:"kind"`
	Node   string                 `json:"node,omitempty"`
	Name   string                 `json:"name,omitempty"`
	Change topology.DiffChange    `json:"change"`
	Fields []topology.FieldChange `json:"fields"`
}

// Preview is GET /changesets/{id}/preview's body: the projected map, what
// differs about it, and what could not be projected.
//
//nolint:govet // fieldalignment: field order is the documented wire shape (docs/api.md), not packing.
type Preview struct {
	ChangesetID   string            `json:"changesetId"`
	Changes       []PreviewChange   `json:"changes"`
	Unprojectable []UnprojectableOp `json:"unprojectable"`
	Topology      topology.Topology `json:"topology"`
	GeneratedAt   int64             `json:"generatedAt"`
	BestEffort    bool              `json:"bestEffort"`
}

// unprojectableReasons is the DECLARED half of the op vocabulary: op types
// whose effect is not expressible as a change to the inventory graph, each with
// the reason the response carries. Every reason is a statement about the op's
// subject, not about this file's completeness — "vnprox has not implemented
// this" is not a reason an operator can act on.
//
//nolint:gochecknoglobals // a lookup table, read-only after construction
var unprojectableReasons = buildUnprojectableReasons()

func buildUnprojectableReasons() map[OpType]string {
	reasons := map[OpType]string{
		OpIfaceRawReplace: "a raw /etc/network/interfaces edit is applied as file text; which entities it produces is only known once the node re-parses it",
		OpSdnApply:        "applies whatever SDN config is already staged cluster-wide; it changes no entity of its own",
	}
	for _, t := range []OpType{
		OpFwRuleCreate, OpFwRuleUpdate, OpFwRuleDelete, OpFwRuleMove, OpFwOptionsUpdate,
		OpFwAliasCreate, OpFwAliasUpdate, OpFwAliasDelete,
		OpFwIpsetCreate, OpFwIpsetUpdate, OpFwIpsetDelete,
		OpFwGroupCreate, OpFwGroupUpdate, OpFwGroupDelete,
	} {
		reasons[t] = "firewall rules and objects are not entities in the topology graph; the map cannot show them"
	}
	for _, t := range []OpType{OpIpamAllocCreate, OpIpamAllocDelete} {
		reasons[t] = "an IPAM allocation is a record inside its subnet, not an entity the map renders"
	}
	for _, t := range []OpType{OpQosShapeCreate, OpQosShapeUpdate, OpQosShapeDelete} {
		reasons[t] = "a QoS shape is app-owned intent applied with tc; it is not an entity in the topology graph"
	}
	for _, t := range []OpType{
		OpWgTunnelCreate, OpWgTunnelUpdate, OpWgTunnelDelete, OpWgPeerAdd, OpWgPeerRemove,
	} {
		reasons[t] = "WireGuard tunnels and peers are app-owned intent, never live-polled entities in the topology graph"
	}
	for _, t := range []OpType{
		OpNatMasqueradeCreate, OpNatMasqueradeDelete,
		OpNatPortForwardCreate, OpNatPortForwardUpdate, OpNatPortForwardDelete,
		OpRouteStaticCreate, OpRouteStaticUpdate, OpRouteStaticDelete,
	} {
		reasons[t] = "NAT rules and static routes live inside an existing interface's post-up lines; they are not entities of their own in the topology graph"
	}
	reasons[OpVFProvision] = "SR-IOV virtual functions are carried on their PF rather than tracked as graph entities; the pool a provision produces is only known once the node re-reads the PF"
	reasons[OpSwitchPortUpdate] = "a physical switch is an external device, not a PVE node; its ports are not entities in the topology graph"
	for _, t := range []OpType{OpSdnFabricCreate, OpSdnFabricUpdate, OpSdnFabricDelete} {
		reasons[t] = "an SDN fabric is PVE-managed underlay routing config, not a drawable entity in the topology graph; see the SDN cockpit's Fabrics view instead"
	}
	for _, t := range []OpType{OpSdnControllerCreate, OpSdnControllerUpdate, OpSdnControllerDelete} {
		reasons[t] = "an SDN controller is PVE-managed underlay control-plane config, not a drawable entity in the topology graph; see the SDN cockpit's Controllers view instead"
	}
	for _, t := range []OpType{OpSdnIpamCreate, OpSdnIpamUpdate, OpSdnIpamDelete} {
		reasons[t] = "an SDN ipam plugin instance is PVE-managed connection config, not a drawable entity in the topology graph; see the IPAM page instead"
	}
	for _, t := range []OpType{OpTcMirrorCreate, OpTcMirrorUpdate, OpTcMirrorDelete} {
		reasons[t] = "a tc.mirror session is app-owned intent applied with tc/clsact/mirred, like a QoS shape; it is not an entity in the topology graph"
	}
	return reasons
}

// entityProjection is the live entity set with a changeset's effects folded in.
//
// MUTATION DISCIPLINE, since it is what keeps the live graph safe: the entities
// in ents start out as the live snapshot's own pointers. Every mutation
// therefore goes through one of the copyOn* helpers, which install a struct copy
// first, and every slice field is REPLACED with a freshly allocated slice rather
// than appended to in place — appending to a live entity's slice can write into
// its backing array. inventory.ProjectSnapshot clones again on the way out, so
// this is belt-and-braces, but the belt is the part that stops a fold from
// corrupting the graph the whole daemon reads.
type entityProjection struct {
	ents    map[inventory.Ref]inventory.Entity
	touched map[inventory.Ref]bool
}

func newEntityProjection(snap inventory.Snapshot) *entityProjection {
	all := snap.All()
	p := &entityProjection{
		ents:    make(map[inventory.Ref]inventory.Entity, len(all)),
		touched: map[inventory.Ref]bool{},
	}
	for _, e := range all {
		p.ents[e.GetRef()] = e
	}
	return p
}

// entities returns the projected set in deterministic Ref order.
func (p *entityProjection) entities() []inventory.Entity {
	out := make([]inventory.Entity, 0, len(p.ents))
	for _, e := range p.ents {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GetRef().String() < out[j].GetRef().String() })
	return out
}

// carried returns the refs the projection did not touch — the ones whose
// provenance and raw source text remain true of them (see
// inventory.ProjectSnapshot).
func (p *entityProjection) carried() map[inventory.Ref]bool {
	out := make(map[inventory.Ref]bool, len(p.ents))
	for ref := range p.ents {
		if !p.touched[ref] {
			out[ref] = true
		}
	}
	return out
}

func (p *entityProjection) put(e inventory.Entity) {
	ref := e.GetRef()
	p.ents[ref] = e
	p.touched[ref] = true
}

func (p *entityProjection) remove(ref inventory.Ref) {
	delete(p.ents, ref)
	p.touched[ref] = true
}

// copyOnBridge returns a mutable copy of the bridge at ref, installed in the
// projection, or false when ref names no bridge (an op against something that
// does not exist projects to nothing — referential validation is what refuses
// such a changeset, not this file).
func (p *entityProjection) copyOnBridge(ref inventory.Ref) (*inventory.Bridge, bool) {
	b, ok := p.ents[ref].(*inventory.Bridge)
	if !ok {
		return nil, false
	}
	cp := *b
	p.put(&cp)
	return &cp, true
}

func (p *entityProjection) copyOnBond(ref inventory.Ref) (*inventory.Bond, bool) {
	b, ok := p.ents[ref].(*inventory.Bond)
	if !ok {
		return nil, false
	}
	cp := *b
	p.put(&cp)
	return &cp, true
}

func (p *entityProjection) copyOnVlan(ref inventory.Ref) (*inventory.VlanIface, bool) {
	v, ok := p.ents[ref].(*inventory.VlanIface)
	if !ok {
		return nil, false
	}
	cp := *v
	p.put(&cp)
	return &cp, true
}

// projectOp folds one op's effect into the projection. It returns "" when the
// op projected, or the reason it could not — the two outcomes are exhaustive
// over the op vocabulary, which is what
// TestPreview_EveryOpTypeIsProjectedOrDisclosed pins.
func (p *entityProjection) projectOp(op Op) string {
	if reason, declared := unprojectableReasons[op.Type]; declared {
		return reason
	}

	switch params := op.Params.(type) {
	case *IfaceUpdateParams:
		p.ifaceUpdate(op.Target, params)
	case *IfaceRenameParams:
		p.ifaceRename(op.Target, params.NewName)

	case *BridgeCreateParams:
		p.bridgeCreate(op.Target, params)
	case *BridgeUpdateParams:
		p.bridgeUpdate(op.Target, params)
	case *BridgeDeleteParams:
		p.remove(op.Target)
	case *BridgePortAddParams:
		if b, ok := p.copyOnBridge(op.Target); ok {
			b.DeclaredPortNames = withName(b.DeclaredPortNames, params.Port)
			b.PortNames = withName(b.PortNames, params.Port)
		}
	case *BridgePortRemoveParams:
		if b, ok := p.copyOnBridge(op.Target); ok {
			b.DeclaredPortNames = withoutName(b.DeclaredPortNames, params.Port)
			b.PortNames = withoutName(b.PortNames, params.Port)
		}

	case *BondCreateParams:
		p.bondCreate(op.Target, params)
	case *BondUpdateParams:
		p.bondUpdate(op.Target, params)
	case *BondDeleteParams:
		p.remove(op.Target)

	case *VlanCreateParams:
		p.vlanCreate(op.Target, params)
	case *VlanUpdateParams:
		p.vlanUpdate(op.Target, params)
	case *VlanDeleteParams:
		p.remove(op.Target)

	case *GuestNicUpdateParams:
		p.guestNicUpdate(op.Target, params)

	case *SdnZoneCreateParams:
		p.sdnZoneCreate(op.Target, params)
	case *SdnZoneUpdateParams:
		p.sdnZoneUpdate(op.Target, params)
	case *SdnZoneDeleteParams:
		p.remove(op.Target)
	case *SdnVnetCreateParams:
		p.put(&inventory.SdnVnet{
			Ref: op.Target, ID: op.Target.ID, Zone: params.Zone,
			Alias: params.Alias, Tag: params.Tag, VlanAware: params.VlanAware,
		})
	case *SdnVnetUpdateParams:
		p.sdnVnetUpdate(op.Target, params)
	case *SdnVnetDeleteParams:
		p.remove(op.Target)
	case *SdnSubnetCreateParams:
		p.put(&inventory.SdnSubnet{
			Ref: op.Target, ID: op.Target.ID, Vnet: params.Vnet, Gateway: params.Gateway,
			DNSZonePrefix: params.DNSZonePrefix,
			DHCPRanges:    append([]string(nil), params.DHCPRanges...), SNAT: params.SNAT,
		})
	case *SdnSubnetUpdateParams:
		p.sdnSubnetUpdate(op.Target, params)
	case *SdnSubnetDeleteParams:
		p.remove(op.Target)

	// sdn.dns.zone.* projects an inventory.SdnDnsServer, not an
	// inventory.SdnDnsZone (T-4112). These three ops manage a
	// /cluster/sdn/dns entry, which is a PowerDNS server CONNECTION; an
	// SdnDnsZone is a DNS DOMAIN, derived from an SDN zone's dnszone field
	// and its subnets' reverse zones.
	//
	// Projecting one as the other put a fabricated domain, named after the
	// connection, into the projected graph — and validate_projection.go's
	// record check asks that same index whether a record's zone exists, so
	// the fabrication let a record op targeting a non-existent domain
	// validate clean and then fail at apply.
	//
	// The target ref still carries KindSDNDnsZone; T-4114 renames the op
	// family and the kind together. See inventory.SdnDnsServer's comment for
	// why the two cannot collide in the meantime.
	case *SdnDnsZoneCreateParams:
		p.put(&inventory.SdnDnsServer{
			Ref: op.Target, ID: op.Target.ID, Type: params.Type, URL: params.URL,
			Fingerprint: params.Fingerprint, TTL: params.TTL, ReverseMaskV6: params.ReverseMaskV6,
		})
	case *SdnDnsZoneUpdateParams:
		p.sdnDnsServerUpdate(op.Target, params)
	case *SdnDnsZoneDeleteParams:
		p.remove(op.Target)
	case *SdnDnsRecordCreateParams:
		p.put(&inventory.SdnDnsRecord{
			Ref: op.Target, Zone: params.Zone, Name: params.Name,
			Type: params.Type, Value: params.Value, TTL: params.TTL,
		})
	case *SdnDnsRecordUpdateParams:
		p.sdnDnsRecordUpdate(op.Target, params)
	case *SdnDnsRecordDeleteParams:
		p.remove(op.Target)

	default:
		// Reached only if an op type is added to the vocabulary with neither a
		// projection rule nor a declared reason. The disclosure still names it,
		// so the preview degrades to honest rather than to silently wrong.
		return "vnprox has no rule for projecting this op onto the topology graph"
	}
	return ""
}

// --- per-op folds ---------------------------------------------------------

// ifaceUpdate folds an iface.update onto whichever iface-namespace entity the
// target names. The declared fields are the ones an interfaces(5) update
// actually changes; runtime MTU is left alone, because a reload does not
// guarantee the kernel accepts the declared value (the map keeps showing the
// last observed one, which is the honest rendering of "not yet applied").
func (p *entityProjection) ifaceUpdate(ref inventory.Ref, params *IfaceUpdateParams) {
	switch e := p.ents[ref].(type) {
	case *inventory.PhysNic:
		cp := *e
		if params.MTU != nil {
			cp.MTUDeclared = *params.MTU
		}
		p.put(&cp)
	case *inventory.Bridge:
		cp := *e
		applyIfaceUpdateToAddrs(params, &cp.Addresses, &cp.Gateway)
		if params.MTU != nil {
			cp.MTUDeclared = *params.MTU
		}
		if params.Comments != nil {
			cp.Comments = *params.Comments
		}
		p.put(&cp)
	case *inventory.Bond:
		cp := *e
		if params.MTU != nil {
			cp.MTUDeclared = *params.MTU
		}
		p.put(&cp)
	case *inventory.VlanIface:
		cp := *e
		var gw string
		applyIfaceUpdateToAddrs(params, &cp.Addresses, &gw)
		if params.MTU != nil {
			cp.MTUDeclared = *params.MTU
		}
		p.put(&cp)
	}
}

// applyIfaceUpdateToAddrs mirrors ifaces.mutateIfaceUpdate's precedence: an
// explicit address list wins, and RemoveAddress is honored only when no list
// was supplied (params_iface.go's own doc comment).
func applyIfaceUpdateToAddrs(params *IfaceUpdateParams, addrs *[]string, gateway *string) {
	switch {
	case params.Addresses != nil:
		*addrs = append([]string(nil), (*params.Addresses)...)
	case params.RemoveAddress:
		*addrs = nil
	}
	switch {
	case params.Gateway != nil:
		*gateway = *params.Gateway
	case params.RemoveGateway:
		*gateway = ""
	}
}

// ifaceRename moves a logical iface to a new identity and rewrites every
// same-node reference to its old name.
//
// Guest NIC attachments are deliberately NOT rewritten: a rename touches the
// node's interfaces file, never a guest's PVE config, so a guest attached to
// the old name really would be left dangling — which is exactly why
// validate_safety.go blocks renaming an interface with guests on it. Quietly
// re-pointing the guest here would show an operator a map in which the
// interlock's whole reason for existing had disappeared.
func (p *entityProjection) ifaceRename(ref inventory.Ref, newName string) {
	if newName == "" || newName == ref.ID {
		return
	}
	newRef := inventory.Ref{Kind: ref.Kind, Node: ref.Node, ID: newName}
	switch e := p.ents[ref].(type) {
	case *inventory.Bridge:
		cp := *e
		cp.Ref, cp.Name = newRef, newName
		p.remove(ref)
		p.put(&cp)
	case *inventory.Bond:
		cp := *e
		cp.Ref, cp.Name = newRef, newName
		p.remove(ref)
		p.put(&cp)
	case *inventory.VlanIface:
		cp := *e
		cp.Ref, cp.Name = newRef, newName
		p.remove(ref)
		p.put(&cp)
	default:
		return
	}

	for otherRef := range p.ents {
		if otherRef.Node != ref.Node || otherRef == newRef {
			continue
		}
		switch e := p.ents[otherRef].(type) {
		case *inventory.Bridge:
			if !containsName(e.PortNames, ref.ID) && !containsName(e.DeclaredPortNames, ref.ID) {
				continue
			}
			cp := *e
			cp.PortNames = renamedIn(e.PortNames, ref.ID, newName)
			cp.DeclaredPortNames = renamedIn(e.DeclaredPortNames, ref.ID, newName)
			p.put(&cp)
		case *inventory.Bond:
			if !containsName(e.Slaves, ref.ID) && !containsName(e.DeclaredSlaves, ref.ID) {
				continue
			}
			cp := *e
			cp.Slaves = renamedIn(e.Slaves, ref.ID, newName)
			cp.DeclaredSlaves = renamedIn(e.DeclaredSlaves, ref.ID, newName)
			p.put(&cp)
		case *inventory.VlanIface:
			if e.ParentName != ref.ID {
				continue
			}
			cp := *e
			cp.ParentName = newName
			p.put(&cp)
		}
	}
}

func (p *entityProjection) bridgeCreate(ref inventory.Ref, params *BridgeCreateParams) {
	virt := inventory.BridgeLinux
	if ref.Kind == inventory.KindOVSBridge {
		virt = inventory.BridgeOVS
	}
	b := &inventory.Bridge{
		Ref: ref, Name: ref.ID, Virt: virt,
		Gateway: params.Gateway, Comments: params.Comments,
		Addresses: append([]string(nil), params.Addresses...),
		Vids:      toInventoryVids(params.Vids),
		// Declared and runtime port membership are both set: after the apply
		// and reload this bridge really does carry these ports, and the map
		// draws structure from runtime membership first (inventory/link.go).
		// Leaving the runtime list empty would render the new bridge with no
		// ports at all — the change shown as not having happened.
		DeclaredPortNames: append([]string(nil), params.Ports...),
		PortNames:         append([]string(nil), params.Ports...),
		MTUDeclared:       params.MTU,
		VlanAware:         params.VlanAware, VlanAwareSet: true,
		STP: params.STP, STPSet: true,
	}
	p.put(b)
}

func (p *entityProjection) bridgeUpdate(ref inventory.Ref, params *BridgeUpdateParams) {
	b, ok := p.copyOnBridge(ref)
	if !ok {
		return
	}
	if params.Addresses != nil {
		b.Addresses = append([]string(nil), (*params.Addresses)...)
	}
	if params.Vids != nil {
		b.Vids = toInventoryVids(*params.Vids)
	}
	if params.Gateway != nil {
		b.Gateway = *params.Gateway
	}
	if params.Comments != nil {
		b.Comments = *params.Comments
	}
	if params.MTU != nil {
		b.MTUDeclared = *params.MTU
	}
	if params.VlanAware != nil {
		b.VlanAware, b.VlanAwareSet = *params.VlanAware, true
	}
	if params.STP != nil {
		b.STP, b.STPSet = *params.STP, true
	}
}

func (p *entityProjection) bondCreate(ref inventory.Ref, params *BondCreateParams) {
	p.put(&inventory.Bond{
		Ref: ref, Name: ref.ID, Mode: params.Mode,
		LACPRate: params.LACPRate, XmitHashPolicy: params.XmitHashPolicy,
		// See bridgeCreate's note on declared-vs-runtime membership.
		DeclaredSlaves: append([]string(nil), params.Slaves...),
		Slaves:         append([]string(nil), params.Slaves...),
		MTUDeclared:    params.MTU,
	})
}

func (p *entityProjection) bondUpdate(ref inventory.Ref, params *BondUpdateParams) {
	b, ok := p.copyOnBond(ref)
	if !ok {
		return
	}
	if params.Slaves != nil {
		b.DeclaredSlaves = append([]string(nil), (*params.Slaves)...)
		b.Slaves = append([]string(nil), (*params.Slaves)...)
		b.SlaveDetail = nil
	}
	if params.Mode != nil {
		b.Mode = *params.Mode
	}
	if params.LACPRate != nil {
		b.LACPRate = *params.LACPRate
	}
	if params.XmitHashPolicy != nil {
		b.XmitHashPolicy = *params.XmitHashPolicy
	}
	if params.MTU != nil {
		b.MTUDeclared = *params.MTU
	}
}

func (p *entityProjection) vlanCreate(ref inventory.Ref, params *VlanCreateParams) {
	v := &inventory.VlanIface{
		Ref: ref, Name: ref.ID, ParentName: params.Parent, Vid: params.Vid,
		Addresses:   append([]string(nil), params.Addresses...),
		Trunks:      toInventoryVids(params.Trunks),
		MTUDeclared: params.MTU,
	}
	if params.OVS {
		v.Virt = "ovs"
	}
	p.put(v)
}

func (p *entityProjection) vlanUpdate(ref inventory.Ref, params *VlanUpdateParams) {
	v, ok := p.copyOnVlan(ref)
	if !ok {
		return
	}
	if params.Addresses != nil {
		v.Addresses = append([]string(nil), (*params.Addresses)...)
	}
	if params.MTU != nil {
		v.MTUDeclared = *params.MTU
	}
}

// guestNicUpdate re-declares the NIC's attachment target by NAME only:
// BridgeOrVnet and EffectiveVid are derived Refs, re-resolved by linkAll
// against the projected entity set, so a reattachment to a bridge this same
// changeset creates resolves exactly the way it will once applied.
func (p *entityProjection) guestNicUpdate(ref inventory.Ref, params *GuestNicUpdateParams) {
	nic, ok := p.ents[ref].(*inventory.GuestNic)
	if !ok {
		return
	}
	cp := *nic
	if params.BridgeOrVnet != nil {
		cp.TargetName = *params.BridgeOrVnet
		cp.BridgeOrVnet = inventory.Ref{}
	}
	if params.Vid != nil {
		cp.Vid = *params.Vid
	}
	if params.RateMbps != nil {
		cp.RateMbps = *params.RateMbps
	}
	if params.Firewall != nil {
		cp.Firewall = *params.Firewall
	}
	if params.LinkDown != nil {
		cp.LinkDown = *params.LinkDown
	}
	p.put(&cp)
}

func (p *entityProjection) sdnZoneCreate(ref inventory.Ref, params *SdnZoneCreateParams) {
	p.put(&inventory.SdnZone{
		Ref: ref, ID: ref.ID, Type: params.Type, Bridge: params.Bridge,
		Controller: params.Controller, IPAM: params.IPAM,
		Nodes:     append([]string(nil), params.Nodes...),
		ExitNodes: append([]string(nil), params.ExitNodes...),
		Peers:     append([]string(nil), params.Peers...),
		VrfVxlan:  params.VrfVxlan, MTU: params.MTU,
	})
}

func (p *entityProjection) sdnZoneUpdate(ref inventory.Ref, params *SdnZoneUpdateParams) {
	z, ok := p.ents[ref].(*inventory.SdnZone)
	if !ok {
		return
	}
	cp := *z
	if params.Bridge != nil {
		cp.Bridge = *params.Bridge
	}
	if params.Controller != nil {
		cp.Controller = *params.Controller
	}
	if params.IPAM != nil {
		cp.IPAM = *params.IPAM
	}
	if params.Nodes != nil {
		cp.Nodes = append([]string(nil), (*params.Nodes)...)
	}
	if params.ExitNodes != nil {
		cp.ExitNodes = append([]string(nil), (*params.ExitNodes)...)
	}
	if params.Peers != nil {
		cp.Peers = append([]string(nil), (*params.Peers)...)
	}
	if params.VrfVxlan != nil {
		cp.VrfVxlan = *params.VrfVxlan
	}
	if params.MTU != nil {
		cp.MTU = *params.MTU
	}
	p.put(&cp)
}

func (p *entityProjection) sdnVnetUpdate(ref inventory.Ref, params *SdnVnetUpdateParams) {
	v, ok := p.ents[ref].(*inventory.SdnVnet)
	if !ok {
		return
	}
	cp := *v
	if params.Alias != nil {
		cp.Alias = *params.Alias
	}
	if params.Tag != nil {
		cp.Tag = *params.Tag
	}
	if params.VlanAware != nil {
		cp.VlanAware = *params.VlanAware
	}
	p.put(&cp)
}

func (p *entityProjection) sdnSubnetUpdate(ref inventory.Ref, params *SdnSubnetUpdateParams) {
	s, ok := p.ents[ref].(*inventory.SdnSubnet)
	if !ok {
		return
	}
	cp := *s
	if params.Gateway != nil {
		cp.Gateway = *params.Gateway
	}
	if params.DNSZonePrefix != nil {
		cp.DNSZonePrefix = *params.DNSZonePrefix
	}
	if params.DHCPRanges != nil {
		cp.DHCPRanges = append([]string(nil), (*params.DHCPRanges)...)
	}
	if params.SNAT != nil {
		cp.SNAT = *params.SNAT
	}
	p.put(&cp)
}

func (p *entityProjection) sdnDnsServerUpdate(ref inventory.Ref, params *SdnDnsZoneUpdateParams) {
	s, ok := p.ents[ref].(*inventory.SdnDnsServer)
	if !ok {
		return
	}
	cp := *s
	if params.Type != nil {
		cp.Type = *params.Type
	}
	if params.URL != nil {
		cp.URL = *params.URL
	}
	if params.Fingerprint != nil {
		cp.Fingerprint = *params.Fingerprint
	}
	if params.TTL != nil {
		cp.TTL = *params.TTL
	}
	if params.ReverseMaskV6 != nil {
		cp.ReverseMaskV6 = *params.ReverseMaskV6
	}
	// Key is not projected: see inventory.SdnDnsServer's comment. An update
	// that rotates the key shows as no field change here, which is correct —
	// a preview must not display a secret, and "the key changed" is not
	// something a diff can say without saying what it changed to.
	p.put(&cp)
}

func (p *entityProjection) sdnDnsRecordUpdate(ref inventory.Ref, params *SdnDnsRecordUpdateParams) {
	r, ok := p.ents[ref].(*inventory.SdnDnsRecord)
	if !ok {
		return
	}
	cp := *r
	if params.Value != nil {
		cp.Value = *params.Value
	}
	if params.TTL != nil {
		cp.TTL = *params.TTL
	}
	p.put(&cp)
}

// --- small slice helpers (every one returns a FRESH slice) -----------------

func withName(names []string, name string) []string {
	if name == "" || containsName(names, name) {
		return append([]string(nil), names...)
	}
	return append(append([]string(nil), names...), name)
}

func withoutName(names []string, name string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if n != name {
			out = append(out, n)
		}
	}
	return out
}

func renamedIn(names []string, oldName, newName string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if n == oldName {
			n = newName
		}
		out = append(out, n)
	}
	return out
}

func containsName(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

func toInventoryVids(vids []VidRange) []inventory.VidRange {
	if len(vids) == 0 {
		return nil
	}
	out := make([]inventory.VidRange, len(vids))
	for i, v := range vids {
		out[i] = inventory.VidRange{Low: v.Low, High: v.High}
	}
	return out
}

// --- the projection itself -------------------------------------------------

// ProjectOps folds ops onto snap and returns the projected snapshot plus every
// op that could not be projected. It is pure: snap is never mutated, and
// nothing outside memory is touched.
func ProjectOps(ops []Op, snap inventory.Snapshot) (inventory.Snapshot, []UnprojectableOp) {
	p := newEntityProjection(snap)
	unprojectable := []UnprojectableOp{}
	for _, op := range ops {
		if reason := p.projectOp(op); reason != "" {
			unprojectable = append(unprojectable, UnprojectableOp{
				OpID: op.ID, Op: string(op.Type), Target: refString(op.Target), Reason: reason,
			})
		}
	}
	return inventory.ProjectSnapshot(snap, p.entities(), p.carried()), unprojectable
}

// ComputePreview builds the full post-apply preview of ops against snap.
//
// The change list is computed with topology.DiffPoints — the same comparison
// T-2704's point-in-time diff uses — so "what is different about the projected
// map" is answered by one implementation rather than two that could disagree.
func ComputePreview(ops []Op, snap inventory.Snapshot) (Preview, error) {
	projected, unprojectable := ProjectOps(ops, snap)

	before, err := topology.PointEntities(snap.All())
	if err != nil {
		return Preview{}, fmt.Errorf("change: flattening the live snapshot for preview: %w", err)
	}
	after, err := topology.PointEntities(projected.All())
	if err != nil {
		return Preview{}, fmt.Errorf("change: flattening the projected snapshot for preview: %w", err)
	}

	diffs := topology.DiffPoints(before, after)
	changes := make([]PreviewChange, 0, len(diffs))
	for _, d := range diffs {
		changes = append(changes, PreviewChange{
			Ref: d.Ref, Kind: d.Kind, Node: d.Node, Name: d.Name,
			Change: d.Change, Fields: d.Fields,
		})
	}

	return Preview{
		Topology:      topology.Project(projected, topology.Filter{}),
		Changes:       changes,
		Unprojectable: unprojectable,
		BestEffort:    true,
		GeneratedAt:   projected.GeneratedAt().Unix(),
	}, nil
}

// Preview computes the post-apply topology preview for a changeset by id
// (T-2605, GET /changesets/{id}/preview).
//
// It REFUSES to project a changeset with blocking validation findings
// (*ErrValidationBlocked → 422 validation_failed, carrying the findings). A
// changeset that cannot apply has no post-apply state; rendering one anyway
// would put a map on screen that no sequence of events can ever produce, and
// the operator has no way to tell it apart from one that can. Validation runs
// against the live snapshot at request time, exactly as the apply path
// revalidates, so a draft that went stale is refused here too.
//
// Read-only: it reads the changeset and the inventory snapshot and returns
// values. It writes no store row (not even an audit row — a preview is a read,
// and a read that logged would make the audit trail's "what happened to this
// changeset" story noisier without adding a single fact about it), calls no
// node agent, and takes no PVE gateway.
func (s *Service) Preview(ctx context.Context, id string) (Preview, error) {
	c, err := s.Get(ctx, id)
	if err != nil {
		return Preview{}, err
	}
	snap := s.inventorySnapshot()
	if findings := s.validateScoped(ctx, c.ClusterID, id, c.Ops); hasError(findings) {
		return Preview{}, &ErrValidationBlocked{Findings: findings}
	}
	preview, err := ComputePreview(c.Ops, snap)
	if err != nil {
		return Preview{}, fmt.Errorf("change: previewing changeset %s: %w", id, err)
	}
	preview.ChangesetID = c.ID
	return preview, nil
}

// PreviewSummary renders the preview as the markdown block T-2702's pull-request
// body carries (gitsync.PreviewSource). It is the same computation the route
// serves — a proposal that described a different projection from the one the UI
// shows would be worse than one that described none.
func (s *Service) PreviewSummary(ctx context.Context, changesetID string) (string, error) {
	preview, err := s.Preview(ctx, changesetID)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString("Projected from the live inventory graph with this changeset's ops folded in. ")
	sb.WriteString("**Best-effort:** it is a projection, not a promise.\n\n")

	if len(preview.Changes) == 0 {
		sb.WriteString("- The map is unchanged: no entity is added, removed or modified.\n")
	}
	for _, ch := range preview.Changes {
		name := ch.Name
		if name == "" {
			name = ch.Ref
		}
		fmt.Fprintf(&sb, "- **%s** `%s` (%s)", ch.Change, ch.Ref, name)
		if len(ch.Fields) > 0 {
			parts := make([]string, 0, len(ch.Fields))
			for _, f := range ch.Fields {
				parts = append(parts, fmt.Sprintf("%s: %q → %q", f.Field, f.Before, f.After))
			}
			sb.WriteString(" — " + strings.Join(parts, ", "))
		}
		sb.WriteString("\n")
	}

	if len(preview.Unprojectable) > 0 {
		sb.WriteString("\nNot projected:\n\n")
		for _, u := range preview.Unprojectable {
			fmt.Fprintf(&sb, "- `%s %s` — %s\n", u.Op, u.Target, u.Reason)
		}
	}
	return sb.String(), nil
}
