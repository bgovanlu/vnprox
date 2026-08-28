// SPDX-License-Identifier: Apache-2.0

package federation

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/store"
)

// DefaultAggregateTimeout bounds one per-cluster read within a fan-out, so a
// single slow/hung cluster can never stall the whole aggregate — it just
// times out and lands in failedClusters like any other unreachable cluster.
const DefaultAggregateTimeout = 15 * time.Second

// PVEReader is the subset of *pve.Client the Aggregator's built-in reads use.
// *pve.Client satisfies it directly, so production wiring needs no adapter;
// tests can also inject a fake to script a specific failure without a mock
// server. ClusterResources backs the global-topology summary and search
// fan-outs (T-1202); ClusterStatus backs node-list/audit reachability. The
// SDN reads back the cross-cluster IPAM view (T-1203): each attached cluster's
// configured SDN subnet CIDRs, the per-cluster input the cross-cluster
// duplicate-subnet check folds together.
type PVEReader interface {
	ClusterStatus(ctx context.Context) ([]pve.ClusterStatusEntry, error)
	ClusterResources(ctx context.Context) ([]pve.ClusterResource, error)
	ListSDNVnets(ctx context.Context) ([]pve.SDNVnet, error)
	ListSDNSubnets(ctx context.Context, vnet string) ([]pve.SDNSubnet, error)
}

// ReaderFactory builds a PVEReader for one attached cluster (typically by
// unsealing its credential — Service.ClientFor). Returning an error counts as
// that cluster failing this fan-out, isolated exactly like a live read error.
type ReaderFactory func(ctx context.Context, c Cluster) (PVEReader, error)

// Aggregator fans reads out to every attached cluster concurrently, with
// per-cluster failure isolation: an unreachable or erroring cluster is named
// in failedClusters and the aggregate is flagged partial, but it never blanks
// or errors the whole response — every other cluster's data still renders
// (docs/api.md's partial/failedClusters convention, mirroring the existing
// partial/failedNodes peer fan-out envelope).
type Aggregator struct {
	svc          *Service
	newReader    ReaderFactory
	newProjector ProjectorFactory
	tunnelHealth TunnelHealth
	log          *slog.Logger
	timeout      time.Duration
}

// ProjectorFactory builds a TopologyProjector for one attached cluster (the
// wider PVE read surface a lazy drill-down projection needs; default:
// Service.ClientFor). A test seam, mirroring ReaderFactory.
type ProjectorFactory func(ctx context.Context, c Cluster) (TopologyProjector, error)

// TunnelHealth is the Aggregator's seam onto live WireGuard tunnel state
// (T-1407) — just enough to answer "is the tunnel a cluster is linked to
// currently down". It carries no import of internal/wireguard or
// internal/findings: cmd/vnproxd's concrete adapter does that work (reading
// the live wg-show-dump poll and resolving Cluster.WgTunnelID to a tunnel's
// node/interface), keeping this package's only knowledge of WireGuard to
// "some opaque tunnel id string". A nil TunnelHealth (the default) means
// every cluster is treated as not tunnel-linked-down — the feature quietly
// no-ops, the same degradation every other optional Aggregator/Engine seam
// in this codebase uses.
type TunnelHealth interface {
	// TunnelDown reports whether the tunnel identified by tunnelID
	// (Cluster.WgTunnelID) is currently down — no live peer of that tunnel
	// has handshaked within the staleness threshold
	// findings.WgHandshakeStaleThreshold also uses, so the Aggregator's
	// suppression and the tunnel_down_peer_unreachable finding can never
	// disagree about which tunnels are down (T-1407 AC2). An unresolvable
	// tunnelID (e.g. deleted after linking) should report false — a
	// dangling link degrades to ordinary PVE-reachability handling rather
	// than silently and permanently hiding a cluster's data.
	TunnelDown(tunnelID string) bool
}

// WithProjectorFactory overrides how per-cluster TopologyProjectors are built
// (default: Service.ClientFor). Primarily a test seam.
func WithProjectorFactory(f ProjectorFactory) AggregatorOption {
	return func(a *Aggregator) { a.newProjector = f }
}

// AggregatorOption tunes an Aggregator at construction.
type AggregatorOption func(*Aggregator)

// WithReaderFactory overrides how per-cluster PVEReaders are built (default:
// Service.ClientFor). Primarily a test seam for injecting scripted failures.
func WithReaderFactory(f ReaderFactory) AggregatorOption {
	return func(a *Aggregator) { a.newReader = f }
}

// WithTimeout overrides the per-cluster read timeout (default
// DefaultAggregateTimeout).
func WithTimeout(d time.Duration) AggregatorOption {
	return func(a *Aggregator) {
		if d > 0 {
			a.timeout = d
		}
	}
}

// WithTunnelHealth wires T-1407's tunnel-liveness seam (default: nil, which
// treats every cluster as not tunnel-linked-down).
func WithTunnelHealth(h TunnelHealth) AggregatorOption {
	return func(a *Aggregator) { a.tunnelHealth = h }
}

// NewAggregator builds an Aggregator over svc's registered clusters.
func NewAggregator(svc *Service, opts ...AggregatorOption) *Aggregator {
	a := &Aggregator{svc: svc, log: svc.log, timeout: DefaultAggregateTimeout}
	a.newReader = func(ctx context.Context, c Cluster) (PVEReader, error) {
		return svc.ClientFor(ctx, c.ID)
	}
	a.newProjector = func(ctx context.Context, c Cluster) (TopologyProjector, error) {
		return svc.ClientFor(ctx, c.ID)
	}
	for _, o := range opts {
		o(a)
	}
	return a
}

// splitTunnelDown partitions clusters into those to fan reads out to
// normally and those whose linked WireGuard tunnel is currently down
// (T-1407). A tunnel-down cluster is not attempted at all — it contributes
// no data to the caller's aggregate, but (unlike an ordinary unreachable
// cluster) is deliberately NOT added to that caller's partial/failedClusters
// envelope: the one tunnel_down_peer_unreachable finding (computed
// independently by internal/findings from the identical TunnelHealth
// definition) already names it, so three separate per-surface "unreachable"
// flags across topology/audit/IPAM-conflict reads would just be redundant
// noise for the same root cause (T-1407 AC2). A cluster with no WgTunnelID
// set, or when tunnelHealth is nil, always falls through to the ordinary
// path (T-1407 AC3's regression case).
func (a *Aggregator) splitTunnelDown(clusters []Cluster) (reachable []Cluster, tunnelDown []Cluster) {
	if a.tunnelHealth == nil {
		return clusters, nil
	}
	reachable = make([]Cluster, 0, len(clusters))
	for _, c := range clusters {
		if c.WgTunnelID != "" && a.tunnelHealth.TunnelDown(c.WgTunnelID) {
			tunnelDown = append(tunnelDown, c)
			continue
		}
		reachable = append(reachable, c)
	}
	return reachable, tunnelDown
}

// NodeInfo is one cluster member node as observed via GET /cluster/status.
type NodeInfo struct {
	Name   string `json:"name"`
	IP     string `json:"ip,omitempty"`
	Online bool   `json:"online"`
}

// ClusterNodes is one attached cluster's node list, tagged with the cluster
// it came from — the correct clusterId/clusterName tagging every aggregate
// read carries (T-1201 AC2).
type ClusterNodes struct {
	ClusterID   string     `json:"clusterId"`
	ClusterName string     `json:"clusterName"`
	Nodes       []NodeInfo `json:"nodes"`
}

// clusterResult pairs one cluster's fan-out result with the cluster it came
// from. err is set iff that cluster's read failed.
type clusterResult[T any] struct {
	err     error
	data    T
	cluster Cluster
}

// fanOut runs fn against every cluster concurrently, each under its own
// timeout, and returns the per-cluster results in cluster-list order. A
// cluster whose reader build or fn call errors carries that error in its
// result; the caller decides how to fold errors into partial/failedClusters
// (every current caller does so identically — see resultsToFailed).
func fanOut[T any](ctx context.Context, a *Aggregator, clusters []Cluster, fn func(ctx context.Context, r PVEReader) (T, error)) []clusterResult[T] {
	results := make([]clusterResult[T], len(clusters))
	var wg sync.WaitGroup
	for i, c := range clusters {
		wg.Add(1)
		go func(i int, c Cluster) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, a.timeout)
			defer cancel()
			results[i].cluster = c
			reader, err := a.newReader(cctx, c)
			if err != nil {
				results[i].err = err
				return
			}
			data, err := fn(cctx, reader)
			if err != nil {
				results[i].err = err
				return
			}
			results[i].data = data
		}(i, c)
	}
	wg.Wait()
	return results
}

// ClusterNodesAll fans GET /cluster/status out to every attached cluster and
// returns each reachable cluster's node list tagged with its clusterId, plus
// partial/failedClusters for any that were unreachable (T-1201 AC2/AC3). It
// also refreshes each cluster's status cache as a side effect. A store-level
// failure to even list the registered clusters is the one hard error.
func (a *Aggregator) ClusterNodesAll(ctx context.Context) (results []ClusterNodes, partial bool, failedClusters []string, err error) {
	clusters, err := a.svc.List(ctx)
	if err != nil {
		return nil, false, nil, err
	}
	clusters, tunnelDown := a.splitTunnelDown(clusters)
	for _, c := range tunnelDown {
		a.log.Debug("federation: cluster excluded from aggregation, linked tunnel is down", "cluster", c.ID, "tunnel", c.WgTunnelID)
	}
	raw := fanOut(ctx, a, clusters, func(ctx context.Context, r PVEReader) ([]NodeInfo, error) {
		status, serr := r.ClusterStatus(ctx)
		if serr != nil {
			return nil, serr
		}
		return nodesFromStatus(status), nil
	})

	results = make([]ClusterNodes, 0, len(raw))
	for _, res := range raw {
		if res.err != nil {
			partial = true
			failedClusters = append(failedClusters, res.cluster.ID)
			a.svc.SetStatus(ctx, res.cluster.ID, "unreachable")
			a.log.Debug("federation: cluster unreachable during aggregation", "cluster", res.cluster.ID, "error", res.err)
			continue
		}
		a.svc.SetStatus(ctx, res.cluster.ID, "ok")
		results = append(results, ClusterNodes{ClusterID: res.cluster.ID, ClusterName: res.cluster.Name, Nodes: res.data})
	}
	return results, partial, failedClusters, nil
}

// NodeClusters implements internal/change.ClusterMembershipSource: the node
// name -> clusterId map the cross-cluster changeset-scoping check needs
// (validate_crosscluster.go). Built by fanning GET /cluster/status out to
// every attached cluster; an unreachable cluster simply contributes no
// entries (best-effort membership), so this never fails on a single cluster
// being down.
//
// PVE node names are NOT globally unique across clusters (every shipped
// fixture, and plenty of real deployments, name their first node "pve1"), so
// a name observed in more than one attached cluster is ambiguous: it is
// deliberately OMITTED from the map rather than attributed to whichever
// cluster was polled last. The scoping check reads an unknown node as "leave
// it alone, never guess", so omitting an ambiguous name is the safe choice —
// it can never false-reject a legitimate same-cluster op just because a
// same-named node happens to exist in a sibling cluster. A name unique to one
// reachable cluster maps to that cluster.
func (a *Aggregator) NodeClusters(ctx context.Context) (map[string]string, error) {
	clusters, err := a.svc.List(ctx)
	if err != nil {
		return nil, err
	}
	raw := fanOut(ctx, a, clusters, func(ctx context.Context, r PVEReader) ([]NodeInfo, error) {
		status, serr := r.ClusterStatus(ctx)
		if serr != nil {
			return nil, serr
		}
		return nodesFromStatus(status), nil
	})
	// First pass: count how many distinct clusters claim each node name.
	owners := map[string]string{}
	ambiguous := map[string]bool{}
	for _, res := range raw {
		if res.err != nil {
			continue
		}
		for _, n := range res.data {
			if prev, seen := owners[n.Name]; seen && prev != res.cluster.ID {
				ambiguous[n.Name] = true
			}
			owners[n.Name] = res.cluster.ID
		}
	}
	out := make(map[string]string, len(owners))
	for name, cid := range owners {
		if ambiguous[name] {
			continue
		}
		out[name] = cid
	}
	return out, nil
}

// ClusterSubnets is one attached cluster's configured SDN subnet CIDR set,
// tagged with the cluster it came from — the per-cluster input the
// cross-cluster IPAM duplicate-subnet check (T-1203) folds together. It is
// deliberately the same shape as internal/ipam.ClusterSubnets, which the API
// layer maps this into to run the actual overlap computation (keeping this
// package free of an internal/ipam dependency).
type ClusterSubnets struct {
	ClusterID   string   `json:"clusterId"`
	ClusterName string   `json:"clusterName"`
	CIDRs       []string `json:"cidrs"`
}

// IPAMSubnets fans the per-cluster SDN subnet enumeration out to every
// attached cluster and returns each reachable cluster's configured subnet
// CIDRs tagged with its clusterId, plus partial/failedClusters for any that
// were unreachable (the cross-cluster IPAM conflict view, T-1203). Failure
// isolation is identical to ClusterNodesAll: one cluster being down never
// blanks the others' contribution. A store-level failure to even list the
// registered clusters is the one hard error.
func (a *Aggregator) IPAMSubnets(ctx context.Context) (results []ClusterSubnets, partial bool, failedClusters []string, err error) {
	clusters, err := a.svc.List(ctx)
	if err != nil {
		return nil, false, nil, err
	}
	clusters, _ = a.splitTunnelDown(clusters)
	raw := fanOut(ctx, a, clusters, func(ctx context.Context, r PVEReader) ([]string, error) {
		return sdnSubnetCIDRs(ctx, r)
	})

	results = make([]ClusterSubnets, 0, len(raw))
	for _, res := range raw {
		if res.err != nil {
			partial = true
			failedClusters = append(failedClusters, res.cluster.ID)
			a.log.Debug("federation: cluster unreachable during IPAM subnet aggregation", "cluster", res.cluster.ID, "error", res.err)
			continue
		}
		results = append(results, ClusterSubnets{ClusterID: res.cluster.ID, ClusterName: res.cluster.Name, CIDRs: res.data})
	}
	return results, partial, failedClusters, nil
}

// sdnSubnetCIDRs reads every SDN vnet's subnets from one cluster and returns
// the flat CIDR list. One vnet's subnet listing failing is tolerated (skipped)
// — matches internal/ipam.Service.sdnSubnets' own per-vnet tolerance — but a
// failure to even list the vnets is the cluster's fan-out error.
func sdnSubnetCIDRs(ctx context.Context, r PVEReader) ([]string, error) {
	vnets, err := r.ListSDNVnets(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, v := range vnets {
		subnets, serr := r.ListSDNSubnets(ctx, v.ID)
		if serr != nil {
			continue
		}
		for _, sub := range subnets {
			if sub.CIDR != "" {
				out = append(out, sub.CIDR)
			}
		}
	}
	return out, nil
}

// nodesFromStatus extracts the member-node rows (Type == "node") from a
// GET /cluster/status response, dropping the summary "cluster" row.
func nodesFromStatus(status []pve.ClusterStatusEntry) []NodeInfo {
	var out []NodeInfo
	for _, e := range status {
		if e.Type != "node" {
			continue
		}
		out = append(out, NodeInfo{Name: e.Name, IP: e.IP, Online: e.Online})
	}
	return out
}

// --- Global audit trail (T-1201 AC5) ---

// AuditSource is the node-local audit store the cluster-dimension merge reads
// per cluster. *store.AuditRepo satisfies it directly.
type AuditSource interface {
	ListPage(ctx context.Context, filter store.AuditFilter, cursor string, limit int) ([]store.AuditEntry, string, error)
}

// AuditRow is one merged audit entry, always carrying the clusterId it was
// tagged with.
type AuditRow struct {
	ClusterID string
	Entry     store.AuditEntry
}

// Audit merges the audit rows of every attached cluster into one newest-first
// page (docs/architecture §7's cluster dimension). Each cluster is first
// probed for reachability (GET /cluster/status); a reachable cluster
// contributes its own cluster_id-filtered slice of the local audit store,
// while an unreachable one contributes nothing and is named in
// failedClusters with partial set — exactly how an unreachable peer behaves
// in the existing node fan-out. Every returned row is tagged with the cluster
// it belongs to; other clusters' rows are unaffected by one being down
// (T-1201 AC5).
//
// This is a single bounded page (up to limit rows per reachable cluster,
// merged and capped at limit) — the fixed-window merge the cluster dimension
// needs; the finer keyset-cursor continuation the node fan-out uses stays a
// per-node concern beneath it.
func (a *Aggregator) Audit(ctx context.Context, src AuditSource, filter store.AuditFilter, limit int) (rows []AuditRow, partial bool, failedClusters []string, err error) {
	if limit <= 0 {
		limit = 50
	}
	clusters, err := a.svc.List(ctx)
	if err != nil {
		return nil, false, nil, err
	}
	clusters, _ = a.splitTunnelDown(clusters)

	reach := fanOut(ctx, a, clusters, func(ctx context.Context, r PVEReader) (struct{}, error) {
		_, serr := r.ClusterStatus(ctx)
		return struct{}{}, serr
	})

	for _, res := range reach {
		if res.err != nil {
			partial = true
			failedClusters = append(failedClusters, res.cluster.ID)
			continue
		}
		cf := filter
		cf.ClusterID = res.cluster.ID
		entries, _, listErr := src.ListPage(ctx, cf, "", limit)
		if listErr != nil {
			// A local store read failing for one cluster's slice is isolated
			// the same way an unreachable cluster is, rather than failing the
			// whole merge.
			partial = true
			failedClusters = append(failedClusters, res.cluster.ID)
			a.log.Debug("federation: reading audit slice for cluster", "cluster", res.cluster.ID, "error", listErr)
			continue
		}
		for _, e := range entries {
			rows = append(rows, AuditRow{Entry: e, ClusterID: res.cluster.ID})
		}
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Entry.At != rows[j].Entry.At {
			return rows[i].Entry.At > rows[j].Entry.At
		}
		return rows[i].Entry.ID > rows[j].Entry.ID
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, partial, failedClusters, nil
}
