package federation

import (
	"context"
	"testing"

	"github.com/bgovanlu/vnprox/internal/pvemock"
	"github.com/bgovanlu/vnprox/internal/store"
)

// fakeTunnelHealth is a scripted TunnelHealth test double: tunnelID ->
// down/up, defaulting to "not down" for any id not explicitly listed.
type fakeTunnelHealth map[string]bool

func (f fakeTunnelHealth) TunnelDown(tunnelID string) bool { return f[tunnelID] }

// linkTunnel sets clusterID's WgTunnelID via the ordinary Update path (the
// ProdSetup route a real "connect two clusters" flow would use).
func linkTunnel(t *testing.T, svc *Service, clusterID, tunnelID string) {
	t.Helper()
	if _, err := svc.Update(context.Background(), clusterID, "", "", nil, &tunnelID); err != nil {
		t.Fatalf("linking tunnel %s to cluster %s: %v", tunnelID, clusterID, err)
	}
}

// TestAggregator_TunnelDown_ClusterNodesAll is T-1407 AC2: a cluster linked
// to a down tunnel is excluded from ClusterNodesAll entirely, but — unlike
// plain PVE-API unreachability — does NOT appear in partial/failedClusters;
// the sibling cluster is unaffected.
func TestAggregator_TunnelDown_ClusterNodesAll(t *testing.T) {
	svc, _, _ := newTestService(t)
	_, ids := attachGroup(t, svc, []pvemock.MockClusterSpec{
		{Name: "single", FixturePath: fixtureSingleNode},
		{Name: "vlan", FixturePath: fixtureThreeNode},
	})
	linkTunnel(t, svc, ids["vlan"], "tun-1")
	agg := NewAggregator(svc, WithTunnelHealth(fakeTunnelHealth{"tun-1": true}))

	results, partial, failed, err := agg.ClusterNodesAll(context.Background())
	if err != nil {
		t.Fatalf("ClusterNodesAll: %v", err)
	}
	if partial {
		t.Error("partial = true, want false — a tunnel-down cluster must not raise the ordinary partial flag")
	}
	if len(failed) != 0 {
		t.Errorf("failedClusters = %v, want empty — the tunnel-down cluster is a suppressed exclusion, not a failure entry", failed)
	}
	if len(results) != 1 || results[0].ClusterID != ids["single"] {
		t.Fatalf("results = %+v, want just the single cluster", results)
	}
}

// TestAggregator_TunnelHealthy_AggregatesNormally is T-1407 AC1: a cluster
// linked to a healthy tunnel aggregates exactly like an unlinked one — no
// exclusion, no synthetic partial/failedClusters entry.
func TestAggregator_TunnelHealthy_AggregatesNormally(t *testing.T) {
	svc, _, _ := newTestService(t)
	_, ids := attachGroup(t, svc, []pvemock.MockClusterSpec{
		{Name: "single", FixturePath: fixtureSingleNode},
		{Name: "vlan", FixturePath: fixtureThreeNode},
	})
	linkTunnel(t, svc, ids["vlan"], "tun-1")
	agg := NewAggregator(svc, WithTunnelHealth(fakeTunnelHealth{"tun-1": false}))

	results, partial, failed, err := agg.ClusterNodesAll(context.Background())
	if err != nil {
		t.Fatalf("ClusterNodesAll: %v", err)
	}
	if partial || len(failed) != 0 {
		t.Fatalf("healthy tunnel but partial=%v failed=%v", partial, failed)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2 (both clusters aggregate normally)", len(results))
	}
}

// TestAggregator_NotTunnelLinked_OrdinaryHandlingUnchanged is T-1407 AC3: a
// federation peer not linked to any tunnel gets T-1201's ordinary
// unreachable-peer handling unchanged, even with a TunnelHealth wired in —
// killing it still surfaces the normal partial/failedClusters signal, not a
// silent exclusion.
func TestAggregator_NotTunnelLinked_OrdinaryHandlingUnchanged(t *testing.T) {
	svc, _, _ := newTestService(t)
	group, ids := attachGroup(t, svc, []pvemock.MockClusterSpec{
		{Name: "single", FixturePath: fixtureSingleNode},
		{Name: "vlan", FixturePath: fixtureThreeNode},
	})
	// TunnelHealth is wired in, but "vlan" never links a tunnel — it must
	// never be consulted for it (fakeTunnelHealth would panic-free default
	// false anyway, but this proves the *codepath*, not just the value, is
	// unaffected: killing it still produces the ordinary failedClusters
	// entry, not a silent tunnel-style exclusion).
	agg := NewAggregator(svc, WithTunnelHealth(fakeTunnelHealth{}))

	group.ByName("vlan").Close()
	results, partial, failed, err := agg.ClusterNodesAll(context.Background())
	if err != nil {
		t.Fatalf("ClusterNodesAll: %v", err)
	}
	if !partial {
		t.Error("partial = false, want true — an unlinked cluster's real outage must still surface normally")
	}
	if len(failed) != 1 || failed[0] != ids["vlan"] {
		t.Fatalf("failedClusters = %v, want [%s]", failed, ids["vlan"])
	}
	if len(results) != 1 || results[0].ClusterID != ids["single"] {
		t.Fatalf("results = %+v, want just the single cluster", results)
	}
}

// TestAggregator_TunnelDown_IPAMAndAudit proves the suppression applies to
// the other two named surfaces (T-1407's Objective: "topology, audit, and
// IPAM-conflict reads simultaneously"), not just ClusterNodesAll.
func TestAggregator_TunnelDown_IPAMAndAudit(t *testing.T) {
	svc, _, db := newTestService(t)
	_, ids := attachGroup(t, svc, []pvemock.MockClusterSpec{
		{Name: "east", FixturePath: fixtureIpamLab},
		{Name: "west", FixturePath: fixtureIpamLab},
	})
	linkTunnel(t, svc, ids["west"], "tun-2")
	agg := NewAggregator(svc, WithTunnelHealth(fakeTunnelHealth{"tun-2": true}))

	ipamResults, partial, failed, err := agg.IPAMSubnets(context.Background())
	if err != nil {
		t.Fatalf("IPAMSubnets: %v", err)
	}
	if partial || len(failed) != 0 {
		t.Fatalf("IPAMSubnets: partial=%v failed=%v, want both empty (suppressed, not failed)", partial, failed)
	}
	if len(ipamResults) != 1 || ipamResults[0].ClusterID != ids["east"] {
		t.Fatalf("IPAMSubnets results = %+v, want just the east cluster", ipamResults)
	}

	audit := store.NewAuditRepo(db)
	ctx := context.Background()
	if _, appendErr := audit.Append(ctx, store.AuditEntry{At: 100, Username: "admin@pam", Action: "changeset.apply", Result: "success", ClusterID: ids["east"]}); appendErr != nil {
		t.Fatalf("audit.Append: %v", appendErr)
	}
	if _, appendErr := audit.Append(ctx, store.AuditEntry{At: 200, Username: "admin@pam", Action: "changeset.apply", Result: "success", ClusterID: ids["west"]}); appendErr != nil {
		t.Fatalf("audit.Append: %v", appendErr)
	}

	rows, partial, failed, err := agg.Audit(ctx, audit, store.AuditFilter{}, 50)
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if partial || len(failed) != 0 {
		t.Fatalf("Audit: partial=%v failed=%v, want both empty (suppressed, not failed)", partial, failed)
	}
	if len(rows) != 1 || rows[0].ClusterID != ids["east"] {
		t.Fatalf("Audit rows = %+v, want just east's row", rows)
	}
}
