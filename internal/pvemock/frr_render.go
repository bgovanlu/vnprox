// SPDX-License-Identifier: Apache-2.0

package pvemock

import (
	"encoding/json"
	"strconv"
)

// frr_render.go renders a node's fixture-declared FRRSpec (T-404,
// docs/features/sdn.md §3) into the same JSON shapes internal/host's
// ParseBGPSummary/ParseEVPNVNI parse from real `vtysh -c "show bgp summary
// json"` / `"show evpn vni json"` output — the same "fixture data rendered
// through the real parser" precedent marshalLLDP set for lldpctl.

// bgpSummaryPeerOut is one peer entry inside a rendered AFI block.
type bgpSummaryPeerOut struct {
	Hostname   string `json:"hostname,omitempty"`
	State      string `json:"state"`
	PeerUptime string `json:"peerUptime"`
	RemoteAS   int    `json:"remoteAs"`
	PfxRcd     int    `json:"pfxRcd"`
	PfxSnt     int    `json:"pfxSnt"`
}

// bgpSummaryBlockOut is one rendered address-family block, keyed at the
// top level by its AFI name (e.g. "l2VpnEvpn") — the nested shape modern
// FRR (`show bgp summary json`, with no single AFI selected) produces.
type bgpSummaryBlockOut struct {
	Peers    map[string]bgpSummaryPeerOut `json:"peers"`
	RouterID string                       `json:"routerId"`
	AS       int                          `json:"as"`
}

// marshalBGPSummary renders spec's peers, grouped by BGPPeerSpec.
// AddressFamily (defaulting to "l2VpnEvpn" — EVPN is what docs/features/
// sdn.md §3 is observing), into `show bgp summary json`'s nested-AFI
// shape. A peer with no explicit PeerUptime gets a small placeholder
// established-uptime when its declared State is "Established", or "never"
// otherwise — fixtures rarely need to control the exact uptime string,
// only whether a session is up.
func marshalBGPSummary(spec *FRRSpec) ([]byte, error) {
	blocks := map[string]bgpSummaryBlockOut{}
	if spec == nil {
		return json.Marshal(blocks)
	}
	for _, p := range spec.Peers {
		afi := p.AddressFamily
		if afi == "" {
			afi = "l2VpnEvpn"
		}
		block, ok := blocks[afi]
		if !ok {
			block = bgpSummaryBlockOut{RouterID: spec.RouterID, AS: spec.ASN, Peers: map[string]bgpSummaryPeerOut{}}
		}
		uptime := p.PeerUptime
		if uptime == "" {
			if p.State == "Established" {
				uptime = "00:00:01"
			} else {
				uptime = "never"
			}
		}
		block.Peers[p.Addr] = bgpSummaryPeerOut{
			Hostname: p.Hostname, State: p.State, PeerUptime: uptime,
			RemoteAS: p.RemoteAS, PfxRcd: p.PfxRcd, PfxSnt: p.PfxSnt,
		}
		blocks[afi] = block
	}
	return json.Marshal(blocks)
}

// evpnVniOut is one rendered VNI entry.
type evpnVniOut struct {
	Type      string `json:"type"`
	VxlanIf   string `json:"vxlanIf,omitempty"`
	TenantVRF string `json:"tenantVrf,omitempty"`
	VNI       int    `json:"vni"`
	NumMacs   int    `json:"numMacs,omitempty"`
	NumArpND  int    `json:"numArpNd,omitempty"`
}

// marshalEVPNVNI renders spec's VNIs into `show evpn vni json`'s
// object-keyed-by-VNI shape.
func marshalEVPNVNI(spec *FRRSpec) ([]byte, error) {
	out := map[string]evpnVniOut{}
	if spec == nil {
		return json.Marshal(out)
	}
	for _, v := range spec.VNIs {
		// EVPNVniSpec and evpnVniOut share an identical field
		// name/order/type list (only their struct tags differ: yaml vs
		// json), so a direct conversion is exact and staticcheck-clean.
		out[strconv.Itoa(v.VNI)] = evpnVniOut(v)
	}
	return json.Marshal(out)
}
