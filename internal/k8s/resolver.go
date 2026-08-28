// SPDX-License-Identifier: Apache-2.0

// resolver.go implements K8sResolver: resolves a flow's srcIp/dstIp
// against a cluster's live overlay to a k8s ref string, mirroring
// internal/flow.GraphResolver's exact shape (Refresh/Resolve) — "so T-1502
// can compose it identically" (T-1501's own card wording) and a future
// T-1504-style classifier can register it as one more resolution stage
// alongside GraphResolver.
//
// Match precedence, most to least precise (see doc.go's "Pod/service
// network model" section for the full honesty-contract rationale):
//  1. Exact Service ClusterIP match -> that service's ref.
//  2. Exact Pod IP match -> that pod's ref.
//  3. Pod-CIDR containment -> the owning node's pod-subnet ref (names the
//     subnet, never invents a specific pod/service identity).
//
// An address matching none of the above resolves to "", false — never
// guessed.

package k8s

import (
	"net"
	"strings"
	"sync"
)

// ServiceRef formats a service's resolver ref: "k8s-svc:<clusterID>/
// <namespace>/<name>" — deliberately not an inventory.Ref triplet (k8s
// services are not members of inventory's closed Kind set, see doc.go);
// callers needing to render this should treat it as an opaque, stable,
// human-legible string.
func ServiceRef(clusterID, namespace, name string) string {
	return "k8s-svc:" + clusterID + "/" + namespace + "/" + name
}

// PodRef formats a pod's resolver ref, same shape as ServiceRef.
func PodRef(clusterID, namespace, name string) string {
	return "k8s-pod:" + clusterID + "/" + namespace + "/" + name
}

// PodSubnetRef formats a node's pod-subnet resolver ref (the coarse,
// CIDR-containment fallback match) — distinguishable from PodRef's exact-
// pod match by its own "k8s-podnet:" prefix, so a consumer can tell "this
// is a specific pod" from "this is just the right subnet" apart without
// parsing further.
func PodSubnetRef(clusterID, node string) string {
	return "k8s-podnet:" + clusterID + "/" + node
}

type podSubnetEntry struct {
	network *net.IPNet
	ref     string
}

// K8sResolver is the per-address-space index Refresh builds from an
// Overlay and Resolve queries — safe for concurrent use (RWMutex-guarded
// swap, identical concurrency shape to flow.GraphResolver).
type K8sResolver struct {
	exact      map[string]string
	podSubnets []podSubnetEntry
	mu         sync.RWMutex
}

// NewK8sResolver builds an empty K8sResolver; call Refresh at least once
// before Resolve returns anything.
func NewK8sResolver() *K8sResolver {
	return &K8sResolver{}
}

// Refresh re-indexes the resolver from one cluster's current Overlay.
// Refresh is additive across different clusterIDs (calling it once per
// registered cluster builds a resolver spanning every cluster) but
// replaces any previous index for the *same* clusterID — callers refresh
// each cluster's own slice on its own poll cadence and this method only
// ever touches that one cluster's entries.
func (r *K8sResolver) Refresh(overlay Overlay) {
	exact := map[string]string{}
	var podSubnets []podSubnetEntry

	for _, s := range overlay.Services {
		if s.ClusterIP == "" || strings.EqualFold(s.ClusterIP, "None") {
			continue // headless service — no single address to index
		}
		exact[s.ClusterIP] = ServiceRef(overlay.ClusterID, s.Namespace, s.Name)
	}
	for _, p := range overlay.Pods {
		if p.PodIP == "" {
			continue
		}
		exact[p.PodIP] = PodRef(overlay.ClusterID, p.Namespace, p.Name)
	}
	for _, pc := range overlay.PodCIDRs {
		_, ipnet, err := net.ParseCIDR(pc.CIDR)
		if err != nil {
			continue
		}
		podSubnets = append(podSubnets, podSubnetEntry{network: ipnet, ref: PodSubnetRef(overlay.ClusterID, pc.Node)})
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.exact == nil {
		r.exact = map[string]string{}
	}
	// Drop this cluster's previous entries (by ref prefix) before merging
	// in the fresh set, so a service/pod/node removed since the last
	// Refresh doesn't linger.
	prefix := "k8s-svc:" + overlay.ClusterID + "/"
	podPrefix := "k8s-pod:" + overlay.ClusterID + "/"
	for ip, ref := range r.exact {
		if strings.HasPrefix(ref, prefix) || strings.HasPrefix(ref, podPrefix) {
			delete(r.exact, ip)
		}
	}
	for ip, ref := range exact {
		r.exact[ip] = ref
	}

	subnetPrefix := overlay.ClusterID + "/"
	filtered := make([]podSubnetEntry, 0, len(r.podSubnets))
	for _, e := range r.podSubnets {
		if strings.HasPrefix(e.ref, "k8s-podnet:"+subnetPrefix) {
			continue
		}
		filtered = append(filtered, e)
	}
	r.podSubnets = append(filtered, podSubnets...)
}

// Resolve implements the same (string, bool) shape as
// flow.Resolver.Resolve.
func (r *K8sResolver) Resolve(ip string) (string, bool) {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return "", false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	if ref, ok := r.exact[parsed.String()]; ok {
		return ref, true
	}
	if ref, ok := r.exact[ip]; ok { // exact string form as reported (covers non-canonical IPv4-in-IPv6 etc.)
		return ref, true
	}
	for _, e := range r.podSubnets {
		if e.network.Contains(parsed) {
			return e.ref, true
		}
	}
	return "", false
}
