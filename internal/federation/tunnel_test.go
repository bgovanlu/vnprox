// SPDX-License-Identifier: Apache-2.0

package federation

import (
	"context"
	"errors"
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

// fakeLinker is a scripted TunnelLinker: clusterID -> derived tunnel id, with
// an optional error for the degradation case.
type fakeLinker struct {
	err   error
	links map[string]string
}

func (f fakeLinker) TunnelIDForCluster(_ context.Context, clusterID string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.links[clusterID], nil
}

// newLinkedService is newTestService with a TunnelLinker wired in — the
// production shape (cmd/vnproxd passes *store.WireGuardRepo).
func newLinkedService(t *testing.T, linker TunnelLinker) *Service {
	t.Helper()
	svc, _, _ := newTestService(t)
	svc.linker = linker
	return svc
}

// TestService_PeerDerivedLinkage: with no explicit clusters.wg_tunnel_id, a
// cluster tagged on a WireGuard peer comes back linked, sourced "peer" — this
// is what keeps wireguard_peers.cluster_id and clusters.wg_tunnel_id from
// drifting, since every downstream consumer (Aggregator.splitTunnelDown,
// internal/findings' tunnel_down_peer_unreachable producer) reads the one
// effective Cluster.WgTunnelID.
func TestService_PeerDerivedLinkage(t *testing.T) {
	ctx := context.Background()
	svc := newLinkedService(t, fakeLinker{links: map[string]string{}})
	linked, err := svc.Add(ctx, "east", "https://east:8006", Credential{Kind: CredentialToken, Token: "t"}, "admin@pam")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	untagged, err := svc.Add(ctx, "west", "https://west:8006", Credential{Kind: CredentialToken, Token: "t"}, "admin@pam")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	svc.linker = fakeLinker{links: map[string]string{linked.ID: "tun-peer"}}

	got, err := svc.Get(ctx, linked.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.WgTunnelID != "tun-peer" || got.WgTunnelSource != TunnelLinkPeer {
		t.Errorf("Get() linkage = (%q, %q), want (tun-peer, %s)", got.WgTunnelID, got.WgTunnelSource, TunnelLinkPeer)
	}
	if got, err = svc.Get(ctx, untagged.ID); err != nil || got.WgTunnelID != "" || got.WgTunnelSource != "" {
		t.Errorf("untagged cluster linkage = (%q, %q), %v; want unlinked", got.WgTunnelID, got.WgTunnelSource, err)
	}

	list, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, c := range list {
		want := ""
		if c.ID == linked.ID {
			want = "tun-peer"
		}
		if c.WgTunnelID != want {
			t.Errorf("List() cluster %s linkage = %q, want %q", c.Name, c.WgTunnelID, want)
		}
	}
}

// TestService_ExplicitLinkageWins: an explicit clusters.wg_tunnel_id override
// beats the peer-derived link and is reported as such; clearing the override
// falls back to the derived one rather than unlinking (the Update doc
// comment's contract — undoing a peer-derived link means retiring the peer
// through an ordinary wg.peer.* changeset, not editing the cluster).
func TestService_ExplicitLinkageWins(t *testing.T) {
	ctx := context.Background()
	svc := newLinkedService(t, fakeLinker{})
	c, err := svc.Add(ctx, "east", "https://east:8006", Credential{Kind: CredentialToken, Token: "t"}, "admin@pam")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	svc.linker = fakeLinker{links: map[string]string{c.ID: "tun-peer"}}

	updated, err := svc.Update(ctx, c.ID, "", "", nil, ptr("tun-explicit"))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.WgTunnelID != "tun-explicit" || updated.WgTunnelSource != TunnelLinkExplicit {
		t.Errorf("Update() linkage = (%q, %q), want (tun-explicit, %s)", updated.WgTunnelID, updated.WgTunnelSource, TunnelLinkExplicit)
	}
	got, err := svc.Get(ctx, c.ID)
	if err != nil || got.WgTunnelID != "tun-explicit" || got.WgTunnelSource != TunnelLinkExplicit {
		t.Errorf("Get() after explicit link = (%q, %q), %v", got.WgTunnelID, got.WgTunnelSource, err)
	}

	cleared, err := svc.Update(ctx, c.ID, "", "", nil, ptr(""))
	if err != nil {
		t.Fatalf("Update clearing: %v", err)
	}
	if cleared.WgTunnelID != "tun-peer" || cleared.WgTunnelSource != TunnelLinkPeer {
		t.Errorf("after clearing the override, linkage = (%q, %q), want the peer-derived (tun-peer, %s)", cleared.WgTunnelID, cleared.WgTunnelSource, TunnelLinkPeer)
	}
}

// TestService_LinkerErrorDegradesToUnlinked: a linker failure must never fail
// a cluster read — it degrades to "not tunnel-linked", the same fail-open
// direction every other T-1407 path takes (a resolution problem must not hide
// a cluster's data or break the registry).
func TestService_LinkerErrorDegradesToUnlinked(t *testing.T) {
	ctx := context.Background()
	svc := newLinkedService(t, fakeLinker{err: errLinker})
	c, err := svc.Add(ctx, "east", "https://east:8006", Credential{Kind: CredentialToken, Token: "t"}, "admin@pam")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, err := svc.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get with a failing linker must not error: %v", err)
	}
	if got.WgTunnelID != "" || got.WgTunnelSource != "" {
		t.Errorf("Get() linkage = (%q, %q), want unlinked", got.WgTunnelID, got.WgTunnelSource)
	}
	if list, listErr := svc.List(ctx); listErr != nil || len(list) != 1 || list[0].WgTunnelID != "" {
		t.Errorf("List() = %+v, %v; want one unlinked cluster", list, listErr)
	}
}

// TestService_NilLinkerIsShippedBehaviour: without a TunnelLinker the service
// consults only the explicit column — T-1407's v3.0.3 behaviour, unchanged.
func TestService_NilLinkerIsShippedBehaviour(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newTestService(t)
	c, err := svc.Add(ctx, "east", "https://east:8006", Credential{Kind: CredentialToken, Token: "t"}, "admin@pam")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, err := svc.Get(ctx, c.ID)
	if err != nil || got.WgTunnelID != "" || got.WgTunnelSource != "" {
		t.Errorf("Get() with nil linker = (%q, %q), %v; want unlinked", got.WgTunnelID, got.WgTunnelSource, err)
	}
	linkTunnel(t, svc, c.ID, "tun-explicit")
	if got, err = svc.Get(ctx, c.ID); err != nil || got.WgTunnelID != "tun-explicit" || got.WgTunnelSource != TunnelLinkExplicit {
		t.Errorf("Get() after explicit link = (%q, %q), %v", got.WgTunnelID, got.WgTunnelSource, err)
	}
}

func ptr[T any](v T) *T { return &v }

var errLinker = errors.New("linker unavailable")
