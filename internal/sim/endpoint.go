package sim

import (
	"fmt"
	"net/netip"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// attachKind classifies what L2 thing an endpoint hangs off.
type attachKind string

const (
	attachNone   attachKind = ""       // external, or an IP in no known subnet
	attachBridge attachKind = "bridge" // a plain Linux/OVS bridge
	attachVnet   attachKind = "vnet"   // an SDN VNet
)

// resolvedEP is the engine's internal, fully-resolved view of one endpoint.
type resolvedEP struct {
	ip         netip.Addr
	vnet       *inventory.SdnVnet
	subnet     *inventory.SdnSubnet
	nic        *inventory.GuestNic
	zone       *inventory.SdnZone
	bridge     *inventory.Bridge
	attach     attachKind
	node       string
	ipSource   IPSource
	kind       EndpointKind
	public     ResolvedEndpoint
	vid        int
	unattached bool
	fatal      bool
	ipKnown    bool
	fwEnabled  bool
}

// resolveEndpoint turns a request Endpoint into a resolvedEP, attaching any
// endpoint-level caveats to res. family (already defaulted via
// Family.orDefault) governs which address family a guest-nic endpoint's IP
// resolves to; a literal ip endpoint's family is self-evident from the
// address itself and ignores it.
func (e *Engine) resolveEndpoint(ep Endpoint, family Family, res *Result) resolvedEP {
	switch ep.Kind {
	case EndpointExternal:
		return resolvedEP{
			kind:   EndpointExternal,
			attach: attachNone,
			public: ResolvedEndpoint{Kind: EndpointExternal, Description: "external / WAN"},
		}
	case EndpointIP:
		return e.resolveIP(ep.IP, res)
	case EndpointGuestNic:
		return e.resolveGuestNic(ep.NicRef, family, res)
	default:
		res.addCaveat(notEvaluated(FeatureUnknownEntityKind,
			fmt.Sprintf("endpoint kind %q is not supported", ep.Kind)))
		return resolvedEP{kind: ep.Kind, fatal: true,
			public: ResolvedEndpoint{Kind: ep.Kind, Description: "unsupported endpoint kind"}}
	}
}

func (e *Engine) resolveIP(raw string, res *Result) resolvedEP {
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		res.addCaveat(blockerCaveat(CodeNotEvaluated,
			fmt.Sprintf("endpoint IP %q is not a valid address", raw)))
		return resolvedEP{kind: EndpointIP, fatal: true,
			public: ResolvedEndpoint{Kind: EndpointIP, Description: "invalid IP literal"}}
	}
	rep := resolvedEP{
		kind:     EndpointIP,
		ip:       addr,
		ipKnown:  true,
		ipSource: IPSourceLiteral,
		public: ResolvedEndpoint{
			Kind: EndpointIP, IP: addr.String(), IPSource: IPSourceLiteral,
		},
	}
	// An IP literal that falls inside a known SDN subnet is treated as
	// living on that subnet's VNet; otherwise it is an unmanaged/off-fabric
	// address (attachNone), reachable only via routing/external.
	if sub := e.subnetContaining(addr); sub != nil {
		rep.subnet = sub
		if vn := e.vnetByID[sub.Vnet]; vn != nil {
			rep.vnet = vn
			rep.zone = e.zoneByID[vn.Zone]
			rep.attach = attachVnet
			rep.vid = vn.Tag
			rep.public.Vnet = vn.ID
			rep.public.Zone = vn.Zone
		}
		rep.public.Subnet = sub.ID
		rep.public.Description = fmt.Sprintf("IP %s in subnet %s", addr, sub.ID)
	} else {
		rep.public.Description = fmt.Sprintf("IP %s (no known subnet — treated as off-fabric)", addr)
	}
	return rep
}

func (e *Engine) resolveGuestNic(ref inventory.Ref, family Family, res *Result) resolvedEP {
	if ref.Kind != inventory.KindGuestNic {
		res.addCaveat(notEvaluated(FeatureUnknownEntityKind,
			fmt.Sprintf("endpoint ref %s is a %s, not a guest NIC", ref, ref.Kind)))
		return resolvedEP{kind: EndpointGuestNic, fatal: true,
			public: ResolvedEndpoint{Kind: EndpointGuestNic, Ref: ref.String(),
				Description: "endpoint is not a guest NIC"}}
	}
	nic, ok := e.guestNics[ref]
	if !ok {
		res.addCaveat(blockerCaveat(CodeNotEvaluated,
			fmt.Sprintf("guest NIC %s not found in inventory", ref)))
		return resolvedEP{kind: EndpointGuestNic, fatal: true,
			public: ResolvedEndpoint{Kind: EndpointGuestNic, Ref: ref.String(),
				Description: "guest NIC not found"}}
	}

	rep := resolvedEP{
		kind:      EndpointGuestNic,
		nic:       nic,
		node:      ref.Node,
		vid:       nic.EffectiveVid,
		fwEnabled: nic.Firewall,
		public: ResolvedEndpoint{
			Kind: EndpointGuestNic, Ref: ref.String(), Guest: nic.Guest.String(),
			Node: ref.Node, Vid: nic.EffectiveVid,
		},
	}

	e.resolveNicAttachment(&rep, nic, res)
	e.resolveNicIP(&rep, ref, family, res)
	rep.public.Description = e.describeNic(nic, rep)
	return rep
}

// resolveNicAttachment fills the L2 attachment (bridge/vnet/zone) of a guest
// NIC endpoint.
func (e *Engine) resolveNicAttachment(rep *resolvedEP, nic *inventory.GuestNic, res *Result) {
	if nic.BridgeOrVnet.IsZero() {
		rep.unattached = true
		rep.attach = attachNone
		return
	}
	rep.public.Attachment = nic.BridgeOrVnet.String()
	switch nic.BridgeOrVnet.Kind {
	case inventory.KindBridge, inventory.KindOVSBridge:
		rep.attach = attachBridge
		rep.bridge = e.bridgesByRef[nic.BridgeOrVnet]
		if rep.bridge == nil {
			rep.unattached = true
			rep.attach = attachNone
			return
		}
		if rep.bridge.Virt == inventory.BridgeOVS {
			res.addCaveat(warnCaveat(CodeOVS,
				"Path traverses an Open vSwitch bridge; the engine's VLAN/trunk reasoning is validated against Linux-bridge semantics."))
		}
	case inventory.KindSDNVnet:
		rep.attach = attachVnet
		rep.vnet = e.vnetByID[nic.BridgeOrVnet.ID]
		if rep.vnet == nil {
			rep.unattached = true
			rep.attach = attachNone
			return
		}
		rep.zone = e.zoneByID[rep.vnet.Zone]
		rep.public.Vnet = rep.vnet.ID
		rep.public.Zone = rep.vnet.Zone
	default:
		// A guest NIC that resolves to some other entity kind — do not guess.
		res.addCaveat(notEvaluated(FeatureUnknownEntityKind,
			fmt.Sprintf("guest NIC attaches to %s (kind %s), which the simulator does not model",
				nic.BridgeOrVnet, nic.BridgeOrVnet.Kind)))
		rep.fatal = true
	}
}

// resolveNicIP fills a guest NIC endpoint's IP from the caller-supplied
// side-table (best available source, filtered to family — T-1404), and its
// subnet.
func (e *Engine) resolveNicIP(rep *resolvedEP, ref inventory.Ref, family Family, res *Result) {
	family = family.orDefault()
	if ip, src, ok := e.bestGuestIP(ref, family); ok {
		rep.ip = ip
		rep.ipKnown = true
		rep.ipSource = src
		rep.public.IP = ip.String()
		rep.public.IPSource = src
		if src == IPSourceAgent {
			res.addCaveat(warnCaveat(CodeGuestAgentIP,
				fmt.Sprintf("Endpoint IP %s for %s came from the guest agent (runtime, lower confidence), not configuration.", ip, ref)))
		}
		if sub := e.subnetContaining(ip); sub != nil {
			rep.subnet = sub
			rep.public.Subnet = sub.ID
		}
		return
	}
	// No IP known for the requested family. We may still know the *subnet*
	// if the VNet has exactly one subnet of that family — enough for
	// zone/L3 reasoning, though not for address-based firewall matching
	// (a dual-stack VNet's other-family subnet is deliberately excluded
	// here, so a v6 request never anchors to a v4-only subnet or vice
	// versa).
	if rep.vnet != nil {
		if subs := familySubnets(e.subnetsByVnet[rep.vnet.ID], family); len(subs) == 1 {
			rep.subnet = subs[0]
			rep.public.Subnet = subs[0].ID
		}
	}
	rep.ipSource = IPSourceUnknown
}

// familySubnets filters subs to those whose CIDR parses as family — used to
// disambiguate a dual-stack VNet's subnet list when a guest-nic's IP isn't
// known for the requested family (resolveNicIP above).
func familySubnets(subs []*inventory.SdnSubnet, family Family) []*inventory.SdnSubnet {
	var out []*inventory.SdnSubnet
	for _, s := range subs {
		pfx, err := netip.ParsePrefix(s.ID)
		if err != nil {
			continue
		}
		if family.matches(pfx.Addr()) {
			out = append(out, s)
		}
	}
	return out
}

// bestGuestIP returns the highest-confidence resolvable IP of the
// requested family for a guest NIC — an address of the other family in
// Input.GuestIPs is never returned, even if it would otherwise outrank a
// same-family candidate (T-1404: family selects, not merely prefers).
func (e *Engine) bestGuestIP(ref inventory.Ref, family Family) (netip.Addr, IPSource, bool) {
	ips := e.guestIPs[ref]
	if len(ips) == 0 {
		return netip.Addr{}, IPSourceUnknown, false
	}
	order := map[IPSource]int{IPSourceStatic: 3, IPSourceIPAM: 2, IPSourceAgent: 1}
	var best netip.Addr
	bestRank := 0
	bestSrc := IPSourceUnknown
	for _, gi := range ips {
		addr, err := netip.ParseAddr(gi.IP)
		if err != nil || !family.matches(addr) {
			continue
		}
		if r := order[gi.Source]; r > bestRank {
			bestRank, best, bestSrc = r, addr, gi.Source
		}
	}
	if bestRank == 0 {
		return netip.Addr{}, IPSourceUnknown, false
	}
	return best, bestSrc, true
}

// subnetContaining returns the SDN subnet whose CIDR contains addr, or nil.
func (e *Engine) subnetContaining(addr netip.Addr) *inventory.SdnSubnet {
	for _, s := range e.subnets {
		pfx, err := netip.ParsePrefix(s.ID)
		if err != nil {
			continue
		}
		if pfx.Contains(addr) {
			return s
		}
	}
	return nil
}

func (e *Engine) describeNic(nic *inventory.GuestNic, rep resolvedEP) string {
	name := nic.Guest.ID
	if g := e.guests[nic.Guest]; g != nil && g.Name != "" {
		name = g.Name
	}
	switch rep.attach {
	case attachBridge:
		return fmt.Sprintf("%s %s on bridge %s (vlan %d)", name, nic.Key, rep.bridge.Name, rep.vid)
	case attachVnet:
		return fmt.Sprintf("%s %s on VNet %s (vlan %d)", name, nic.Key, rep.vnet.ID, rep.vid)
	default:
		return fmt.Sprintf("%s %s (attaches to %q — unresolved)", name, nic.Key, nic.TargetName)
	}
}
