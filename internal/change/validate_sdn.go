package change

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// underlayMTU is the assumed default underlay path MTU (standard Ethernet)
// a VXLAN/EVPN zone's encapsulated traffic rides over, absent any more
// precise per-node/per-peer-route MTU discovery — resolving the *actual*
// path MTU to every configured peer address would mean walking each
// member node's routing table to find the outbound interface for each peer
// IP (host-reader/collector work no task before T-402 does), so this is a
// documented, flagged simplification: the same "underlay MTU − 50" figure
// docs/features/sdn.md §2's VXLAN wizard worked example uses verbatim, and
// the number real PVE's own VXLAN zone default (1450) assumes.
const underlayMTU = 1500

// vxlanOverhead is VXLAN's encapsulation header size (docs/features/sdn.md
// §2: "MTU math shown explicitly (underlay MTU − 50)").
const vxlanOverhead = 50

// VxlanOverhead exports vxlanOverhead for internal/findings' `vxlan_underlay_mtu`
// continuous health check (T-803), which promotes this exact encapsulation-
// overhead sanity math to a continuous check against the *observed* underlay
// path MTU (real PhysNic/Bond MTU from inventory) rather than only running it
// against the assumed default (underlayMTU above) at changeset-validate time
// — see checkVxlanMTU's doc comment for why an assumed default is used here
// at all. Reusing this constant (rather than internal/findings duplicating
// the literal 50) keeps the two checks' math from silently drifting apart.
const VxlanOverhead = vxlanOverhead

// sdnValidate is T-402's pre-apply validator class (docs/features/sdn.md
// §4: "vnprox wraps the SDN apply ... with: pre-apply validation (zone node
// coverage, bridge existence on member nodes, MTU sanity)"). "Zone node
// coverage" — every node a zone names must be a real cluster node — is
// already fully covered by T-202's referential class (validate_referential.
// go's SdnZoneCreateParams/UpdateParams cases, codeNodeNotFound): an empty
// `nodes` list is deliberately valid (real PVE semantics: no restriction =
// every cluster node — see validate_test.go's "clean: sdn.zone.create with
// no nodes restriction" golden case), so there is nothing left for this
// class to add there. What T-402 does add: bridge existence on member
// nodes and tag uniqueness — real PVE would itself fail to apply a zone
// that fails either, so both are SeverityError and — per
// ValidateWithSafety's documented short-circuit ordering — run right after
// the referential class (targets exist) and before the safety-interlock
// class. (VXLAN MTU sanity is advisory — see validate_advisory.go's
// checkVxlanMTU — since an over-large MTU degrades rather than breaks an
// apply, and the vnet-deletion-with-attached-guests guard is safety-
// interlock-shaped — see validate_safety.go's vnetDeletionGuardFindings,
// which honors AllowDangerousOps exactly like its bridge counterpart.)
//
// Every check here evaluates against snap plus this changeset's own ops
// (via the shared projection type every other class folds through), never a
// live PVE read (this package's validators are pure — see validate.go's
// doc comment): a bridge a simple/vlan zone assumes may be one this same
// changeset also creates (bridge.create), and that is honored correctly.
func sdnValidate(ops []Op, snap inventory.Snapshot) []Finding {
	proj := newProjection(snap)
	for _, op := range ops {
		proj.fold(op)
	}

	var out []Finding
	out = append(out, zoneBridgeExistenceFindings(ops, snap, proj)...)
	out = append(out, vnetTagUniquenessFindings(ops, snap)...)
	out = append(out, snatRequiresGatewayFindings(ops, snap)...)
	out = append(out, vniRequiredFindings(ops, snap)...)
	return out
}

// effectiveVnetTags resolves every known/folded vnet's effective tag (the
// VNI, for vxlan/evpn zones), starting from snap's SdnVnet entities and
// folding this changeset's vnet.create/update/delete ops forward.
func effectiveVnetTags(ops []Op, snap inventory.Snapshot) map[string]int {
	tags := map[string]int{}
	for _, e := range snap.All() {
		if v, ok := e.(*inventory.SdnVnet); ok {
			tags[v.ID] = v.Tag
		}
	}
	for _, op := range ops {
		switch p := op.Params.(type) {
		case *SdnVnetCreateParams:
			tags[op.Target.ID] = p.Tag
		case *SdnVnetUpdateParams:
			if p.Tag != nil {
				tags[op.Target.ID] = *p.Tag
			}
		case *SdnVnetDeleteParams:
			delete(tags, op.Target.ID)
		}
	}
	return tags
}

// vniRequiredFindings flags a vnet this changeset creates/updates that ends
// up in a vxlan/evpn zone with an effective tag of 0 — real PVE requires a
// VNI for those zone types (codeSDNVNIRequired). Only vnets the changeset
// itself touches are reported (mirroring vnetTagUniquenessFindings), and
// the zone type is resolved net-effect-aware so a vnet and the vxlan/evpn
// zone that gives it its type, created in the same changeset, are honored.
func vniRequiredFindings(ops []Op, snap inventory.Snapshot) []Finding {
	zones := effectiveZones(ops, snap)
	vnetZones := effectiveVnetZones(ops, snap)
	tags := effectiveVnetTags(ops, snap)

	seen := map[string]bool{}
	var out []Finding
	for _, op := range ops {
		// Fire on a create, or on an update that *explicitly* sets the tag
		// (to 0) — an update that leaves the tag alone must not surface a
		// pre-existing missing VNI the user didn't touch this changeset.
		switch op.Type {
		case OpSdnVnetCreate:
		case OpSdnVnetUpdate:
			p, ok := op.Params.(*SdnVnetUpdateParams)
			if !ok || p.Tag == nil {
				continue
			}
		default:
			continue
		}
		id := op.Target.ID
		if seen[id] {
			continue
		}
		seen[id] = true

		zoneID, ok := vnetZones[id]
		if !ok {
			continue
		}
		z, ok := zones[zoneID]
		if !ok {
			continue // referential class flags the missing zone
		}
		if z.typ != "vxlan" && z.typ != "evpn" {
			continue
		}
		if tags[id] != 0 {
			continue
		}
		ref := inventory.Ref{Kind: inventory.KindSDNVnet, ID: id}.String()
		out = append(out, errorf(codeSDNVNIRequired, ref,
			"vnet %q is in a %s zone but has no VNI — PVE requires a VNI for a %s vnet", id, z.typ, z.typ))
	}
	return out
}

// effectiveZone is one zone's (type, bridge, nodes, exitNodes) as of "now"
// — the base snapshot with every zone.create/update/delete op in this
// changeset folded in, in order. exitNodes was added by T-701 for
// validate_advisory.go's evpnSubnetAdvisoryFindings (codeSNATRequiresExitNode
// needs an EVPN zone's *effective* exit-node list, cross-checked against
// what the same changeset's own zone.update may have just changed — T-701
// acceptance criterion 3's "cross-checked against the wizard's own
// exitNodes selection live").
type effectiveZone struct {
	typ       string
	bridge    string
	nodes     []string
	exitNodes []string
}

// effectiveZones resolves every zone's effective (type, bridge, nodes,
// exitNodes), starting from snap's existing SdnZone entities and folding
// ops forward.
func effectiveZones(ops []Op, snap inventory.Snapshot) map[string]effectiveZone {
	zones := map[string]effectiveZone{}
	for _, e := range snap.All() {
		z, ok := e.(*inventory.SdnZone)
		if !ok {
			continue
		}
		zones[z.ID] = effectiveZone{typ: z.Type, bridge: z.Bridge, nodes: z.Nodes, exitNodes: z.ExitNodes}
	}
	for _, op := range ops {
		switch p := op.Params.(type) {
		case *SdnZoneCreateParams:
			zones[op.Target.ID] = effectiveZone{typ: p.Type, bridge: p.Bridge, nodes: p.Nodes, exitNodes: p.ExitNodes}
		case *SdnZoneUpdateParams:
			z := zones[op.Target.ID]
			if p.Bridge != nil {
				z.bridge = *p.Bridge
			}
			if p.Nodes != nil {
				z.nodes = *p.Nodes
			}
			if p.ExitNodes != nil {
				z.exitNodes = *p.ExitNodes
			}
			zones[op.Target.ID] = z
		case *SdnZoneDeleteParams:
			delete(zones, op.Target.ID)
		}
	}
	return zones
}

// effectiveVnetZones maps every known/folded sdn-vnet id to its owning
// zone id, "as of now". A vnet's zone assignment is immutable after create
// (SdnVnetUpdateParams carries no Zone field — params_sdn.go's doc
// comment), so only create/delete ops need folding.
func effectiveVnetZones(ops []Op, snap inventory.Snapshot) map[string]string {
	vnets := map[string]string{}
	for _, e := range snap.All() {
		v, ok := e.(*inventory.SdnVnet)
		if !ok {
			continue
		}
		vnets[v.ID] = v.Zone
	}
	for _, op := range ops {
		switch p := op.Params.(type) {
		case *SdnVnetCreateParams:
			vnets[op.Target.ID] = p.Zone
		case *SdnVnetDeleteParams:
			delete(vnets, op.Target.ID)
		}
	}
	return vnets
}

// effectiveSubnet is one subnet's (vnet, gateway, snat) as of "now", plus
// the most recent op that touched it in this changeset (lastOp, zero Op if
// only the snapshot established it) — lastOp is what
// snatRequiresGatewayFindings' fix patch replaces, so the fix always has
// the same (Type, Target) pair as the op the finding is actually about
// (validate_fix.go's documented property).
type effectiveSubnet struct {
	lastOp  Op
	vnet    string
	gateway string
	snat    bool
	touched bool
}

// effectiveSubnets resolves every subnet's effective (vnet, gateway, snat),
// starting from snap's existing SdnSubnet entities (CIDR is always the map
// key/Ref.ID — docs/data-model.md's SdnSubnet.ID doc comment) and folding
// this changeset's own subnet.create/update/delete ops forward, in order.
func effectiveSubnets(ops []Op, snap inventory.Snapshot) map[string]effectiveSubnet {
	subnets := map[string]effectiveSubnet{}
	for _, e := range snap.All() {
		s, ok := e.(*inventory.SdnSubnet)
		if !ok {
			continue
		}
		subnets[s.ID] = effectiveSubnet{vnet: s.Vnet, gateway: s.Gateway, snat: s.SNAT}
	}
	for _, op := range ops {
		switch p := op.Params.(type) {
		case *SdnSubnetCreateParams:
			subnets[op.Target.ID] = effectiveSubnet{vnet: p.Vnet, gateway: p.Gateway, snat: p.SNAT, lastOp: op, touched: true}
		case *SdnSubnetUpdateParams:
			s := subnets[op.Target.ID]
			if p.Gateway != nil {
				s.gateway = *p.Gateway
			}
			if p.SNAT != nil {
				s.snat = *p.SNAT
			}
			s.lastOp = op
			s.touched = true
			subnets[op.Target.ID] = s
		case *SdnSubnetDeleteParams:
			delete(subnets, op.Target.ID)
		}
	}
	return subnets
}

// touchedSubnetIDs returns, in first-appearance order and deduplicated,
// every subnet id this changeset's own subnet.create/update ops name —
// mirrors vnetTagUniquenessFindings' touchedOrder convention (only report a
// finding for what the user actually touched this changeset, even though
// the effective state a check evaluates may include untouched snapshot
// data or a sibling op's contribution).
func touchedSubnetIDs(ops []Op) []string {
	seen := map[string]bool{}
	var out []string
	for _, op := range ops {
		switch op.Type {
		case OpSdnSubnetCreate, OpSdnSubnetUpdate:
			if !seen[op.Target.ID] {
				seen[op.Target.ID] = true
				out = append(out, op.Target.ID)
			}
		}
	}
	return out
}

// snatRequiresGatewayFindings is T-701 acceptance criterion 2's blocking
// check: a subnet whose effective state has snat=true but no effective
// gateway (net-effect-aware — a subnet.create with no gateway followed by
// a subnet.update that flips snat on in the same changeset is still
// caught, and a subnet.update that supplies both in one op is too). Real
// PVE's SubnetPlugin rejects this shape at subnet stage time (T-701
// root-cause analysis §4), so — like zoneBridgeExistenceFindings/
// vnetTagUniquenessFindings above — this is SeverityError, not advisory.
// The fix patch is attached to the *last* op that touched the subnet (see
// effectiveSubnet.lastOp's doc comment) so it always shares that op's
// (Type, Target) pair, matching validate_fix.go's documented convention.
func snatRequiresGatewayFindings(ops []Op, snap inventory.Snapshot) []Finding {
	subnets := effectiveSubnets(ops, snap)
	var out []Finding
	for _, id := range touchedSubnetIDs(ops) {
		s, ok := subnets[id]
		if !ok || !s.touched || !s.snat || s.gateway != "" {
			continue
		}
		ref := inventory.Ref{Kind: inventory.KindSDNSubnet, ID: id}.String()
		f := errorf(codeSNATRequiresGateway, ref, "subnet %s has snat enabled but no gateway — PVE rejects SNAT without a gateway", id)
		f.Fix = fixSetSubnetGateway(s.lastOp, id)
		out = append(out, f)
	}
	return out
}

// zoneBridgeExistenceFindings flags a simple/vlan zone.create/update op
// whose effective bridge does not exist (as of this changeset's own net
// effect, including any bridge.create it also carries) on one or more of
// its effective member nodes — docs/features/sdn.md §4's "bridge existence
// on member nodes". Only zone types that assume a real, shared per-node
// bridge (simple, vlan) are checked; vxlan/evpn zones realize per-node
// bridges PVE itself creates, not a pre-existing one (see the T-401
// report's identical reasoning for why EdgeRealizes only applies to
// simple/vlan/qinq zones), and qinq zones share simple/vlan's shape but
// this codebase's SDNZoneSpec has no separate qinq-specific bridge
// semantics to distinguish, so qinq is intentionally left unchecked here
// rather than guessed at.
func zoneBridgeExistenceFindings(ops []Op, snap inventory.Snapshot, proj *projection) []Finding {
	eff := effectiveZones(ops, snap)

	touched := map[string]bool{}
	for _, op := range ops {
		if op.Type == OpSdnZoneCreate || op.Type == OpSdnZoneUpdate {
			touched[op.Target.ID] = true
		}
	}
	ids := make([]string, 0, len(touched))
	for id := range touched {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var out []Finding
	for _, id := range ids {
		z := eff[id]
		if z.typ != "simple" && z.typ != "vlan" {
			continue
		}
		if z.bridge == "" {
			continue
		}
		ref := inventory.Ref{Kind: inventory.KindSDNZone, ID: id}.String()
		for _, node := range z.nodes {
			if _, ok := proj.ifaceRef(node, z.bridge); !ok {
				out = append(out, errorf(codeSDNBridgeMissing, ref,
					"zone %q assumes bridge %q on node %s, which does not exist there", id, z.bridge, node))
			}
		}
	}
	return out
}

// vnetTagUniquenessFindings flags a vnet whose effective tag (VLAN ID for a
// vlan zone, VNI for vxlan/evpn) collides with a sibling vnet's in the same
// zone — docs/features/sdn.md §4's "MTU sanity" neighbor check, tag
// uniqueness, listed alongside it in T-402's card: two VNets sharing one
// zone's tag namespace is a PVE apply-time conflict, not merely a style
// nit. Only vnets this changeset actually creates or updates are reported
// (each once), even though the collision may be against an untouched
// sibling — reporting every untouched sibling too would be noise for
// something the user didn't touch.
func vnetTagUniquenessFindings(ops []Op, snap inventory.Snapshot) []Finding {
	type vnetInfo struct {
		zone string
		tag  int
	}
	vnets := map[string]vnetInfo{}
	for _, e := range snap.All() {
		v, ok := e.(*inventory.SdnVnet)
		if !ok {
			continue
		}
		vnets[v.ID] = vnetInfo{zone: v.Zone, tag: v.Tag}
	}

	var touchedOrder []string
	for _, op := range ops {
		switch p := op.Params.(type) {
		case *SdnVnetCreateParams:
			vnets[op.Target.ID] = vnetInfo{zone: p.Zone, tag: p.Tag}
			touchedOrder = append(touchedOrder, op.Target.ID)
		case *SdnVnetUpdateParams:
			vi := vnets[op.Target.ID]
			if p.Tag != nil {
				vi.tag = *p.Tag
			}
			vnets[op.Target.ID] = vi
			touchedOrder = append(touchedOrder, op.Target.ID)
		case *SdnVnetDeleteParams:
			delete(vnets, op.Target.ID)
		}
	}
	if len(touchedOrder) == 0 {
		return nil
	}

	byZoneTag := map[string][]string{}
	for id, vi := range vnets {
		if vi.tag == 0 {
			continue
		}
		key := fmt.Sprintf("%s|%d", vi.zone, vi.tag)
		byZoneTag[key] = append(byZoneTag[key], id)
	}

	var out []Finding
	seen := map[string]bool{}
	for _, id := range touchedOrder {
		if seen[id] {
			continue
		}
		vi, ok := vnets[id]
		if !ok || vi.tag == 0 {
			continue
		}
		key := fmt.Sprintf("%s|%d", vi.zone, vi.tag)
		siblings := byZoneTag[key]
		if len(siblings) < 2 {
			continue
		}
		others := make([]string, 0, len(siblings)-1)
		for _, s := range siblings {
			if s != id {
				others = append(others, s)
			}
		}
		sort.Strings(others)
		ref := inventory.Ref{Kind: inventory.KindSDNVnet, ID: id}.String()
		out = append(out, errorf(codeSDNTagDuplicate, ref,
			"vnet %q's tag %d in zone %q is already used by %s", id, vi.tag, vi.zone, strings.Join(others, ", ")))
		seen[id] = true
	}
	return out
}
