// SPDX-License-Identifier: Apache-2.0

package ceph

import (
	"net"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
	"github.com/bgovanlu/vnprox/internal/xnode"
)

// Project builds Overlay from a live inventory snapshot plus status
// (Discover's output): for every node hosting at least one OSD, resolves
// which interface on that node carries an address inside the declared
// public/cluster CIDR (carrierInCIDR), then walks that carrier's physical
// path via internal/topology.ResolvePhysicalPath — the exact same
// carrier -> parent-bridge -> bond-slaves -> PhysNics resolver T-702's
// management-path visibility already established, reused here rather than
// re-implemented (this task's card: "reusing internal/topology's existing
// NIC-path resolution"). Pure with respect to snap/status — no I/O, the
// shape AC1's golden projection test exercises directly.
func Project(snap inventory.Snapshot, status Status) Overlay {
	byNode := map[string][]OSD{}
	var nodeNames []string
	for _, o := range status.OSDs {
		if _, ok := byNode[o.Node]; !ok {
			nodeNames = append(nodeNames, o.Node)
		}
		byNode[o.Node] = append(byNode[o.Node], o)
	}
	sort.Strings(nodeNames)

	nodes := make([]NodeAttribution, 0, len(nodeNames))
	var osdAttrs []OSDAttribution

	for _, node := range nodeNames {
		na := NodeAttribution{Node: node}

		if status.PublicNetwork != "" {
			if ref, ok := carrierInCIDR(snap, node, status.PublicNetwork); ok {
				na.PublicCarrier = ref
				na.PublicPath, na.PublicNICs = topology.ResolvePhysicalPath(snap, ref)
				na.PublicRidingOn = ridingRef(na.PublicPath, na.PublicNICs)
				na.PublicMTU, na.PublicMTUKnown = carrierMTU(snap, ref)
			}
		}
		if status.ClusterNetwork != "" {
			if ref, ok := carrierInCIDR(snap, node, status.ClusterNetwork); ok {
				na.ClusterCarrier = ref
				na.ClusterPath, na.ClusterNICs = topology.ResolvePhysicalPath(snap, ref)
				na.ClusterRidingOn = ridingRef(na.ClusterPath, na.ClusterNICs)
				na.ClusterMTU, na.ClusterMTUKnown = carrierMTU(snap, ref)
			}
		}
		nodes = append(nodes, na)

		osdsForNode := append([]OSD(nil), byNode[node]...)
		sort.Slice(osdsForNode, func(i, j int) bool { return osdsForNode[i].ID < osdsForNode[j].ID })
		for _, o := range osdsForNode {
			osdAttrs = append(osdAttrs, OSDAttribution{OSD: o, PublicBond: na.PublicRidingOn, ClusterBond: na.ClusterRidingOn})
		}
	}

	return Overlay{
		PublicNetwork:  status.PublicNetwork,
		ClusterNetwork: status.ClusterNetwork,
		Nodes:          nodes,
		OSDs:           osdAttrs,
	}
}

// carrierInCIDR finds the (deterministically first, by Ref.String() sort
// order) Bridge or VlanIface on node whose declared Addresses includes one
// falling inside cidr — the two inventory kinds that declare an Addresses
// field at all (entity.go; the same restriction internal/change's
// addressesOf documents for protected-interface detection). ok is false
// (zero Ref) when cidr fails to parse or no carrier on node matches —
// "unresolved", never a guess.
func carrierInCIDR(snap inventory.Snapshot, node, cidr string) (inventory.Ref, bool) {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return inventory.Ref{}, false
	}
	var matches []inventory.Ref
	for _, e := range snap.All() {
		ref := e.GetRef()
		if ref.Node != node {
			continue
		}
		addrs, ok := addressesOf(e)
		if !ok {
			continue
		}
		for _, a := range addrs {
			ip, _, err := net.ParseCIDR(a)
			if err != nil {
				continue
			}
			if network.Contains(ip) {
				matches = append(matches, ref)
				break
			}
		}
	}
	if len(matches) == 0 {
		return inventory.Ref{}, false
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].String() < matches[j].String() })
	return matches[0], true
}

// carrierForExactAddr finds the Bridge/VlanIface on node whose declared
// Addresses contains addr exactly (raw string match, then parsed-IP match —
// the same dual matching internal/change's matchRoles uses, since a
// corosync ring address is reported as a bare IP while inventory's own
// Addresses are always CIDR strings). Used only by CorosyncSharedLink,
// which needs an exact address (corosync.conf's ring*_addr), not a CIDR
// containment test.
func carrierForExactAddr(snap inventory.Snapshot, node, addr string) (inventory.Ref, bool) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return inventory.Ref{}, false
	}
	var matches []inventory.Ref
	for _, e := range snap.All() {
		ref := e.GetRef()
		if ref.Node != node {
			continue
		}
		addrs, ok := addressesOf(e)
		if !ok {
			continue
		}
		for _, a := range addrs {
			if a == addr {
				matches = append(matches, ref)
				break
			}
			if ip, _, err := net.ParseCIDR(a); err == nil && ip.String() == addr {
				matches = append(matches, ref)
				break
			}
		}
	}
	if len(matches) == 0 {
		return inventory.Ref{}, false
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].String() < matches[j].String() })
	return matches[0], true
}

// addressesOf returns e's declared CIDR addresses and whether e is a kind
// that declares an Addresses field at all (Bridge/VlanIface).
func addressesOf(e inventory.Entity) ([]string, bool) {
	switch v := e.(type) {
	case *inventory.Bridge:
		return v.Addresses, true
	case *inventory.VlanIface:
		return v.Addresses, true
	default:
		return nil, false
	}
}

// carrierMTU resolves ref's own effective MTU (runtime if reported, else
// declared — xnode.EffectiveMTU, the same rule CrossNodeMTU already uses
// for a same-named bridge comparison) — the MTU actually configured for
// the network riding this carrier, not the terminal PhysNic's MTU (a
// carrier can be a VLAN sub-interface with its own MTU distinct from its
// parent bond's).
func carrierMTU(snap inventory.Snapshot, ref inventory.Ref) (int, bool) {
	e, ok := snap.Get(ref)
	if !ok {
		return 0, false
	}
	switch v := e.(type) {
	case *inventory.Bridge:
		return xnode.EffectiveMTU(v.MTU, v.MTUDeclared)
	case *inventory.VlanIface:
		return xnode.EffectiveMTU(v.MTU, v.MTUDeclared)
	default:
		return 0, false
	}
}

// ridingRef reports the single ref path/nics say this carrier's traffic
// "rides" for badge/inspector display: the first Bond in path if the
// carrier is bonded, else the sole terminal PhysNic if path resolves
// directly to exactly one bare NIC. Zero Ref (unresolved) when path
// resolves to zero or more-than-one terminal NIC with no Bond present —
// there is no single answer to "the bond" in that case, so this never
// guesses one.
func ridingRef(path []inventory.Ref, nics []inventory.Ref) inventory.Ref {
	for _, r := range path {
		if r.Kind == inventory.KindBond {
			return r
		}
	}
	if len(nics) == 1 {
		return nics[0]
	}
	return inventory.Ref{}
}
