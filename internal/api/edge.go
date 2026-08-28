// SPDX-License-Identifier: Apache-2.0

// edge.go implements T-1403's Edge & NAT cockpit read views: GET
// /edge/routes and GET /edge/nat. Both are netRead-gated, accept no request
// body, and mount no netWrite-capable route at all — every mutation to
// NAT/route state flows through the ordinary nat.*/route.static.* changeset
// ops (internal/change), never a dedicated write route here (T-1403's own
// safety note: "surfacing inbound exposure must not itself become a write
// path"). Both handlers are thin adapters over internal/edge's pure
// projection functions: gather already-collected per-node interfaces-file
// content (the same read T-208's raw editor uses,
// ChangesetService.ReadRawInterfaces) plus the live SDN tree
// (SDNService.Tree) and an optional IPAM-based guest correlation, then call
// edge.ProjectRoutes/ProjectNAT.

package api

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/edge"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/ipam"
	"github.com/bgovanlu/vnprox/internal/sdn"
)

// EdgeInterfacesSource is the per-node raw-interfaces-file read GET
// /edge/routes and GET /edge/nat need — exactly ChangesetService's own
// ReadRawInterfaces (T-208's raw editor "open" call), reused verbatim so
// there is no second interfaces-file read path: this route only ever
// parses what NodeAgent already writes/reads, never a shadow copy.
type EdgeInterfacesSource interface {
	ReadRawInterfaces(ctx context.Context, node string) (content, hash string, err error)
}

// EdgeGraph resolves cluster node names and guest status for the port-
// forward -> guest correlation — the same live *inventory.Graph
// GuestInteriorGraph already wires in elsewhere (its Snapshot method
// satisfies this directly).
type EdgeGraph interface {
	Snapshot() inventory.Snapshot
}

// EdgeIPAMSource backs the port-forward -> guest correlation (T-1403's own
// exit-demo scenario: a port-forward pointing at a currently powered-off
// guest, flagged distinctly): *ipam.Service's existing AllAllocations
// method (already exported for T-406's DHCP-range-overlap check). nil
// simply omits guest correlation (every port-forward's target reports
// unresolved) rather than failing the whole read — the same optional-
// dependency degrade-gracefully convention every other read view in this
// package follows.
type EdgeIPAMSource interface {
	AllAllocations(ctx context.Context) (map[string][]ipam.Allocation, error)
}

// mountEdgeRoutes registers GET /edge/routes and GET /edge/nat (docs/api.md's
// Edge section). ifacesSrc/graph are required (nil simply omits both
// routes, matching every other mountXRoutes function's degraded-mode
// convention for a daemon running without its underlying dependency
// wired); sdnSvc/ipamSrc are optional (nil narrows the response rather than
// failing it — SDN simple-zone-NAT rows or guest correlation are simply
// omitted).
func mountEdgeRoutes(r chi.Router, ifacesSrc EdgeInterfacesSource, sdnSvc SDNService, graph EdgeGraph, ipamSrc EdgeIPAMSource, auth AuthService) {
	if ifacesSrc == nil || graph == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/edge/routes", handleEdgeRoutes(ifacesSrc, graph))
		r.Get("/edge/nat", handleEdgeNAT(ifacesSrc, sdnSvc, graph, ipamSrc))
	})
}

// clusterNodeNames returns every KindNode entity's name in snap, sorted for
// deterministic response ordering.
func clusterNodeNames(snap inventory.Snapshot) []string {
	var out []string
	for _, ent := range snap.All() {
		if ent.GetRef().Kind == inventory.KindNode {
			out = append(out, ent.GetRef().ID)
		}
	}
	sort.Strings(out)
	return out
}

// gatherNodeInterfaces reads every cluster node's current interfaces-file
// content via ifacesSrc, skipping (not failing the whole response for) a
// node whose read errors — the same "an unreachable peer's band degrades
// independently" tolerance docs/api.md's staleness section documents for
// other cluster-fan-out reads.
func gatherNodeInterfaces(ctx context.Context, ifacesSrc EdgeInterfacesSource, nodes []string) []edge.NodeInterfaces {
	out := make([]edge.NodeInterfaces, 0, len(nodes))
	for _, n := range nodes {
		content, _, err := ifacesSrc.ReadRawInterfaces(ctx, n)
		if err != nil {
			continue
		}
		out = append(out, edge.NodeInterfaces{Node: n, Content: content})
	}
	return out
}

func handleEdgeRoutes(ifacesSrc EdgeInterfacesSource, graph EdgeGraph) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodes := clusterNodeNames(graph.Snapshot())
		inputs := gatherNodeInterfaces(r.Context(), ifacesSrc, nodes)
		view, err := edge.ProjectRoutes(inputs)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "edge_projection_failed", "could not project edge routes")
			return
		}
		view.GeneratedAt = time.Now().Unix()
		writeJSON(w, http.StatusOK, view)
	}
}

func handleEdgeNAT(ifacesSrc EdgeInterfacesSource, sdnSvc SDNService, graph EdgeGraph, ipamSrc EdgeIPAMSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		snap := graph.Snapshot()
		nodes := clusterNodeNames(snap)
		inputs := gatherNodeInterfaces(ctx, ifacesSrc, nodes)

		var subnets []edge.SDNSubnetInput
		if sdnSvc != nil {
			if tree, err := sdnSvc.Tree(ctx); err == nil {
				subnets = flattenSDNSubnets(tree.Zones)
			}
		}

		var lookup edge.GuestLookup
		if ipamSrc != nil {
			if allocs, err := ipamSrc.AllAllocations(ctx); err == nil {
				lookup = buildGuestLookup(allocs, snap)
			}
		}

		view, err := edge.ProjectNAT(inputs, subnets, lookup)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "edge_projection_failed", "could not project edge NAT")
			return
		}
		view.GeneratedAt = time.Now().Unix()
		writeJSON(w, http.StatusOK, view)
	}
}

// flattenSDNSubnets flattens internal/sdn.Service.Tree's nested zones ->
// vnets -> subnets shape into edge.ProjectNAT's flat input, carrying the
// owning zone's type along with each subnet (the "simple zone" filter
// ProjectNAT applies).
func flattenSDNSubnets(zones []sdn.Zone) []edge.SDNSubnetInput {
	var out []edge.SDNSubnetInput
	for _, z := range zones {
		for _, v := range z.Vnets {
			for _, s := range v.Subnets {
				out = append(out, edge.SDNSubnetInput{
					Zone: z.ID, ZoneType: z.Type, Vnet: v.ID, CIDR: s.CIDR, Gateway: s.Gateway, SNAT: s.SNAT,
				})
			}
		}
	}
	return out
}

// buildGuestLookup adapts a raw IPAM allocation map (CIDR -> allocations)
// plus the live inventory snapshot into an edge.GuestLookup: resolve an
// IntIP to the VMID an allocation records, then to that VMID's known Guest
// entity for its ref and running/stopped status. An IP with no allocation,
// or an allocation whose VMID names no currently known guest, correlates to
// nothing — ProjectNAT then simply leaves that port-forward's target
// unresolved rather than guessing.
func buildGuestLookup(allocs map[string][]ipam.Allocation, snap inventory.Snapshot) edge.GuestLookup {
	byVMID := make(map[int]*inventory.Guest)
	for _, ent := range snap.All() {
		if g, ok := ent.(*inventory.Guest); ok {
			byVMID[g.VMID] = g
		}
	}
	ipToVMID := make(map[string]int)
	for _, list := range allocs {
		for _, a := range list {
			if a.VMID != 0 {
				ipToVMID[a.IP] = a.VMID
			}
		}
	}
	return func(ip string) (string, bool, bool) {
		vmid, ok := ipToVMID[ip]
		if !ok {
			return "", false, false
		}
		g, ok := byVMID[vmid]
		if !ok {
			return "", false, false
		}
		return g.GetRef().String(), g.Status != "running", true
	}
}
