// SPDX-License-Identifier: Apache-2.0

package findings

import (
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/wireguard"
)

type staticFederation struct {
	err      error
	clusters []FederatedCluster
}

func (s staticFederation) TunnelLinkedClusters() ([]FederatedCluster, error) {
	return s.clusters, s.err
}

// TestFederationTunnelFindings_HealthyNoFinding is T-1407 AC1: a cluster
// linked to a healthy tunnel never fires, across several cycles.
func TestFederationTunnelFindings_HealthyNoFinding(t *testing.T) {
	db := newDebouncer()
	prov := staticFederation{clusters: []FederatedCluster{
		{ClusterID: "cl-1", ClusterName: "east", TunnelNode: wireguard.FixtureNode, TunnelIfName: wireguard.FixtureIfName},
	}}
	wg := staticWG{[]wireguard.ObservedTunnel{wireguard.FixtureHealthy(wgNow)}}
	for i := 0; i < 4; i++ {
		if got := federationTunnelFindings(prov, wg, db, wgNow); len(got) != 0 {
			t.Fatalf("cycle %d: healthy tunnel produced %d findings, want 0", i, len(got))
		}
	}
}

// TestFederationTunnelFindings_StaleRaisesAndClears is T-1407 AC2: a
// fixture-driven stale handshake raises exactly one tunnel_down_peer_unreachable
// finding (hysteresis-debounced, a single missed check must not fire), and it
// clears once the tunnel returns to healthy.
func TestFederationTunnelFindings_StaleRaisesAndClears(t *testing.T) {
	db := newDebouncer()
	prov := staticFederation{clusters: []FederatedCluster{
		{ClusterID: "cl-1", ClusterName: "east", TunnelNode: wireguard.FixtureNode, TunnelIfName: wireguard.FixtureIfName},
	}}
	stale := staticWG{[]wireguard.ObservedTunnel{wireguard.FixtureStaleHandshake(wgNow)}}

	if got := federationTunnelFindings(prov, stale, db, wgNow); len(got) != 0 {
		t.Fatalf("cycle 1: got %d findings, want 0 (single missed check must not fire)", len(got))
	}
	got := federationTunnelFindings(prov, stale, db, wgNow)
	if len(got) != 1 {
		t.Fatalf("cycle 2: got %+v, want exactly one finding", got)
	}
	f := got[0]
	if f.Check != CheckTunnelDownPeerUnreachable {
		t.Errorf("check = %q, want %q", f.Check, CheckTunnelDownPeerUnreachable)
	}
	if f.Source != SourceFederation {
		t.Errorf("source = %q, want %q", f.Source, SourceFederation)
	}
	if f.ID != "federation:"+CheckTunnelDownPeerUnreachable+"|cl-1" {
		t.Errorf("id = %q, want a stable federation:...|cl-1 id", f.ID)
	}
	if f.Fixable {
		t.Error("Fixable = true, want false (T-1407 AC4: no auto-remediation)")
	}
	if len(f.Nodes) != 1 || f.Nodes[0] != wireguard.FixtureNode {
		t.Errorf("Nodes = %v, want [%s]", f.Nodes, wireguard.FixtureNode)
	}

	// Return to healthy: clears within the fall window.
	healthy := staticWG{[]wireguard.ObservedTunnel{wireguard.FixtureHealthy(wgNow)}}
	_ = federationTunnelFindings(prov, healthy, db, wgNow)
	if got := federationTunnelFindings(prov, healthy, db, wgNow); len(got) != 0 {
		t.Fatalf("after healthy: got %d findings, want 0 (should clear)", len(got))
	}
}

// TestFederationTunnelFindings_NilProviderSkips proves a nil
// FederationProvider or WGProvider quietly contributes nothing (the same
// degraded-mode convention every other optional producer uses).
func TestFederationTunnelFindings_NilProviderSkips(t *testing.T) {
	db := newDebouncer()
	wg := staticWG{[]wireguard.ObservedTunnel{wireguard.FixtureStaleHandshake(wgNow)}}
	if got := federationTunnelFindings(nil, wg, db, wgNow); len(got) != 0 {
		t.Fatalf("nil FederationProvider: got %d findings, want 0", len(got))
	}
	prov := staticFederation{clusters: []FederatedCluster{{ClusterID: "cl-1", TunnelNode: wireguard.FixtureNode, TunnelIfName: wireguard.FixtureIfName}}}
	if got := federationTunnelFindings(prov, nil, db, wgNow); len(got) != 0 {
		t.Fatalf("nil WGProvider: got %d findings, want 0", len(got))
	}
}

// TestFederationTunnelFindings_EngineWires proves the Engine surfaces
// federation-source findings end-to-end through Findings() when both
// providers are wired.
func TestFederationTunnelFindings_EngineWires(t *testing.T) {
	prov := staticFederation{clusters: []FederatedCluster{
		{ClusterID: "cl-1", ClusterName: "east", TunnelNode: wireguard.FixtureNode, TunnelIfName: wireguard.FixtureIfName},
	}}
	e := New(Config{
		Federation: prov,
		WG:         staticWG{[]wireguard.ObservedTunnel{wireguard.FixtureStaleHandshake(wgNow)}},
		Now:        func() time.Time { return wgNow },
	})
	_ = e.Findings() // cycle 1: below rise threshold
	fs := e.Findings()
	if countCheck(fs, CheckTunnelDownPeerUnreachable) != 1 {
		t.Fatalf("engine did not surface tunnel_down_peer_unreachable: %+v", fs)
	}
}
