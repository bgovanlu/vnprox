// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"fmt"
	"net"
	"sort"
)

// ClusterSubnets is one attached cluster's subnet CIDR set, tagged with the
// cluster it came from — the per-cluster input the cross-cluster IPAM view
// (GET /federation/ipam/conflicts) folds together. The CIDRs are whatever
// GET /ipam/subnets reported for that cluster (SDN + bridge-derived), fanned
// out through internal/federation.Aggregator.
type ClusterSubnets struct {
	ClusterID   string
	ClusterName string
	CIDRs       []string
}

// crossClusterDocsSuggestion is the standing remediation for a duplicate
// subnet spanning two clusters — there is no automatic fix (renumbering is an
// operator judgment call), so the conflict points at the design note.
const crossClusterDocsSuggestion = "the same IP space is configured in two clusters; renumber one, or confirm the overlap is intentional (e.g. isolated L2 domains)"

// CrossClusterConflicts folds N attached clusters' subnet lists into the
// cross-cluster duplicate/overlap findings (T-1203 AC2): the same or an
// overlapping CIDR allocated in two different clusters surfaces as one
// Conflict of type cross_cluster_duplicate_subnet, naming both clusters. No
// overlap anywhere → empty. A CIDR overlapping another within the *same*
// cluster is never a cross-cluster conflict and is ignored here (that is a
// single-cluster concern the per-cluster merge already owns).
//
// Output is deterministic: conflicts are keyed by their sorted cluster-name
// pair plus sorted CIDR pair and de-duplicated, then sorted, so the same
// input always yields byte-identical findings (the unified stream's stable-id
// requirement).
func CrossClusterConflicts(clusters []ClusterSubnets) []Conflict {
	// Pre-parse every cluster's CIDRs once (skipping malformed entries), so
	// the O(pairs) comparison below doesn't re-parse.
	type parsed struct {
		net  *net.IPNet
		cidr string
	}
	nets := make([][]parsed, len(clusters))
	for i, c := range clusters {
		for _, raw := range c.CIDRs {
			_, ipnet, err := net.ParseCIDR(raw)
			if err != nil || ipnet == nil {
				continue
			}
			nets[i] = append(nets[i], parsed{cidr: ipnet.String(), net: ipnet})
		}
	}

	seen := map[string]bool{}
	var out []Conflict
	for i := 0; i < len(clusters); i++ {
		for j := i + 1; j < len(clusters); j++ {
			for _, a := range nets[i] {
				for _, b := range nets[j] {
					if !netsOverlap(a.net, b.net) {
						continue
					}
					pairClusters := sortedUnique([]string{clusters[i].ClusterName, clusters[j].ClusterName})
					pairCIDRs := sortedUnique([]string{a.cidr, b.cidr})
					key := fmt.Sprintf("%v|%v", pairClusters, pairCIDRs)
					if seen[key] {
						continue
					}
					seen[key] = true
					out = append(out, Conflict{
						Type:     ConflictCrossClusterDuplicateSubnet,
						Severity: SeverityWarning,
						Message: fmt.Sprintf("subnet %s overlaps %s across clusters %s and %s",
							pairCIDRs[0], pairCIDRs[len(pairCIDRs)-1], pairClusters[0], pairClusters[len(pairClusters)-1]),
						Suggestion: crossClusterDocsSuggestion,
						IPs:        pairCIDRs,
						Clusters:   pairClusters,
					})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !equalStrs(out[i].Clusters, out[j].Clusters) {
			return lessStrs(out[i].Clusters, out[j].Clusters)
		}
		return lessStrs(out[i].IPs, out[j].IPs)
	})
	return out
}

// netsOverlap reports whether two IP networks share any address (either
// contains the other's base address). Two networks of different address
// families never overlap (Contains already returns false for a mismatched
// family, but the base-IP check makes the intent explicit).
func netsOverlap(a, b *net.IPNet) bool {
	return a.Contains(b.IP) || b.Contains(a.IP)
}

// SeverityWarning is re-exported from the findings vocabulary for this
// package's own conflict construction (the merge engine uses the same string
// literals). Declared here rather than imported to keep internal/ipam free of
// an internal/findings dependency (adapt_ipam.go's decoupling).
const SeverityWarning = "warning"

func equalStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func lessStrs(a, b []string) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// sortedUnique returns a sorted, de-duplicated copy of ss (empty strings
// dropped) — a local helper mirroring internal/findings' own.
func sortedUnique(ss []string) []string {
	seen := make(map[string]bool, len(ss))
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
