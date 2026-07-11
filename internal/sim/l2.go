package sim

import (
	"fmt"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// pathState is the outcome of the reachability phase.
type pathState int

const (
	pathReachable pathState = iota
	pathUnreachable
	pathIndeterminate
)

type reachResult struct {
	missing *Missing
	state   pathState
}

func reachable() reachResult     { return reachResult{state: pathReachable} }
func indeterminate() reachResult { return reachResult{state: pathIndeterminate} }
func unreachable(code, msg, atRef, atNode string) reachResult {
	return reachResult{state: pathUnreachable, missing: &Missing{Code: code, Message: msg, AtRef: atRef, AtNode: atNode}}
}

// reachability establishes whether a path exists between src and dst,
// appending the hop list to res and returning the outcome (with Missing set
// on an unreachable break).
func (e *Engine) reachability(src, dst resolvedEP, res *Result) reachResult {
	if src.unattached {
		return unreachable("nic_unattached",
			fmt.Sprintf("source guest NIC %s attaches to %q, which resolves to no bridge or VNet",
				src.public.Ref, nicTarget(src)), src.public.Ref, src.node)
	}
	if dst.unattached {
		return unreachable("nic_unattached",
			fmt.Sprintf("destination guest NIC %s attaches to %q, which resolves to no bridge or VNet",
				dst.public.Ref, nicTarget(dst)), dst.public.Ref, dst.node)
	}

	srcExt := src.kind == EndpointExternal
	dstExt := dst.kind == EndpointExternal
	switch {
	case srcExt && dstExt:
		return unreachable("both_external",
			"both endpoints are external — nothing to simulate on the fabric", "", "")
	case srcExt:
		// Flow originates externally; the fabric endpoint (dst) is the target.
		return e.externalPath(dst, true, res)
	case dstExt:
		// Flow egresses to external; the fabric endpoint (src) is the source.
		return e.externalPath(src, false, res)
	}

	// Both endpoints are on the fabric. Try L2 adjacency first.
	if r, handled := e.l2Path(src, dst, res); handled {
		return r
	}
	// Not on a shared L2 domain — needs L3 routing.
	return e.l3Path(src, dst, res)
}

// l2Path evaluates same-L2-domain reachability. handled is false when the
// endpoints are not on a shared L2 domain (so the caller falls through to
// L3); when true, r is the definitive L2 outcome.
func (e *Engine) l2Path(src, dst resolvedEP, res *Result) (r reachResult, handled bool) {
	sk, sok := l2DomainKey(src)
	dk, dok := l2DomainKey(dst)
	if !sok || !dok || sk != dk {
		return reachResult{}, false
	}

	// Same L2 domain. Emit the source-side hops now; the outcome decides
	// whether the far side is reachable.
	e.addHop(res, hopForEndpoint(src))
	e.addHop(res, hopForAttachment(src))

	sameNode := src.node == dst.node || src.kind == EndpointIP || dst.kind == EndpointIP
	if sameNode {
		e.addHop(res, hopForAttachment(dst))
		e.addHop(res, hopForEndpoint(dst))
		return reachable(), true
	}

	// Cross-node: the shared L2 domain must actually be carried between the
	// two nodes.
	if src.attach == attachVnet {
		if r := e.crossNodeVnet(src, dst, res); r.state != pathReachable {
			return r, true
		}
	} else { // plain bridge
		if r := e.crossNodeBridge(src, dst, res); r.state != pathReachable {
			return r, true
		}
	}
	e.addHop(res, hopForAttachment(dst))
	e.addHop(res, hopForEndpoint(dst))
	return reachable(), true
}

// crossNodeVnet checks a shared VNet is realized/carried across both nodes.
func (e *Engine) crossNodeVnet(src, dst resolvedEP, res *Result) reachResult {
	zone := src.zone
	if zone == nil {
		return indeterminateVnet(res, src.vnet.ID)
	}
	switch zone.Type {
	case "vxlan", "evpn":
		// Overlay: reachable across any member node pair (encapsulated over
		// the underlay). Require both nodes to be in the zone's node set.
		if !contains(zone.Nodes, src.node) {
			return notRealized(src.vnet.ID, zone.ID, src.node)
		}
		if !contains(zone.Nodes, dst.node) {
			return notRealized(dst.vnet.ID, zone.ID, dst.node)
		}
		e.addHop(res, Hop{Kind: "overlay", Label: fmt.Sprintf("VXLAN/EVPN overlay (zone %s)", zone.ID),
			Detail: "encapsulated over the underlay VTEP mesh"})
		return reachable()
	case "simple":
		return unreachable("simple_zone_node_local",
			fmt.Sprintf("VNet %s is in simple zone %s, which is node-local: there is no inter-node L2 path (source on %s, destination on %s)",
				src.vnet.ID, zone.ID, src.node, dst.node), src.public.Attachment, "")
	case "vlan", "qinq":
		// The zone's VLAN must be trunked on both realizing bridges.
		bridgeName := zone.Bridge
		fabricVid := src.vid
		if zone.Type == "qinq" {
			res.addCaveat(warnCaveat(CodeNotEvaluated,
				"QinQ inner-tag (customer VLAN) isolation is not evaluated; only the service VLAN's trunking is checked."))
		}
		if r := e.trunkCheck(src.node, bridgeName, fabricVid, res); r.state != pathReachable {
			return r
		}
		if r := e.trunkCheck(dst.node, bridgeName, fabricVid, res); r.state != pathReachable {
			return r
		}
		e.addHop(res, fabricHop(bridgeName, fabricVid, src.node, dst.node))
		return reachable()
	default:
		res.addCaveat(notEvaluated(FeatureUnknownEntityKind,
			fmt.Sprintf("SDN zone type %q is not modeled by the simulator", zone.Type)))
		return indeterminate()
	}
}

// crossNodeBridge checks a shared plain bridge name is carried across nodes.
func (e *Engine) crossNodeBridge(src, dst resolvedEP, res *Result) reachResult {
	name := src.bridge.Name
	if r := e.trunkCheck(src.node, name, src.vid, res); r.state != pathReachable {
		return r
	}
	if r := e.trunkCheck(dst.node, name, dst.vid, res); r.state != pathReachable {
		return r
	}
	e.addHop(res, fabricHop(name, src.vid, src.node, dst.node))
	return reachable()
}

// trunkCheck verifies that VLAN vid is carried by the named bridge on node
// (VLAN-aware bridge VID set / trunk pruning). vid 0 is untagged (native).
func (e *Engine) trunkCheck(node, bridgeName string, vid int, res *Result) reachResult {
	br := e.bridgeByName(node, bridgeName)
	if br == nil {
		return unreachable("bridge_absent",
			fmt.Sprintf("bridge %s is not present on node %s", bridgeName, node),
			"", node)
	}
	if vid == 0 {
		return reachable() // untagged native traffic — carried where the bridge exists
	}
	if !br.VlanAware {
		return unreachable("bridge_not_vlan_aware",
			fmt.Sprintf("bridge %s on node %s is not VLAN-aware, so VLAN %d traffic is not carried", bridgeName, node, vid),
			br.GetRef().String(), node)
	}
	if !vidPermitted(br, vid) {
		uplink := e.uplinkName(br)
		return unreachable("vlan_not_trunked",
			fmt.Sprintf("VLAN %d is not trunked on %s of node %s", vid, uplink, node),
			br.GetRef().String(), node)
	}
	// Configured bridge permits it; cross-check the switch's LLDP advert.
	e.lldpTrunkCrossCheck(br, vid, res)
	return reachable()
}

// vidPermitted reports whether a VLAN-aware bridge carries vid: an empty VID
// set means "all VLANs" (kernel default), otherwise vid must be in a range.
func vidPermitted(br *inventory.Bridge, vid int) bool {
	if len(br.Vids) == 0 {
		return true
	}
	for _, r := range br.Vids {
		if vid >= r.Low && vid <= r.High {
			return true
		}
	}
	return false
}

// uplinkName returns the human name of a bridge's uplink port (a bond
// preferred, else a physical NIC, else a generic label) for the
// missing-trunk message.
func (e *Engine) uplinkName(br *inventory.Bridge) string {
	var physFallback string
	for _, p := range br.Ports {
		switch p.Kind {
		case inventory.KindBond, inventory.KindOVSBond:
			return p.ID
		case inventory.KindPhysNic:
			if physFallback == "" {
				physFallback = p.ID
			}
		}
	}
	if physFallback != "" {
		return physFallback
	}
	return "the uplink"
}

// lldpTrunkCrossCheck adds an advisory caveat when the switch's LLDP
// advertisement on the bridge's uplink does not list vid as trunked, even
// though the bridge's own VID set permits it (docs/features/lldp-discovery
// §2). Advisory only — LLDP frequently omits tagged VLANs, so it never
// overrides the configured-state verdict.
func (e *Engine) lldpTrunkCrossCheck(br *inventory.Bridge, vid int, res *Result) {
	for _, edge := range e.inv.EdgesOf(br.GetRef()) {
		// bridge <- port-of <- (bond|physnic); walk to the physnic and its
		// lldp neighbor.
		if edge.Kind != inventory.EdgePortOf || edge.To != br.GetRef() {
			continue
		}
		e.lldpCheckFromPort(edge.From, br, vid, res)
	}
}

func (e *Engine) lldpCheckFromPort(port inventory.Ref, br *inventory.Bridge, vid int, res *Result) {
	switch port.Kind {
	case inventory.KindPhysNic:
		for _, edge := range e.inv.EdgesOf(port) {
			if edge.Kind != inventory.EdgeLldpAdjacent || edge.From != port {
				continue
			}
			n, ok := e.inv.Get(edge.To)
			if !ok {
				continue
			}
			nb, ok := n.(*inventory.LldpNeighbor)
			if !ok || (nb.VLAN == 0 && len(nb.TaggedVLANs) == 0) {
				continue // switch advertised no VLAN info to cross-check against
			}
			if nb.VLAN == vid || containsInt(nb.TaggedVLANs, vid) {
				return
			}
			res.addCaveat(warnCaveat(CodeLLDPTrunkMismatch,
				fmt.Sprintf("Bridge %s permits VLAN %d, but switch port %s (LLDP) does not advertise it as trunked; verify the physical switch carries VLAN %d.",
					br.Name, vid, nb.PortID, vid)))
			return
		}
	case inventory.KindBond, inventory.KindOVSBond:
		for _, edge := range e.inv.EdgesOf(port) {
			if edge.Kind == inventory.EdgeEnslavedBy && edge.To == port {
				e.lldpCheckFromPort(edge.From, br, vid, res)
			}
		}
	}
}

func (e *Engine) bridgeByName(node, name string) *inventory.Bridge {
	if m := e.bridgeByNodeName[node]; m != nil {
		return m[name]
	}
	return nil
}

// l2DomainKey returns a stable identity for an endpoint's L2 broadcast
// domain, and whether the endpoint has one. Two endpoints on the same key
// (same VNet, or same plain-bridge name, at the same fabric VLAN) share an
// L2 segment.
func l2DomainKey(ep resolvedEP) (string, bool) {
	switch ep.attach {
	case attachVnet:
		return fmt.Sprintf("vnet/%s/vid=%d", ep.vnet.ID, ep.vid), true
	case attachBridge:
		return fmt.Sprintf("bridge/%s/vid=%d", ep.bridge.Name, ep.vid), true
	default:
		return "", false
	}
}

func nicTarget(ep resolvedEP) string {
	if ep.nic != nil {
		return ep.nic.TargetName
	}
	return ""
}

func indeterminateVnet(res *Result, vnetID string) reachResult {
	res.addCaveat(notEvaluated("SDN zone for VNet "+vnetID,
		"the VNet's zone was not found in inventory, so cross-node realization cannot be checked"))
	return indeterminate()
}

func notRealized(vnetID, zoneID, node string) reachResult {
	return unreachable("vnet_not_realized",
		fmt.Sprintf("VNet %s (zone %s) is not realized on node %s", vnetID, zoneID, node), "", node)
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func containsInt(is []int, n int) bool {
	for _, v := range is {
		if v == n {
			return true
		}
	}
	return false
}
