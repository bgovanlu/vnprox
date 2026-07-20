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
// server. The SDN reads back the cross-cluster IPAM view (T-1203): each
// attached cluster's configured SDN subnet CIDRs, the per-cluster input the
// cross-cluster duplicate-subnet check folds together.
type PVEReader interface {
	ClusterStatus(ctx context.Context) ([]pve.ClusterStatusEntry, error)
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
	svc       *Service
	newReader ReaderFactory
	log       *slog.Logger
	timeout   time.Duration
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

// NewAggregator builds an Aggregator over svc's registered clusters.
func NewAggregator(svc *Service, opts ...AggregatorOption) *Aggregator {
	a := &Aggregator{svc: svc, log: svc.log, timeout: DefaultAggregateTimeout}
	a.newReader = func(ctx context.Context, c Cluster) (PVEReader, error) {
		return svc.ClientFor(ctx, c.ID)
	}
	for _, o := range opts {
		o(a)
	}
	return a
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
