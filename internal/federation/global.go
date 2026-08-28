// SPDX-License-Identifier: Apache-2.0

package federation

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// DefaultSearchLimit caps how many merged, cluster-namespaced hits
// GET /federation/search returns — the same order of magnitude the
// single-cluster GET /inventory/search cap uses, applied across the merged
// set so one very-large cluster can't crowd every sibling out.
const DefaultSearchLimit = 50

// ClusterSummary is one attached cluster's capsule-level rollup for the
// global map (T-1202): enough to draw a cluster capsule (name, aggregate
// findings count, drift status, unreachable indicator) without fetching that
// cluster's full topology. The full GET /topology-equivalent payload is
// fetched lazily on drill-down (ClusterTopology), never inlined here.
//
// Findings/Drift are derived only from PVE-observable health at the summary
// altitude — an offline member node is a genuine, cheaply-observed degraded
// condition (each counts as one finding and sets Drift). Deeper per-cluster
// findings/drift analysis stays behind drill-down, where that cluster's full
// projection is available; the summary deliberately stays a two-call
// (status + resources) fan-out so a cluster capsule can never be the slow
// part of the global view.
type ClusterSummary struct {
	ClusterID   string `json:"clusterId"`
	ClusterName string `json:"clusterName"`
	Nodes       int    `json:"nodes"`
	NodesOnline int    `json:"nodesOnline"`
	Guests      int    `json:"guests"`
	Findings    int    `json:"findings"`
	Reachable   bool   `json:"reachable"`
	Drift       bool   `json:"drift"`
}

// TopologySummary fans a two-call (GET /cluster/status + GET /cluster/
// resources) rollup out to every attached cluster and returns one
// ClusterSummary per cluster — including the unreachable ones, tagged
// Reachable:false so the frontend can render their capsule greyed rather
// than dropping them from the global map. partial/failedClusters mirror the
// existing failure-isolation envelope (one slow/dead cluster never blanks
// the whole response). Results are cluster-list order; the caller sorts for
// display if it wants a stable order.
func (a *Aggregator) TopologySummary(ctx context.Context) (summaries []ClusterSummary, partial bool, failedClusters []string, err error) {
	clusters, err := a.svc.List(ctx)
	if err != nil {
		return nil, false, nil, err
	}
	raw := fanOut(ctx, a, clusters, func(ctx context.Context, r PVEReader) (ClusterSummary, error) {
		status, serr := r.ClusterStatus(ctx)
		if serr != nil {
			return ClusterSummary{}, serr
		}
		resources, rerr := r.ClusterResources(ctx)
		if rerr != nil {
			return ClusterSummary{}, rerr
		}
		return summarize(status, resources), nil
	})

	summaries = make([]ClusterSummary, 0, len(raw))
	for _, res := range raw {
		if res.err != nil {
			partial = true
			failedClusters = append(failedClusters, res.cluster.ID)
			a.svc.SetStatus(ctx, res.cluster.ID, "unreachable")
			a.log.Debug("federation: cluster unreachable during topology summary", "cluster", res.cluster.ID, "error", res.err)
			summaries = append(summaries, ClusterSummary{ClusterID: res.cluster.ID, ClusterName: res.cluster.Name, Reachable: false})
			continue
		}
		a.svc.SetStatus(ctx, res.cluster.ID, "ok")
		s := res.data
		s.ClusterID = res.cluster.ID
		s.ClusterName = res.cluster.Name
		s.Reachable = true
		summaries = append(summaries, s)
	}
	return summaries, partial, failedClusters, nil
}

// summarize folds one cluster's status+resources into a ClusterSummary (sans
// the cluster identity fields, which the caller stamps on). Offline member
// nodes are the summary altitude's one honest, PVE-observable finding.
func summarize(status []pve.ClusterStatusEntry, resources []pve.ClusterResource) ClusterSummary {
	var s ClusterSummary
	for _, e := range status {
		if e.Type != "node" {
			continue
		}
		s.Nodes++
		if e.Online {
			s.NodesOnline++
		}
	}
	for _, r := range resources {
		if r.Type == string(pve.GuestQemu) || r.Type == string(pve.GuestLXC) {
			s.Guests++
		}
	}
	s.Findings = s.Nodes - s.NodesOnline
	if s.Findings < 0 {
		s.Findings = 0
	}
	s.Drift = s.Findings > 0
	return s
}

// SearchHit is one cluster-namespaced global-search result: the same shape a
// single-cluster GET /inventory/search row carries, plus the clusterId/
// clusterName the palette groups by (T-1202). Ref is an inventory Ref
// triplet string, unique within its own cluster; the (clusterId, ref) pair
// is globally unique.
type SearchHit struct {
	ClusterID    string `json:"clusterId"`
	ClusterName  string `json:"clusterName"`
	Ref          string `json:"ref"`
	Kind         string `json:"kind"`
	Label        string `json:"label"`
	Node         string `json:"node"`
	MatchedField string `json:"matchedField"`
}

// Search fans a query out to every attached cluster and returns merged,
// cluster-namespaced hits (T-1202: "GET /federation/search fans /inventory/
// search out per attached cluster via the aggregator, namespacing each
// result with clusterId/clusterName"). Each cluster is searched over its own
// PVE-observable inventory (member nodes and guests, by name/VMID) — the
// cross-cluster counterpart to the local spotlight search, scoped to what
// the aggregator can read over a cluster's PVE API without a full inventory
// build. partial/failedClusters isolate a dead cluster exactly as every
// other fan-out does. A blank query returns no hits without any fan-out.
func (a *Aggregator) Search(ctx context.Context, q string, limit int) (hits []SearchHit, partial bool, failedClusters []string, err error) {
	needle := strings.ToLower(strings.TrimSpace(q))
	if needle == "" {
		return nil, false, nil, nil
	}
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	clusters, err := a.svc.List(ctx)
	if err != nil {
		return nil, false, nil, err
	}
	raw := fanOut(ctx, a, clusters, func(ctx context.Context, r PVEReader) ([]pve.ClusterResource, error) {
		return r.ClusterResources(ctx)
	})

	for _, res := range raw {
		if res.err != nil {
			partial = true
			failedClusters = append(failedClusters, res.cluster.ID)
			a.log.Debug("federation: cluster unreachable during search", "cluster", res.cluster.ID, "error", res.err)
			continue
		}
		hits = append(hits, matchResources(res.cluster, needle, res.data)...)
	}

	// Stable order: cluster name, then label, so the palette's grouping is
	// deterministic across requests regardless of fan-out completion order.
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].ClusterName != hits[j].ClusterName {
			return hits[i].ClusterName < hits[j].ClusterName
		}
		return hits[i].Label < hits[j].Label
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, partial, failedClusters, nil
}

// matchResources builds the cluster-namespaced hits for one cluster's
// resources whose name/VMID contains needle (already lower-cased/trimmed).
func matchResources(c Cluster, needle string, resources []pve.ClusterResource) []SearchHit {
	var out []SearchHit
	for _, r := range resources {
		switch r.Type {
		case "node":
			if strings.Contains(strings.ToLower(r.Name), needle) {
				out = append(out, SearchHit{
					ClusterID: c.ID, ClusterName: c.Name,
					Ref:  inventory.Ref{Kind: inventory.KindNode, Node: r.Node, ID: r.Node}.String(),
					Kind: string(inventory.KindNode), Label: r.Name, Node: r.Node, MatchedField: "name",
				})
			}
		case string(pve.GuestQemu), string(pve.GuestLXC):
			vmid := strconv.Itoa(r.VMID)
			field := ""
			switch {
			case r.Name != "" && strings.Contains(strings.ToLower(r.Name), needle):
				field = "name"
			case strings.Contains(vmid, needle):
				field = "vmid"
			}
			if field == "" {
				continue
			}
			label := r.Name
			if label == "" {
				label = vmid
			}
			out = append(out, SearchHit{
				ClusterID: c.ID, ClusterName: c.Name,
				Ref:  inventory.Ref{Kind: inventory.KindGuest, Node: r.Node, ID: vmid}.String(),
				Kind: string(inventory.KindGuest), Label: label, Node: r.Node, MatchedField: field,
			})
		}
	}
	return out
}

// TopologyProjector is the wider slice of *pve.Client a single cluster's
// lazy drill-down projection reads. *pve.Client satisfies it directly.
type TopologyProjector interface {
	ClusterStatus(ctx context.Context) ([]pve.ClusterStatusEntry, error)
	ClusterResources(ctx context.Context) ([]pve.ClusterResource, error)
	ListNodeNetwork(ctx context.Context, node string) ([]pve.NetworkInterface, error)
	ListSDNZones(ctx context.Context) ([]pve.SDNZone, error)
	ListSDNVnets(ctx context.Context) ([]pve.SDNVnet, error)
	ListSDNSubnets(ctx context.Context, vnet string) ([]pve.SDNSubnet, error)
}

// ClusterTopology builds one attached cluster's full topology projection on
// demand — the lazy drill-down the global map fetches when the operator
// clicks a capsule (T-1202: "a given cluster's full GET /topology payload is
// fetched lazily on drill-down, not inlined"). It is projected purely from
// what the aggregator can read over that cluster's PVE API (member nodes,
// their PVE-parsed network config, guests, and the SDN tree) — host-only
// signals (LLDP neighbours, netlink live state) belong to a node's own
// vnproxd and are simply absent from a remote projection, exactly like a
// cluster with no LLDP data renders on its own map (docs/features/
// topology.md §5). The returned Topology matches GET /topology's contract so
// the frontend renders it through the unchanged single-cluster canvas.
func (a *Aggregator) ClusterTopology(ctx context.Context, id string) (topology.Topology, error) {
	c, err := a.svc.Get(ctx, id)
	if err != nil {
		return topology.Topology{}, err
	}
	proj, err := a.newProjector(ctx, c)
	if err != nil {
		return topology.Topology{}, fmt.Errorf("federation: building projector for cluster %s: %w", id, err)
	}
	return projectCluster(ctx, a, proj)
}

// projectCluster runs the PVE-API-only ingest into a fresh inventory graph
// and projects it. SDN reads are best-effort — a cluster with SDN
// unconfigured or unreachable at the SDN endpoint still yields its physical/
// L2/guest topology rather than erroring the whole drill-down.
func projectCluster(ctx context.Context, a *Aggregator, proj TopologyProjector) (topology.Topology, error) {
	status, err := proj.ClusterStatus(ctx)
	if err != nil {
		return topology.Topology{}, fmt.Errorf("federation: cluster status: %w", err)
	}
	graph := inventory.NewGraph()

	var nodes []string
	for _, e := range status {
		if e.Type != "node" {
			continue
		}
		nodes = append(nodes, e.Name)
	}
	graph.ApplyPoll(inventory.SourcePVECluster, inventory.Scope{}, inventory.FromClusterStatus(status))

	for _, n := range nodes {
		ifaces, nerr := proj.ListNodeNetwork(ctx, n)
		if nerr != nil {
			a.log.Debug("federation: projecting node network", "node", n, "error", nerr)
			continue
		}
		graph.ApplyPoll(inventory.SourcePVENetwork, inventory.Scope{Node: n}, inventory.FromPVENetwork(n, ifaces))
	}

	resources, rerr := proj.ClusterResources(ctx)
	if rerr != nil {
		return topology.Topology{}, fmt.Errorf("federation: cluster resources: %w", rerr)
	}
	graph.ApplyPoll(inventory.SourcePVEGuest, inventory.Scope{Kinds: []inventory.Kind{inventory.KindGuest, inventory.KindGuestNic}},
		inventory.FromPVEGuests(resources, nil))

	if sdn := projectSDN(ctx, a, proj); len(sdn) > 0 {
		graph.ApplyPoll(inventory.SourcePVESDN, inventory.Scope{}, sdn)
	}

	return topology.Project(graph.Snapshot(), topology.Filter{}), nil
}

// projectSDN reads the SDN tree best-effort; any error yields no SDN
// entities rather than failing the drill-down.
func projectSDN(ctx context.Context, a *Aggregator, proj TopologyProjector) []inventory.Entity {
	zones, err := proj.ListSDNZones(ctx)
	if err != nil {
		a.log.Debug("federation: projecting SDN zones", "error", err)
		return nil
	}
	vnets, err := proj.ListSDNVnets(ctx)
	if err != nil {
		a.log.Debug("federation: projecting SDN vnets", "error", err)
		return nil
	}
	subnets := map[string][]pve.SDNSubnet{}
	for _, v := range vnets {
		subs, serr := proj.ListSDNSubnets(ctx, v.ID)
		if serr != nil {
			a.log.Debug("federation: projecting SDN subnets", "vnet", v.ID, "error", serr)
			continue
		}
		subnets[v.ID] = subs
	}
	// No "?pending=1" reads here: this is a best-effort, on-demand
	// cross-cluster drill-down projection (this function's own doc
	// comment), not the poll-cached inventory graph internal/collect.pollSDN
	// builds — nil pending maps read back as pve.PendingNone for every id,
	// same as internal/collect's own pending-fetch failure fallback.
	return inventory.FromPVESDN(zones, vnets, subnets, nil, nil, nil, nil)
}
