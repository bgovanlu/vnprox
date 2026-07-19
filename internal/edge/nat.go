package edge

import (
	"fmt"

	"github.com/bgovanlu/vnprox/internal/host"
)

// Masquerade is one nat.masquerade.* rule, decoded from its generated
// marker line — see internal/host.NatMasqueradeConfig.
type Masquerade struct {
	ID         string `json:"id"`
	Node       string `json:"node"`
	Iface      string `json:"iface"`
	SourceCIDR string `json:"sourceCidr"`
	Comment    string `json:"comment,omitempty"`
}

// PortForward is one nat.portforward.* rule, decoded from its generated
// marker line, plus the "guest-level DNAT path" correlation (docs/roadmap-
// universal.md's phase-14 framing): when TargetGuestRef resolves, the Edge
// layer draws WAN -> port-forward -> guest as one path, and
// TargetGuestPoweredOff is this card's own exit-demo acceptance criterion
// ("one of them flagged as forwarding to a powered-off guest").
type PortForward struct {
	ID                    string `json:"id"`
	Node                  string `json:"node"`
	Iface                 string `json:"iface"`
	Proto                 string `json:"proto"`
	IntIP                 string `json:"intIp"`
	Comment               string `json:"comment,omitempty"`
	TargetGuestRef        string `json:"targetGuestRef,omitempty"`
	ExtPort               int    `json:"extPort"`
	IntPort               int    `json:"intPort"`
	TargetGuestPoweredOff bool   `json:"targetGuestPoweredOff,omitempty"`
}

// SDNSimpleZoneNAT is one PVE SDN simple-zone subnet with SNAT enabled —
// already surfaced read-only via GET /sdn's Subnet.snat (docs/features/
// sdn.md §2); ProjectNAT only re-shapes it into the Edge layer's own view,
// never a second source of truth for it.
type SDNSimpleZoneNAT struct {
	Zone    string `json:"zone"`
	Vnet    string `json:"vnet"`
	Subnet  string `json:"subnet"`
	Gateway string `json:"gateway,omitempty"`
}

// SDNSubnetInput is one SDN subnet as ProjectNAT needs it — the API
// adapter flattens internal/sdn.Service.Tree's zones/vnets/subnets into
// this flat shape (see internal/api/edge.go).
type SDNSubnetInput struct {
	Zone     string
	ZoneType string
	Vnet     string
	CIDR     string
	Gateway  string
	SNAT     bool
}

// NATView is GET /edge/nat's response shape.
type NATView struct {
	Masquerade       []Masquerade       `json:"masquerade"`
	PortForwards     []PortForward      `json:"portForwards"`
	SDNSimpleZoneNAT []SDNSimpleZoneNAT `json:"sdnSimpleZoneNat"`
	GeneratedAt      int64              `json:"generatedAt"`
}

// GuestLookup resolves a port-forward's IntIP to a known guest ref and its
// powered-off state. ok is false when IntIP correlates to no currently
// known guest (an external/unmanaged target, or simply no IPAM data for
// it) — ProjectNAT then leaves TargetGuestRef/TargetGuestPoweredOff at
// their zero values rather than guessing. A nil GuestLookup disables
// correlation entirely (every port-forward's target is reported unresolved)
// — the same optional-dependency degrade-gracefully convention every other
// read view in this codebase follows.
type GuestLookup func(ip string) (ref string, poweredOff bool, ok bool)

// ProjectNAT parses every node's interfaces file for nat.masquerade.*/
// nat.portforward.* rules (via their generated markers) and folds in the
// SDN simple-zone-NAT rows subnets already carries. Deterministic ordering,
// same convention as ProjectRoutes.
func ProjectNAT(nodes []NodeInterfaces, subnets []SDNSubnetInput, lookup GuestLookup) (NATView, error) {
	var out NATView
	for _, n := range nodes {
		f, err := host.ParseInterfaces([]byte(n.Content))
		if err != nil {
			return NATView{}, fmt.Errorf("edge: parsing interfaces for node %s: %w", n.Node, err)
		}
		for _, e := range f.Ifaces() {
			for _, item := range e.Body {
				if item.Kind != host.BodyOption || item.Key != "post-up" {
					continue
				}
				s, ok := host.CutEdgeMarker(item.Value)
				if !ok {
					continue
				}
				if c, ok := host.DecodeNatMasqueradeMarker(s); ok {
					out.Masquerade = append(out.Masquerade, Masquerade{
						ID: c.ID, Node: n.Node, Iface: c.Iface, SourceCIDR: c.SourceCIDR, Comment: c.Comment,
					})
					continue
				}
				if c, ok := host.DecodeNatPortForwardMarker(s); ok {
					pf := PortForward{
						ID: c.ID, Node: n.Node, Iface: c.Iface, Proto: c.Proto,
						ExtPort: c.ExtPort, IntIP: c.IntIP, IntPort: c.IntPort, Comment: c.Comment,
					}
					if lookup != nil {
						if ref, poweredOff, ok := lookup(c.IntIP); ok {
							pf.TargetGuestRef = ref
							pf.TargetGuestPoweredOff = poweredOff
						}
					}
					out.PortForwards = append(out.PortForwards, pf)
				}
			}
		}
	}
	for _, s := range subnets {
		if s.ZoneType == "simple" && s.SNAT {
			out.SDNSimpleZoneNAT = append(out.SDNSimpleZoneNAT, SDNSimpleZoneNAT{
				Zone: s.Zone, Vnet: s.Vnet, Subnet: s.CIDR, Gateway: s.Gateway,
			})
		}
	}
	return out, nil
}
