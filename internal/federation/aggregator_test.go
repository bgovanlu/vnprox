// SPDX-License-Identifier: Apache-2.0

package federation

import (
	"context"
	"testing"

	"github.com/bgovanlu/vnprox/internal/pvemock"
	"github.com/bgovanlu/vnprox/internal/store"
)

const (
	fixtureSingleNode = "../../testdata/clusters/single-node.yaml"
	fixtureThreeNode  = "../../testdata/clusters/three-node-vlan.yaml"
	fixtureEvpn       = "../../testdata/clusters/evpn-lab.yaml"
)

// attachGroup starts a mock cluster group and registers each of its clusters
// with svc using the fixture's own root@pam/vnprox-mock ticket credential,
// returning the group and a name->clusterId map. The clusters aggregate
// through real *pve.Clients against real httptest listeners.
func attachGroup(t *testing.T, svc *Service, specs []pvemock.MockClusterSpec) (*pvemock.ClusterGroup, map[string]string) {
	t.Helper()
	group, err := pvemock.StartClusterGroup(specs)
	if err != nil {
		t.Fatalf("StartClusterGroup: %v", err)
	}
	t.Cleanup(group.Close)

	ids := map[string]string{}
	for _, mc := range group.Clusters {
		c, err := svc.Add(context.Background(), mc.Name, mc.URL,
			Credential{Kind: CredentialTicket, Username: "root@pam", Password: "vnprox-mock"}, "admin@pam")
		if err != nil {
			t.Fatalf("Add cluster %s: %v", mc.Name, err)
		}
		ids[mc.Name] = c.ID
	}
	return group, ids
}

// TestAggregator_ClusterNodesAll is T-1201 AC2: three fixture-backed clusters
// attached to one Aggregator all aggregate, each tagged with the correct
// clusterId.
func TestAggregator_ClusterNodesAll(t *testing.T) {
	svc, _, _ := newTestService(t)
	_, ids := attachGroup(t, svc, []pvemock.MockClusterSpec{
		{Name: "single", FixturePath: fixtureSingleNode},
		{Name: "vlan", FixturePath: fixtureThreeNode},
		{Name: "evpn", FixturePath: fixtureEvpn},
	})
	agg := NewAggregator(svc)

	results, partial, failed, err := agg.ClusterNodesAll(context.Background())
	if err != nil {
		t.Fatalf("ClusterNodesAll: %v", err)
	}
	if partial || len(failed) != 0 {
		t.Fatalf("all three reachable but partial=%v failed=%v", partial, failed)
	}
	if len(results) != 3 {
		t.Fatalf("got %d cluster results, want 3", len(results))
	}

	byID := map[string]ClusterNodes{}
	for _, r := range results {
		byID[r.ClusterID] = r
	}
	for name, id := range ids {
		r, ok := byID[id]
		if !ok {
			t.Fatalf("no aggregate result for cluster %q (id %s)", name, id)
		}
		if r.ClusterName != name {
			t.Errorf("result for %s tagged clusterName %q, want %q", id, r.ClusterName, name)
		}
		if len(r.Nodes) == 0 {
			t.Errorf("cluster %q aggregated zero nodes", name)
		}
	}

	// The single-node fixture has exactly one node; the three-node fixture
	// has more than one — proving the per-cluster node lists are genuinely
	// distinct, not one cluster's data mislabeled.
	if got := len(byID[ids["single"]].Nodes); got != 1 {
		t.Errorf("single-node cluster reported %d nodes, want 1", got)
	}
	if got := len(byID[ids["vlan"]].Nodes); got < 2 {
		t.Errorf("three-node-vlan cluster reported %d nodes, want >= 2", got)
	}
}

// TestAggregator_FailureIsolation is T-1201 AC3: killing one of three
// attached clusters mid-aggregation still returns the other two's full data,
// with partial:true and failedClusters naming the unreachable one.
func TestAggregator_FailureIsolation(t *testing.T) {
	svc, _, _ := newTestService(t)
	group, ids := attachGroup(t, svc, []pvemock.MockClusterSpec{
		{Name: "single", FixturePath: fixtureSingleNode},
		{Name: "vlan", FixturePath: fixtureThreeNode},
		{Name: "evpn", FixturePath: fixtureEvpn},
	})
	agg := NewAggregator(svc)

	// Kill the "vlan" cluster's listener — it is now unreachable.
	group.ByName("vlan").Close()

	results, partial, failed, err := agg.ClusterNodesAll(context.Background())
	if err != nil {
		t.Fatalf("ClusterNodesAll: %v", err)
	}
	if !partial {
		t.Error("partial = false, want true (one cluster unreachable)")
	}
	if len(failed) != 1 || failed[0] != ids["vlan"] {
		t.Fatalf("failedClusters = %v, want [%s] (the killed vlan cluster)", failed, ids["vlan"])
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2 surviving clusters", len(results))
	}
	// The two survivors still carry full data.
	byID := map[string]ClusterNodes{}
	for _, r := range results {
		byID[r.ClusterID] = r
	}
	if _, ok := byID[ids["vlan"]]; ok {
		t.Error("killed cluster still contributed a result")
	}
	if len(byID[ids["single"]].Nodes) == 0 || len(byID[ids["evpn"]].Nodes) == 0 {
		t.Error("a surviving cluster lost its node data when a sibling failed")
	}

	// The unreachable cluster's status cache is refreshed to "unreachable".
	got, err := svc.Get(context.Background(), ids["vlan"])
	if err != nil {
		t.Fatalf("Get vlan cluster: %v", err)
	}
	if got.Status != "unreachable" {
		t.Errorf("vlan cluster status = %q, want unreachable", got.Status)
	}
}

// TestAggregator_NodeClusters proves the change.ClusterMembershipSource
// implementation maps each node unique to one cluster to that cluster's id,
// and — crucially — OMITS a node name that collides across clusters, since
// PVE node names are not globally unique. Both shipped fixtures name their
// first node "pve1", so that name is ambiguous and must be dropped; the
// three-node fixture's pve2/pve3 are unique to it and map cleanly.
func TestAggregator_NodeClusters(t *testing.T) {
	svc, _, _ := newTestService(t)
	_, ids := attachGroup(t, svc, []pvemock.MockClusterSpec{
		{Name: "single", FixturePath: fixtureSingleNode},
		{Name: "vlan", FixturePath: fixtureThreeNode},
	})
	agg := NewAggregator(svc)

	m, err := agg.NodeClusters(context.Background())
	if err != nil {
		t.Fatalf("NodeClusters: %v", err)
	}
	// pve1 exists in both clusters -> ambiguous -> omitted (never guessed).
	if cid, present := m["pve1"]; present {
		t.Errorf("pve1 is ambiguous across clusters but mapped to %q; want omitted", cid)
	}
	// pve2/pve3 are unique to the three-node cluster and map to it.
	if m["pve2"] != ids["vlan"] || m["pve3"] != ids["vlan"] {
		t.Errorf("pve2/pve3 mapped to %q/%q, want both = vlan cluster %s", m["pve2"], m["pve3"], ids["vlan"])
	}
	// No node maps to an unknown cluster.
	for node, cid := range m {
		if cid != ids["single"] && cid != ids["vlan"] {
			t.Errorf("node %q mapped to unknown cluster %q", node, cid)
		}
	}
}

// TestAggregator_Audit is T-1201 AC5: GET /audit merges rows across >=2
// attached clusters, newest-first, each tagged clusterId; one cluster
// unreachable -> partial/failedClusters, the other's rows unaffected.
func TestAggregator_Audit(t *testing.T) {
	svc, _, db := newTestService(t)
	group, ids := attachGroup(t, svc, []pvemock.MockClusterSpec{
		{Name: "single", FixturePath: fixtureSingleNode},
		{Name: "vlan", FixturePath: fixtureThreeNode},
	})
	audit := store.NewAuditRepo(db)
	ctx := context.Background()

	// Seed audit rows tagged per cluster (as a federated primary's own audit
	// log would be — each row records which cluster the action targeted).
	seed := func(at int64, action, clusterID string) {
		if _, err := audit.Append(ctx, store.AuditEntry{At: at, Username: "admin@pam", Action: action, Result: "success", ClusterID: clusterID}); err != nil {
			t.Fatalf("audit.Append: %v", err)
		}
	}
	seed(100, "changeset.apply", ids["single"])
	seed(200, "changeset.apply", ids["vlan"])
	seed(300, "changeset.confirm", ids["single"])

	// Merge across both reachable clusters: newest-first, tagged clusterId.
	rows, partial, failed, err := agg(svc).Audit(ctx, audit, store.AuditFilter{}, 50)
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if partial || len(failed) != 0 {
		t.Fatalf("both reachable but partial=%v failed=%v", partial, failed)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d merged rows, want 3", len(rows))
	}
	if rows[0].Entry.At != 300 || rows[1].Entry.At != 200 || rows[2].Entry.At != 100 {
		t.Fatalf("rows not newest-first: %d,%d,%d", rows[0].Entry.At, rows[1].Entry.At, rows[2].Entry.At)
	}
	for _, r := range rows {
		if r.ClusterID != r.Entry.ClusterID {
			t.Errorf("row tagged %q but entry.ClusterID %q", r.ClusterID, r.Entry.ClusterID)
		}
	}

	// Now kill the vlan cluster: its rows drop, single's are unaffected,
	// partial/failedClusters report the outage.
	group.ByName("vlan").Close()
	rows, partial, failed, err = agg(svc).Audit(ctx, audit, store.AuditFilter{}, 50)
	if err != nil {
		t.Fatalf("Audit after kill: %v", err)
	}
	if !partial {
		t.Error("partial = false after killing a cluster, want true")
	}
	if len(failed) != 1 || failed[0] != ids["vlan"] {
		t.Fatalf("failedClusters = %v, want [%s]", failed, ids["vlan"])
	}
	// Only the single cluster's two rows survive; none carry the vlan id.
	if len(rows) != 2 {
		t.Fatalf("got %d rows after kill, want 2 (single cluster only)", len(rows))
	}
	for _, r := range rows {
		if r.ClusterID != ids["single"] {
			t.Errorf("row from a non-single cluster (%q) leaked through after vlan went down", r.ClusterID)
		}
	}
}

// agg is a tiny test helper: a fresh Aggregator over svc.
func agg(svc *Service) *Aggregator { return NewAggregator(svc) }
