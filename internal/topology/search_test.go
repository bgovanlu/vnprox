// SPDX-License-Identifier: Apache-2.0

package topology_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/topology"
)

// scorePrefixForTest mirrors search.go's unexported scorePrefix constant
// (100/80/50/20 for exact/prefix/substring/fuzzy) so this external test
// package can assert on ranking tiers without reaching into internals.
const scorePrefixForTest = 80

// TestSearch is T-106 acceptance criterion 3: partial name, MAC, IP, and
// VMID queries each find the right entities across fixtures, ranked
// (exact/prefix before fuzzy) and capped.
func TestSearch(t *testing.T) {
	graph, _, _ := buildGraph(t, fixtureSingleNode)
	snap := graph.Snapshot()

	t.Run("partial name", func(t *testing.T) {
		results := topology.Search(snap, "web0")
		mustContainRef(t, results, "guest:pve1:100")
		// Both the guest itself and its NIC (which also indexes the
		// guest's name for search) match "web0" as a prefix; either may
		// sort first (ties break on Ref), but neither should rank below
		// the exact/prefix tier.
		if results[0].Score != scorePrefixForTest {
			t.Errorf("top hit score = %d, want %d (prefix match)", results[0].Score, scorePrefixForTest)
		}
	})

	t.Run("mac", func(t *testing.T) {
		results := topology.Search(snap, "BC:24:11:AA:00:64")
		mustContainRef(t, results, "guest-nic:pve1:100/net0")
		if results[0].Score != 100 {
			t.Errorf("exact MAC match score = %d, want 100 (exact)", results[0].Score)
		}
	})

	t.Run("ip", func(t *testing.T) {
		results := topology.Search(snap, "192.168.1.254")
		mustContainRef(t, results, "lldp-neighbor:pve1:eno1/ac:1f:6b:00:11:22/Gi1/0/1")
	})

	t.Run("vmid", func(t *testing.T) {
		results := topology.Search(snap, "101")
		mustContainRef(t, results, "guest:pve1:101")
	})

	t.Run("empty query yields no results", func(t *testing.T) {
		if got := topology.Search(snap, ""); len(got) != 0 {
			t.Errorf("Search(\"\") = %v, want empty", got)
		}
	})

	t.Run("no match yields no results", func(t *testing.T) {
		if got := topology.Search(snap, "zzz-does-not-exist-zzz"); len(got) != 0 {
			t.Errorf("Search(no-match) = %v, want empty", got)
		}
	})
}

// TestSearch_RankingAndCap builds a graph with more than DefaultSearchLimit
// name-matching entities to prove the exact/prefix-before-fuzzy ordering
// and the cap both hold.
func TestSearch_RankingAndCap(t *testing.T) {
	graph, _, _ := buildGraph(t, fixtureThreeNodeVlan)
	snap := graph.Snapshot()

	// "vmbr0" is an exact match for the bridge name on every node, and a
	// prefix match for "vmbr0.20" (the VLAN sub-interface) on every node.
	results := topology.Search(snap, "vmbr0")
	if len(results) == 0 {
		t.Fatal("expected at least one match for vmbr0")
	}
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Fatalf("results not sorted by descending score at index %d: %+v", i, results)
		}
	}
	// Exact "vmbr0" bridge hits must outrank the "vmbr0.20" prefix hits.
	var sawExactBeforePrefix bool
	firstPrefixIdx := -1
	for i, r := range results {
		if r.Kind == "vlan" && firstPrefixIdx == -1 {
			firstPrefixIdx = i
		}
	}
	for i, r := range results {
		if r.Kind == "bridge" && (firstPrefixIdx == -1 || i < firstPrefixIdx) {
			sawExactBeforePrefix = true
		}
	}
	if !sawExactBeforePrefix {
		t.Errorf("expected exact bridge-name matches to rank before vlan-name prefix matches; got %+v", results)
	}

	if len(results) > topology.DefaultSearchLimit {
		t.Errorf("results len = %d, want <= %d (DefaultSearchLimit)", len(results), topology.DefaultSearchLimit)
	}
}

func mustContainRef(t *testing.T, results []topology.SearchResult, ref string) {
	t.Helper()
	for _, r := range results {
		if r.Ref == ref {
			return
		}
	}
	t.Errorf("expected search results to contain %q; got %+v", ref, results)
}
