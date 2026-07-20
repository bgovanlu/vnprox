package federation

import (
	"context"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/pvemock"
)

// TestAggregator_TopologySummary is T-1202 AC1's backend half: a per-cluster
// summary across N fixture-backed clusters, each tagged with the correct
// clusterId and reachable, guest/node counts populated.
func TestAggregator_TopologySummary(t *testing.T) {
	svc, _, _ := newTestService(t)
	_, ids := attachGroup(t, svc, []pvemock.MockClusterSpec{
		{Name: "single", FixturePath: fixtureSingleNode},
		{Name: "vlan", FixturePath: fixtureThreeNode},
		{Name: "evpn", FixturePath: fixtureEvpn},
	})
	agg := NewAggregator(svc)

	summaries, partial, failed, err := agg.TopologySummary(context.Background())
	if err != nil {
		t.Fatalf("TopologySummary: %v", err)
	}
	if partial || len(failed) != 0 {
		t.Fatalf("all reachable but partial=%v failed=%v", partial, failed)
	}
	if len(summaries) != 3 {
		t.Fatalf("got %d summaries, want 3", len(summaries))
	}
	byID := map[string]ClusterSummary{}
	for _, s := range summaries {
		byID[s.ClusterID] = s
	}
	for name, id := range ids {
		s, ok := byID[id]
		if !ok {
			t.Fatalf("no summary for cluster %q", name)
		}
		if s.ClusterName != name {
			t.Errorf("summary %s tagged clusterName %q, want %q", id, s.ClusterName, name)
		}
		if !s.Reachable {
			t.Errorf("cluster %q summarized unreachable, want reachable", name)
		}
		if s.Nodes == 0 {
			t.Errorf("cluster %q summarized zero nodes", name)
		}
	}
	if got := byID[ids["single"]].Nodes; got != 1 {
		t.Errorf("single-node cluster reported %d nodes, want 1", got)
	}
	if got := byID[ids["vlan"]].Nodes; got < 2 {
		t.Errorf("three-node-vlan cluster reported %d nodes, want >= 2", got)
	}
}

// TestAggregator_TopologySummary_FailureIsolation is T-1202 AC1: one
// unreachable cluster still appears in the summary tagged Reachable:false
// (so its capsule renders greyed), others intact, with partial/failedClusters.
func TestAggregator_TopologySummary_FailureIsolation(t *testing.T) {
	svc, _, _ := newTestService(t)
	group, ids := attachGroup(t, svc, []pvemock.MockClusterSpec{
		{Name: "single", FixturePath: fixtureSingleNode},
		{Name: "vlan", FixturePath: fixtureThreeNode},
	})
	agg := NewAggregator(svc)
	group.ByName("vlan").Close()

	summaries, partial, failed, err := agg.TopologySummary(context.Background())
	if err != nil {
		t.Fatalf("TopologySummary: %v", err)
	}
	if !partial {
		t.Error("partial = false, want true (one cluster unreachable)")
	}
	if len(failed) != 1 || failed[0] != ids["vlan"] {
		t.Fatalf("failedClusters = %v, want [%s]", failed, ids["vlan"])
	}
	// Both clusters still produce a summary — the failed one greyed.
	if len(summaries) != 2 {
		t.Fatalf("got %d summaries, want 2 (failed cluster still greyed, not dropped)", len(summaries))
	}
	byID := map[string]ClusterSummary{}
	for _, s := range summaries {
		byID[s.ClusterID] = s
	}
	if byID[ids["vlan"]].Reachable {
		t.Error("killed cluster summarized reachable, want Reachable:false")
	}
	if !byID[ids["single"]].Reachable || byID[ids["single"]].Nodes == 0 {
		t.Error("surviving cluster lost its data when a sibling failed")
	}
}

// TestAggregator_Search is T-1202 AC3's backend half: a query fans out to
// >=2 clusters and returns cluster-namespaced hits.
func TestAggregator_Search(t *testing.T) {
	svc, _, _ := newTestService(t)
	_, ids := attachGroup(t, svc, []pvemock.MockClusterSpec{
		{Name: "single", FixturePath: fixtureSingleNode},
		{Name: "vlan", FixturePath: fixtureThreeNode},
	})
	agg := NewAggregator(svc)

	// "pve" matches every fixture's node names (pve1/pve2/pve3) across both
	// clusters, so hits must be namespaced to disambiguate.
	hits, partial, failed, err := agg.Search(context.Background(), "pve", 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if partial || len(failed) != 0 {
		t.Fatalf("both reachable but partial=%v failed=%v", partial, failed)
	}
	if len(hits) == 0 {
		t.Fatal("no hits for 'pve', want node hits from both clusters")
	}
	clustersSeen := map[string]bool{}
	for _, h := range hits {
		if h.ClusterID == "" || h.ClusterName == "" {
			t.Errorf("hit %+v missing cluster namespacing", h)
		}
		if h.ClusterID != ids["single"] && h.ClusterID != ids["vlan"] {
			t.Errorf("hit tagged unknown cluster %q", h.ClusterID)
		}
		clustersSeen[h.ClusterID] = true
	}
	if len(clustersSeen) != 2 {
		t.Errorf("hits came from %d clusters, want both (namespaced)", len(clustersSeen))
	}

	// A query matching nothing returns no hits, not an error.
	none, _, _, err := agg.Search(context.Background(), "zzz-no-such-entity", 0)
	if err != nil {
		t.Fatalf("Search(no match): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("got %d hits for a non-matching query, want 0", len(none))
	}

	// A blank query short-circuits with no fan-out.
	blank, _, _, err := agg.Search(context.Background(), "  ", 0)
	if err != nil || len(blank) != 0 {
		t.Errorf("blank Search = (%d hits, %v), want (0, nil)", len(blank), err)
	}
}

// TestAggregator_Search_FailureIsolation: one dead cluster omits its own
// hits and is named in failedClusters; the survivor's hits are unaffected.
func TestAggregator_Search_FailureIsolation(t *testing.T) {
	svc, _, _ := newTestService(t)
	group, ids := attachGroup(t, svc, []pvemock.MockClusterSpec{
		{Name: "single", FixturePath: fixtureSingleNode},
		{Name: "vlan", FixturePath: fixtureThreeNode},
	})
	agg := NewAggregator(svc)
	group.ByName("vlan").Close()

	hits, partial, failed, err := agg.Search(context.Background(), "pve", 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !partial || len(failed) != 1 || failed[0] != ids["vlan"] {
		t.Fatalf("partial=%v failed=%v, want partial with [%s]", partial, failed, ids["vlan"])
	}
	for _, h := range hits {
		if h.ClusterID == ids["vlan"] {
			t.Errorf("hit leaked from the killed cluster: %+v", h)
		}
	}
}

// TestAggregator_ClusterTopology is T-1202's lazy drill-down: one attached
// cluster's full topology projects from its PVE API alone (nodes, bridges,
// guests), matching GET /topology's contract.
func TestAggregator_ClusterTopology(t *testing.T) {
	svc, _, _ := newTestService(t)
	_, ids := attachGroup(t, svc, []pvemock.MockClusterSpec{
		{Name: "vlan", FixturePath: fixtureThreeNode},
	})
	agg := NewAggregator(svc)

	topo, err := agg.ClusterTopology(context.Background(), ids["vlan"])
	if err != nil {
		t.Fatalf("ClusterTopology: %v", err)
	}
	if len(topo.Nodes) == 0 {
		t.Fatal("projected topology has no nodes")
	}
	// Cluster member nodes surface as nodeGroups (the per-node bands), not as
	// map entities; a real projection instead proves out via the physical/L2
	// and guest layers the PVE API feeds.
	var haveBridge, haveGuest bool
	groups := map[string]bool{}
	for _, n := range topo.Nodes {
		groups[n.NodeGroup] = true
		if strings.Contains(n.Kind, "bridge") {
			haveBridge = true
		}
		if n.Kind == "guest" {
			haveGuest = true
		}
	}
	if !haveBridge {
		t.Error("projection has no bridge entity (PVE network not ingested)")
	}
	if !haveGuest {
		t.Error("projection has no guest entity (cluster resources not ingested)")
	}
	if len(groups) < 2 {
		t.Errorf("projection spans %d node-groups, want >= 2 for a three-node cluster", len(groups))
	}
}

// TestAggregator_ClusterTopology_NotFound: an unknown cluster id is a
// not-found error, not a projection.
func TestAggregator_ClusterTopology_NotFound(t *testing.T) {
	svc, _, _ := newTestService(t)
	agg := NewAggregator(svc)
	if _, err := agg.ClusterTopology(context.Background(), "no-such-id"); err == nil {
		t.Fatal("ClusterTopology(unknown) = nil error, want not-found")
	}
}
