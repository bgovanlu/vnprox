package sim

import (
	"fmt"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// l3Path evaluates routing between two on-fabric endpoints that do NOT share
// an L2 domain (different subnets/VNets/bridges). It emits hops and returns
// the routing outcome per PVE SDN semantics (docs/features/sdn.md).
func (e *Engine) l3Path(src, dst resolvedEP, res *Result) reachResult {
	e.addHop(res, hopForEndpoint(src))
	e.addHop(res, hopForAttachment(src))

	// Routing decisions need both endpoints anchored to a subnet. Plain
	// (non-SDN) bridges route via host routing tables the inventory does not
	// carry — honestly not evaluated rather than guessed.
	if src.subnet == nil || dst.subnet == nil {
		res.addCaveat(notEvaluated(FeatureExternalRouting,
			"L3 routing between these endpoints depends on host/upstream routing tables not carried in the inventory snapshot"))
		return indeterminate()
	}

	if src.subnet.ID == dst.subnet.ID {
		// Same subnet but different L2 domain is contradictory config; treat
		// as intra-subnet (routable) rather than inventing a break.
		e.addHop(res, hopForAttachment(dst))
		e.addHop(res, hopForEndpoint(dst))
		return reachable()
	}

	sZone, dZone := src.zone, dst.zone
	sameZone := sZone != nil && dZone != nil && sZone.ID == dZone.ID
	if sameZone {
		if sZone.Type == "evpn" {
			e.addHop(res, Hop{Kind: "router", Node: firstExitNode(sZone),
				Label:  fmt.Sprintf("EVPN VRF routing (zone %s)", sZone.ID),
				Detail: "inter-VNet routing via the zone's anycast gateways / VRF"})
			e.addHop(res, hopForAttachment(dst))
			e.addHop(res, hopForEndpoint(dst))
			return reachable()
		}
		return unreachable("no_intrazone_route",
			fmt.Sprintf("no route between subnets %s and %s: %s zone %s does not route between VNets (only EVPN zones provide inter-VNet routing)",
				src.subnet.ID, dst.subnet.ID, sZone.Type, sZone.ID), "", "")
	}

	// Different zones (or a zone we couldn't identify): PVE does not route
	// between them without an exit node / external router.
	return unreachable("no_route_between_zones",
		fmt.Sprintf("no route between subnets %s and %s — different zones without exit node",
			src.subnet.ID, dst.subnet.ID), "", "")
}

// externalPath evaluates reachability between an on-fabric endpoint and
// external/WAN. ingress is true when the flow originates externally (the
// fabric endpoint is the destination).
func (e *Engine) externalPath(f resolvedEP, ingress bool, res *Result) reachResult {
	if !ingress {
		e.addHop(res, hopForEndpoint(f))
		e.addHop(res, hopForAttachment(f))
	}

	r := e.egressBoundary(f, ingress, res)

	if r.state == pathReachable {
		if ingress {
			e.addHop(res, hopForAttachment(f))
			e.addHop(res, hopForEndpoint(f))
		} else {
			e.addHop(res, Hop{Kind: "external", Label: "external / WAN"})
		}
	}
	return r
}

// egressBoundary determines whether the fabric endpoint has a routing
// boundary to external (SNAT, an SDN exit node, or a plain-bridge host
// gateway) and emits the boundary hop + relevant caveats.
func (e *Engine) egressBoundary(f resolvedEP, ingress bool, res *Result) reachResult {
	if ingress {
		res.addCaveat(warnCaveat(CodeConntrack,
			"Inbound connections from external require a published service / port-forward (DNAT), which is not modeled; this reflects routing reachability only."))
	}

	switch f.attach {
	case attachVnet:
		zone := f.zone
		snat := f.subnet != nil && f.subnet.SNAT
		hasExit := zone != nil && len(zone.ExitNodes) > 0
		switch {
		case hasExit:
			exit := firstExitNode(zone)
			e.addHop(res, Hop{Kind: "exit-node", Node: exit,
				Label:  fmt.Sprintf("exit node %s (zone %s)", exit, zone.ID),
				Detail: "routes the SDN fabric to the physical network"})
			if snat && !ingress {
				res.addCaveat(snatCaveat(f.subnet.ID))
			}
			return reachable()
		case snat:
			e.addHop(res, Hop{Kind: "snat", Node: f.node,
				Label:  fmt.Sprintf("SNAT egress (subnet %s)", f.subnet.ID),
				Detail: "source-NAT to the node's address"})
			if !ingress {
				res.addCaveat(snatCaveat(f.subnet.ID))
			}
			return reachable()
		default:
			cidr := "the VNet's subnet"
			if f.subnet != nil {
				cidr = f.subnet.ID
			}
			return unreachable("no_external_boundary",
				fmt.Sprintf("subnet %s has no SNAT and its zone has no exit node: no path to/from external", cidr),
				"", f.node)
		}
	case attachBridge:
		if f.bridge.Gateway != "" {
			e.addHop(res, Hop{Ref: f.bridge.GetRef().String(), Kind: "gateway", Node: f.node,
				Label:  fmt.Sprintf("gateway %s (bridge %s)", f.bridge.Gateway, f.bridge.Name),
				Detail: "the node's configured default gateway for this subnet"})
			res.addCaveat(infoCaveat(CodeSimulated,
				"Reachability beyond the node's own gateway (upstream/physical-network routing) is not evaluated."))
			return reachable()
		}
		return unreachable("no_gateway",
			fmt.Sprintf("bridge %s on node %s has no gateway configured: no path to/from external", f.bridge.Name, f.node),
			f.bridge.GetRef().String(), f.node)
	default:
		res.addCaveat(notEvaluated(FeatureExternalRouting,
			"the fabric endpoint is not anchored to a bridge or VNet, so its external boundary cannot be determined"))
		return indeterminate()
	}
}

func snatCaveat(cidr string) Caveat {
	return warnCaveat(CodeSNATAsymmetry,
		fmt.Sprintf("Egress from subnet %s is SNAT'd; return traffic is conntrack-dependent, so a forward allow does not by itself prove replies are deliverable.", cidr))
}

// firstExitNode returns a zone's first designated exit node, or "" if none.
func firstExitNode(zone *inventory.SdnZone) string {
	if zone == nil || len(zone.ExitNodes) == 0 {
		return ""
	}
	return zone.ExitNodes[0]
}
