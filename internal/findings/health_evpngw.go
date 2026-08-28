// SPDX-License-Identifier: Apache-2.0

// health_evpngw.go implements docs/features/monitoring.md §5's
// "evpn_gw_inconsistency" health check (T-803): an EVPN zone's anycast
// subnet gateway (docs/features/sdn.md §2: "the gateway becomes the
// anycast address realized on every zone member node") realized
// differently across that zone's member nodes — present on some, absent (or
// a different address) on others. PVE realizes a VNet as a same-named
// bridge interface on every node it applies to (see guest NICs attaching
// to e.g. "vnet-tenant-a" directly by name in evpn-lab.yaml); this check
// compares that per-node bridge's declared address set against the
// subnet's gateway, reusing T-801's internal/xnode.BridgesByName
// cross-node fold (the same "group same-named bridges by node" helper
// drift/change's own bridge/MTU comparisons already share) rather than
// writing a second same-named-bridge grouping.
//
// **Needs hardware validation**: exact per-node anycast-gateway realization
// in /etc/network/interfaces is unverified against a real PVE cluster (see
// planning/reports/needs-hardware-validation.md) — this check's "present
// iff the vnet-named bridge carries the gateway address" proxy is this
// codebase's own best inference, not a confirmed mirror of real EVPN
// realization.
//
// Hysteresis-exempt (mgmt_single_path-style): whether a gateway address is
// present on a node's realized VNet bridge is a structural configuration
// fact (from the same host-netlink poll every other structural check
// already reads), not a noisy live counter — there is nothing to debounce.
// A finding fires the instant realization diverges and clears the instant
// every member node agrees again.

package findings

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/xnode"
)

const CheckEvpnGwInconsistency = "evpn_gw_inconsistency"

const evpnGwInconsistencyDocsLink = "docs/features/monitoring.md#5-health-checks"

// checkEvpnGwInconsistency returns one finding per (evpn zone, subnet) whose
// gateway is present on the VNet's realized bridge on at least one member
// node and absent (or the bridge is missing) on at least one other member
// node — a real split, not merely "not realized anywhere yet" (that
// all-absent case is `sdn.evpn_gateway_missing`'s job at validate time, not
// this continuous check's).
func checkEvpnGwInconsistency(snap inventory.Snapshot) []Finding {
	zones := map[string]*inventory.SdnZone{}
	vnets := map[string]*inventory.SdnVnet{}
	var subnets []*inventory.SdnSubnet
	for _, e := range snap.All() {
		switch v := e.(type) {
		case *inventory.SdnZone:
			zones[v.ID] = v
		case *inventory.SdnVnet:
			vnets[v.ID] = v
		case *inventory.SdnSubnet:
			subnets = append(subnets, v)
		}
	}

	bridgesByName := xnode.BridgesByName(snap)

	var out []Finding
	for _, sub := range subnets {
		if sub.Gateway == "" {
			continue
		}
		vnet, ok := vnets[sub.Vnet]
		if !ok {
			continue
		}
		zone, ok := zones[vnet.Zone]
		if !ok || zone.Type != "evpn" {
			continue
		}
		members := sortedUnique(zone.Nodes)
		if len(members) < 2 {
			continue // nothing to be inconsistent across
		}

		byNode := bridgesByName[vnet.ID]
		var present, absent []string
		for _, node := range members {
			if br, ok := byNode[node]; ok && bridgeHasAddress(br, sub.Gateway) {
				present = append(present, node)
			} else {
				absent = append(absent, node)
			}
		}
		if len(present) == 0 || len(absent) == 0 {
			continue // consistent: everywhere or nowhere
		}

		detail := fmt.Sprintf(
			"EVPN zone %s's anycast gateway %s for vnet %s is realized on %s but not on %s — routed traffic through this subnet may be silently broken on the nodes missing it",
			zone.ID, sub.Gateway, vnet.ID, strings.Join(present, ", "), strings.Join(absent, ", "))
		refs := []string{zone.GetRef().String(), vnet.GetRef().String(), sub.GetRef().String()}
		f := newHealthFinding(CheckEvpnGwInconsistency, SeverityWarning, detail, append(append([]string(nil), present...), absent...), refs)
		f.DocsLink = evpnGwInconsistencyDocsLink
		out = append(out, f)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// bridgeHasAddress reports whether br declares gatewayIP among its
// addresses (each entry is "ip" or "ip/prefix" — the prefix, if any, is
// ignored for this comparison).
func bridgeHasAddress(br *inventory.Bridge, gatewayIP string) bool {
	for _, addr := range br.Addresses {
		ip, _, _ := strings.Cut(addr, "/")
		if ip == gatewayIP {
			return true
		}
	}
	return false
}
