package sim

import "fmt"

// addHop appends h to the result's hop list unless it is a no-op (empty
// label), and — T-1505 — discloses the CodeQosShaped caveat when h names a
// ref (Kind:Node:ID) carrying an applied qos.shape, so a shaped hop is
// surfaced rather than silently ignored. res.addCaveat dedupes by (code,
// message), so a path that crosses the same shaped ref at more than one
// hop (e.g. a bridge that is both src's and dst's attachment) still
// produces exactly one caveat.
func (e *Engine) addHop(res *Result, h Hop) {
	if h.Label == "" {
		return
	}
	res.Hops = append(res.Hops, h)
	if h.Ref != "" && e.shapedRefs[h.Ref] {
		res.addCaveat(qosShapedCaveat(h.Label))
	}
}

// hopForEndpoint renders the endpoint itself as a hop.
func hopForEndpoint(ep resolvedEP) Hop {
	switch ep.kind {
	case EndpointExternal:
		return Hop{Kind: "external", Label: "external / WAN"}
	case EndpointIP:
		return Hop{Kind: "ip", Label: ep.public.IP, Detail: ep.public.Description}
	case EndpointGuestNic:
		return Hop{Ref: ep.public.Ref, Kind: "guest-nic", Node: ep.node,
			Label: ep.public.Description}
	default:
		return Hop{}
	}
}

// hopForAttachment renders an endpoint's L2 attachment (bridge/VNet) as a
// hop. Empty for endpoints with no on-fabric attachment.
func hopForAttachment(ep resolvedEP) Hop {
	switch ep.attach {
	case attachBridge:
		return Hop{Ref: ep.bridge.GetRef().String(), Kind: "bridge", Node: ep.node,
			Label:  fmt.Sprintf("bridge %s", ep.bridge.Name),
			Detail: vlanDetail(ep.vid)}
	case attachVnet:
		return Hop{Ref: ep.vnet.GetRef().String(), Kind: "sdn-vnet", Node: ep.node,
			Label: fmt.Sprintf("VNet %s", ep.vnet.ID), Detail: vlanDetail(ep.vid)}
	default:
		return Hop{}
	}
}

// fabricHop renders the inter-node fabric segment carrying a VLAN between two
// nodes.
func fabricHop(bridgeName string, vid int, srcNode, dstNode string) Hop {
	return Hop{Kind: "fabric",
		Label:  fmt.Sprintf("fabric: %s trunk %s→%s", bridgeName, srcNode, dstNode),
		Detail: vlanDetail(vid)}
}

func vlanDetail(vid int) string {
	if vid == 0 {
		return "untagged"
	}
	return fmt.Sprintf("VLAN %d", vid)
}
