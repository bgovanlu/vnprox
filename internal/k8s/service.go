// service.go implements Poller: the per-cluster cache GET
// /k8s/{clusterId}/overlay's live poll updates and internal/findings'
// K8sProvider seam reads from. (Named Poller, not Service, to avoid
// colliding with types.go's k8s API Service wire type.)
//
// Documented scope boundary: this package does not implement its own
// background polling scheduler (the periodic-tick machinery a T-1303-style
// scheduler would provide) — no such scheduler infrastructure exists yet
// on this codebase's dependency base (T-103/T-1002 only, per this task's
// card). Poll is instead called synchronously by GET
// /k8s/{clusterId}/overlay on every request (docs/architecture.md §7's
// "always compute fresh, never trust a cache as authoritative" invariant
// for that route), and Poller simply remembers the most recent result per
// cluster so the unified findings stream (adapt_k8s.go's K8sProvider) has
// something to report between overlay views rather than nothing. A
// dedicated background scheduler is a documented, named follow-up, not
// silently pretended to exist.

package k8s

import (
	"context"
	"sort"
	"sync"
	"time"
)

// PollResult is one cluster's most recent poll outcome.
type PollResult struct {
	Err      string
	Findings []NodePortFinding
	Overlay  Overlay
	At       int64
}

// Poller caches the most recent PollResult per cluster ID.
type Poller struct {
	cache map[string]PollResult
	mu    sync.RWMutex
}

// NewPoller builds an empty Poller.
func NewPoller() *Poller {
	return &Poller{cache: map[string]PollResult{}}
}

// Poll performs one live, synchronous poll of clusterID via client
// (Nodes/Pods/Services/KubeSystemDaemonSets, in that order — a failure on
// any one call aborts the poll and caches the error rather than returning
// a partial overlay), builds the Overlay and NodePort-exposure findings,
// caches the result, and returns it. index/lookup are nil-safe (see
// CorrelateNodes/CheckNodePortExposure); now defaults to time.Now.
func (s *Poller) Poll(ctx context.Context, clusterID string, client *Client, index GuestIPIndex, lookup FwLookup, now func() time.Time) (Overlay, []NodePortFinding, error) {
	if now == nil {
		now = time.Now
	}

	nodes, err := client.Nodes(ctx)
	if err != nil {
		return s.setErr(clusterID, err, now)
	}
	pods, err := client.Pods(ctx)
	if err != nil {
		return s.setErr(clusterID, err, now)
	}
	services, err := client.Services(ctx)
	if err != nil {
		return s.setErr(clusterID, err, now)
	}
	daemonsets, err := client.KubeSystemDaemonSets(ctx)
	if err != nil {
		return s.setErr(clusterID, err, now)
	}

	overlay := BuildOverlay(clusterID, nodes, pods, services, daemonsets, index)
	overlay.GeneratedAt = now().Unix()
	findings := CheckNodePortExposure(clusterID, services, overlay.Nodes, lookup)

	s.mu.Lock()
	s.cache[clusterID] = PollResult{Overlay: overlay, Findings: findings, At: overlay.GeneratedAt}
	s.mu.Unlock()

	return overlay, findings, nil
}

func (s *Poller) setErr(clusterID string, err error, now func() time.Time) (Overlay, []NodePortFinding, error) {
	s.mu.Lock()
	s.cache[clusterID] = PollResult{Err: err.Error(), At: now().Unix()}
	s.mu.Unlock()
	return Overlay{}, nil, err
}

// Last returns clusterID's most recently cached poll result, if any.
func (s *Poller) Last(clusterID string) (PollResult, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.cache[clusterID]
	return r, ok
}

// Forget drops clusterID's cached result (called when a cluster is
// deregistered, so a deleted cluster's stale findings don't linger in the
// unified findings stream).
func (s *Poller) Forget(clusterID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cache, clusterID)
}

// CachedFindings returns every currently-cached NodePortFinding across
// every cluster, in a deterministic order (sorted by clusterId, namespace,
// service, nodePort) — the data internal/findings' K8sProvider seam
// reports, per this file's own doc comment on why this is "most recent
// observed", not "live right now".
func (s *Poller) CachedFindings() []NodePortFinding {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []NodePortFinding
	for _, r := range s.cache {
		out = append(out, r.Findings...)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.ClusterID != b.ClusterID {
			return a.ClusterID < b.ClusterID
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		return a.NodePort < b.NodePort
	})
	return out
}
