// SPDX-License-Identifier: Apache-2.0

package collect_test

import (
	"context"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/fw"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

const fixtureMessyBrownfield = "../../testdata/clusters/messy-brownfield.yaml"

// TestFirewallBanners_DatacenterOffCascadesOnRealFixture is T-501
// acceptance criterion 3, run against a real fixture (rather than a
// hand-built Snapshot): testdata/clusters/messy-brownfield.yaml's "MESS 6"
// is exactly the documented footgun — pve3's guest 105 (quarantine-box)
// has an enabled, populated guest-scope ruleset, but the cluster
// (datacenter) firewall is globally disabled, so the warning must surface
// at both the datacenter scope and the guest scope beneath it, even though
// the guest's own toggle is nominally on.
func TestFirewallBanners_DatacenterOffCascadesOnRealFixture(t *testing.T) {
	srv := loadFixtureServer(t, fixtureMessyBrownfield)
	c, graph, _ := newTestCollector(t, srv)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.RunPVELoop(ctx) }()

	quarantineBox := inventory.Ref{Kind: inventory.KindGuest, Node: "pve3", ID: "105"}
	waitFor(t, 3*time.Second, "quarantine-box's firewall ruleset to converge", func() bool {
		snap := fw.BuildSnapshot(graph.Snapshot().All())
		return snap.Cluster != nil && snap.Guests[quarantineBox] != nil
	})

	snap := fw.BuildSnapshot(graph.Snapshot().All())
	if snap.Cluster.Enabled {
		t.Fatal("fixture precondition failed: expected the cluster firewall to be disabled")
	}

	view, err := fw.Resolve(snap, quarantineBox)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if view.Active {
		t.Fatal("Active = true, want false: the datacenter firewall is off")
	}
	foundClusterGate := false
	for _, g := range view.Gates {
		if g.Scope == inventory.FwScopeCluster {
			foundClusterGate = true
		}
	}
	if !foundClusterGate {
		t.Errorf("Gates = %+v, want a cluster-scope gate cascading to this guest", view.Gates)
	}
	// The guest's own configured rule is still visible (transparency), even
	// though it is inert.
	if len(view.Rules) == 0 {
		t.Error("Rules is empty, want quarantine-box's configured (inert) rule still shown")
	}

	guestRS := snap.Guests[quarantineBox]
	guestBanners := fw.ScopeBanners(snap, inventory.FwScopeGuest, "pve3", guestRS)
	if len(guestBanners) != 1 || guestBanners[0].Scope != inventory.FwScopeCluster {
		t.Errorf("ScopeBanners(guest) = %+v, want exactly one cascaded cluster-scope banner", guestBanners)
	}

	nodeRS := snap.Nodes["pve3"]
	nodeBanners := fw.ScopeBanners(snap, inventory.FwScopeNode, "pve3", nodeRS)
	if len(nodeBanners) != 1 || nodeBanners[0].Scope != inventory.FwScopeCluster {
		t.Errorf("ScopeBanners(node) = %+v, want exactly one cascaded cluster-scope banner", nodeBanners)
	}

	clusterBanners := fw.ScopeBanners(snap, inventory.FwScopeCluster, "", snap.Cluster)
	if len(clusterBanners) != 1 {
		t.Errorf("ScopeBanners(cluster) = %+v, want exactly one banner", clusterBanners)
	}
}
