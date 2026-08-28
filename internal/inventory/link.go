// SPDX-License-Identifier: Apache-2.0

package inventory

import (
	"net"
	"sort"
	"strconv"
	"strings"
)

// EdgeKind names a typed relationship between two entities
// (docs/data-model.md §1).
type EdgeKind string

const (
	// EdgeEnslavedBy points from a slave NIC to the bond that enslaves it.
	EdgeEnslavedBy EdgeKind = "enslaved-by"
	// EdgePortOf points from a bridge port (NIC/bond/vlan) to its bridge.
	EdgePortOf EdgeKind = "port-of"
	// EdgeTaggedOn points from a VLAN sub-interface to its parent link.
	EdgeTaggedOn EdgeKind = "tagged-on"
	// EdgeRealizes points from an SDN VNet to a bridge that realizes it on a
	// node.
	EdgeRealizes EdgeKind = "realizes"
	// EdgeAttachedTo points from a guest NIC to its bridge or VNet.
	EdgeAttachedTo EdgeKind = "attached-to"
	// EdgeLldpAdjacent points from a physical NIC to a discovered LLDP
	// neighbor.
	EdgeLldpAdjacent EdgeKind = "lldp-adjacent"
	// EdgeVtepPeer connects two underlay bridges/interfaces that own a
	// vxlan/evpn zone's peer addresses — the VTEP tunnel mesh
	// docs/features/sdn.md §1 draws for those zone types ("EVPN/VXLAN
	// zones draw the VTEP mesh with tunnel endpoints and MTU
	// annotations"). Added by T-401.
	EdgeVtepPeer EdgeKind = "vtep-peer"
)

// Edge is a typed, directed relationship between two entity Refs, optionally
// annotated with rendering badges (e.g. a VLAN tag or node name).
type Edge struct {
	From   Ref
	To     Ref
	Kind   EdgeKind
	Badges []string
}

// linkAll resolves the derived Ref fields of every entity (bridge Ports,
// vlan Parent, guest-NIC BridgeOrVnet + EffectiveVid, LLDP LocalNic) against
// the whole entity set and returns the typed edge list. It mutates the
// (freshly built, not-yet-published) entities in place.
func linkAll(ents map[Ref]Entity) []Edge {
	// index: node -> iface name -> Ref, for all L2/physical links.
	linkByName := map[string]map[string]Ref{}
	addLink := func(node, name string, ref Ref) {
		if name == "" {
			return
		}
		m := linkByName[node]
		if m == nil {
			m = map[string]Ref{}
			linkByName[node] = m
		}
		if _, exists := m[name]; !exists {
			m[name] = ref
		}
	}
	vnetByID := map[string]Ref{}
	zoneByID := map[string]*SdnZone{}
	bridges := []*Bridge{}

	for ref, e := range ents {
		switch v := e.(type) {
		case *PhysNic:
			addLink(ref.Node, v.Name, ref)
		case *Bond:
			addLink(ref.Node, v.Name, ref)
		case *Bridge:
			addLink(ref.Node, v.Name, ref)
			bridges = append(bridges, v)
		case *VlanIface:
			addLink(ref.Node, v.Name, ref)
		case *SdnVnet:
			vnetByID[v.ID] = ref
		case *SdnZone:
			zoneByID[v.ID] = v
		}
	}

	var edges []Edge
	resolveName := func(node, name string) (Ref, bool) {
		if m := linkByName[node]; m != nil {
			r, ok := m[name]
			return r, ok
		}
		return Ref{}, false
	}

	for ref, e := range ents {
		switch v := e.(type) {
		case *Bond:
			slaves := v.Slaves
			if len(slaves) == 0 {
				slaves = v.DeclaredSlaves
			}
			for _, s := range slaves {
				if sr, ok := resolveName(ref.Node, s); ok {
					edges = append(edges, Edge{From: sr, To: ref, Kind: EdgeEnslavedBy})
				}
			}
		case *Bridge:
			names := v.PortNames
			if len(names) == 0 {
				names = v.DeclaredPortNames
			}
			v.Ports = v.Ports[:0]
			for _, pn := range names {
				if pr, ok := resolveName(ref.Node, pn); ok {
					v.Ports = append(v.Ports, pr)
					edges = append(edges, Edge{From: pr, To: ref, Kind: EdgePortOf})
				}
			}
		case *VlanIface:
			if pr, ok := resolveName(ref.Node, v.ParentName); ok {
				v.Parent = pr
				edges = append(edges, Edge{From: ref, To: pr, Kind: EdgeTaggedOn, Badges: []string{"vid=" + strconv.Itoa(v.Vid)}})
			}
		case *LldpNeighbor:
			if nr, ok := resolveName(v.Node, v.LocalIface); ok && nr.Kind == KindPhysNic {
				v.LocalNic = nr
				edges = append(edges, Edge{From: nr, To: ref, Kind: EdgeLldpAdjacent})
			}
		}
	}

	// SDN realizes edges: a VNet is realized on every bridge (per node) named
	// by its zone's bridge.
	for ref, e := range ents {
		vn, ok := e.(*SdnVnet)
		if !ok {
			continue
		}
		zone := zoneByID[vn.Zone]
		if zone == nil || zone.Bridge == "" {
			continue
		}
		for _, b := range bridges {
			if b.Name == zone.Bridge {
				badges := []string{"node=" + b.Node}
				if vn.Tag != 0 {
					badges = append(badges, "tag="+strconv.Itoa(vn.Tag))
				}
				edges = append(edges, Edge{From: ref, To: b.GetRef(), Kind: EdgeRealizes, Badges: badges})
			}
		}
	}

	// VTEP mesh edges: for vxlan/evpn zones, every pair of the zone's peer
	// addresses that resolves to a bridge/interface owning that address on
	// some node gets a full-mesh "vtep-peer" edge between them (real VXLAN
	// tunnels are unicast peer-to-peer, or route-reflected through EVPN's
	// controller, but either way every pair of VTEPs needs reachability —
	// docs/features/sdn.md §1: "EVPN/VXLAN zones draw the VTEP mesh with
	// tunnel endpoints and MTU annotations"). badges carry the zone id and
	// the zone's declared VNI MTU so the map can annotate the tunnel MTU
	// inline, matching the VXLAN wizard's "underlay MTU - 50" math
	// (docs/features/sdn.md §2).
	for _, zone := range zoneByID {
		if zone.Type != "vxlan" && zone.Type != "evpn" {
			continue
		}
		var endpoints []Ref
		seen := map[Ref]bool{}
		for _, peer := range zone.Peers {
			br := bridgeOwningAddress(bridges, peer)
			if br == nil || seen[br.GetRef()] {
				continue
			}
			seen[br.GetRef()] = true
			endpoints = append(endpoints, br.GetRef())
		}
		sort.Slice(endpoints, func(i, j int) bool { return endpoints[i].String() < endpoints[j].String() })
		badges := []string{"zone=" + zone.ID}
		if zone.MTU != 0 {
			badges = append(badges, "vniMtu="+strconv.Itoa(zone.MTU))
		}
		for i := 0; i < len(endpoints); i++ {
			for j := i + 1; j < len(endpoints); j++ {
				edges = append(edges, Edge{From: endpoints[i], To: endpoints[j], Kind: EdgeVtepPeer, Badges: badges})
			}
		}
	}

	// Guest NIC attachment: resolve to a plain bridge on the guest's node or,
	// failing that, a cluster-scoped SDN VNet; propagate the effective VLAN.
	for ref, e := range ents {
		nic, ok := e.(*GuestNic)
		if !ok {
			continue
		}
		resolveGuestNic(nic, ref.Node, resolveName, vnetByID, zoneByID, ents)
		if !nic.BridgeOrVnet.IsZero() {
			var badges []string
			if nic.EffectiveVid != 0 {
				badges = append(badges, "vid="+strconv.Itoa(nic.EffectiveVid))
			}
			edges = append(edges, Edge{From: ref, To: nic.BridgeOrVnet, Kind: EdgeAttachedTo, Badges: badges})
		}
	}

	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Kind != edges[j].Kind {
			return edges[i].Kind < edges[j].Kind
		}
		if edges[i].From != edges[j].From {
			return edges[i].From.String() < edges[j].From.String()
		}
		return edges[i].To.String() < edges[j].To.String()
	})
	return edges
}

// bridgeOwningAddress returns the bridge in bridges carrying addr (a bare
// IP, as zone.Peers lists) among its declared Addresses (CIDR strings), or
// nil if no bridge owns it. Used to anchor a vxlan/evpn zone's VTEP mesh
// (peer addresses) onto the actual underlay bridge/interface entities the
// map already renders, rather than introducing a new synthetic node kind
// just to represent a tunnel endpoint.
func bridgeOwningAddress(bridges []*Bridge, addr string) *Bridge {
	want := net.ParseIP(addr)
	if want == nil {
		return nil
	}
	for _, b := range bridges {
		for _, cidr := range b.Addresses {
			host, _, ok := strings.Cut(cidr, "/")
			if !ok {
				host = cidr
			}
			if ip := net.ParseIP(host); ip != nil && ip.Equal(want) {
				return b
			}
		}
	}
	return nil
}

// resolveGuestNic sets a guest NIC's BridgeOrVnet Ref and EffectiveVid.
//
// A guest's "bridge=" value may name either a plain Linux/OVS bridge present
// on the guest's node, or an SDN VNet (VNets are cluster-scoped and carry
// the same name a guest attaches to). Plain bridges take precedence when a
// name matches both. VLAN propagation:
//   - Plain bridge: the effective VLAN is the guest's own tag (nic.Vid).
//   - SDN VNet with a tag (simple/vlan/qinq zone): the VNet's tag is the
//     VLAN carried on the fabric; the guest's own tag is an inner tag. The
//     effective (fabric) VLAN is the VNet tag, falling back to the guest tag
//     when the VNet has none (e.g. vxlan/evpn zones).
func resolveGuestNic(
	nic *GuestNic, node string,
	resolveName func(node, name string) (Ref, bool),
	vnetByID map[string]Ref, zoneByID map[string]*SdnZone,
	ents map[Ref]Entity,
) {
	nic.EffectiveVid = nic.Vid
	if nic.TargetName == "" {
		return
	}
	// Prefer a plain bridge on the guest's own node.
	if br, ok := resolveName(node, nic.TargetName); ok && (br.Kind == KindBridge || br.Kind == KindOVSBridge) {
		nic.BridgeOrVnet = br
		nic.EffectiveVid = nic.Vid
		return
	}
	// Otherwise an SDN VNet (cluster-scoped).
	if vr, ok := vnetByID[nic.TargetName]; ok {
		nic.BridgeOrVnet = vr
		if vn, ok := ents[vr].(*SdnVnet); ok && vn.Tag != 0 {
			nic.EffectiveVid = vn.Tag
		}
	}
}
