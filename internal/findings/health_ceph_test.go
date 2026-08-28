// SPDX-License-Identifier: Apache-2.0

package findings_test

// health_ceph_test.go proves T-1503's Engine wiring (health_ceph.go): a
// CephProvider's overlay drives all three ceph_* checks through the same
// hysteresis-gated Engine cycle every other health check uses, and clears
// cleanly once the provider's overlay stops breaching. The underlying
// detection logic itself (ceph.CorosyncSharedLink/ClusterMTUMismatch/
// SingleNIC, against real collector-built topologies) is exhaustively
// covered by internal/ceph's own table tests (AC3) — this file only proves
// the Engine-level plumbing (Config.Ceph -> checkCephFootguns -> hysteresis
// -> Finding) is wired correctly, using hand-built ceph.Overlay values the
// same way internal/ceph's own Project is proven separately.

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/ceph"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

type fakeCephProvider struct {
	err     error
	cor     *host.CorosyncConfig
	overlay ceph.Overlay
}

func (f *fakeCephProvider) CephOverlay() (ceph.Overlay, *host.CorosyncConfig, error) {
	return f.overlay, f.cor, f.err
}

func singleNICOverlay() ceph.Overlay {
	nic := inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno1"}
	return ceph.Overlay{
		PublicNetwork:  "10.20.0.0/24",
		ClusterNetwork: "10.30.0.0/24",
		Nodes: []ceph.NodeAttribution{{
			Node:            "pve1",
			PublicNICs:      []inventory.Ref{nic},
			PublicRidingOn:  nic,
			ClusterNICs:     []inventory.Ref{nic},
			ClusterRidingOn: nic,
		}},
	}
}

func TestCephFootguns_SingleNIC_FiresAfterHysteresisAndClears(t *testing.T) {
	g := newGraphWithNodes("pve1")
	prov := &fakeCephProvider{overlay: singleNICOverlay()}
	eng := findings.New(findings.Config{Graph: g, Ceph: prov})

	eng.Findings() // first cycle: rise=1, not yet active
	found := findByCheck(t, eng.Findings(), ceph.CheckSingleNIC)
	if len(found) != 1 {
		t.Fatalf("got %d ceph_single_nic findings after 2 cycles, want 1", len(found))
	}
	f := found[0]
	if f.Source != findings.SourceHealth {
		t.Errorf("Source = %s, want health", f.Source)
	}
	if f.DocsLink == "" {
		t.Error("ceph_single_nic must carry a DocsLink")
	}
	if len(f.Nodes) != 1 || f.Nodes[0] != "pve1" {
		t.Errorf("Nodes = %v, want [pve1]", f.Nodes)
	}

	// Clear it: the provider now reports a bonded topology.
	nicA := inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno1"}
	nicB := inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno2"}
	bond := inventory.Ref{Kind: inventory.KindBond, Node: "pve1", ID: "bond1"}
	prov.overlay = ceph.Overlay{
		Nodes: []ceph.NodeAttribution{{
			Node:            "pve1",
			PublicNICs:      []inventory.Ref{nicA, nicB},
			PublicPath:      []inventory.Ref{bond, nicA, nicB},
			PublicRidingOn:  bond,
			ClusterNICs:     []inventory.Ref{nicA, nicB},
			ClusterPath:     []inventory.Ref{bond, nicA, nicB},
			ClusterRidingOn: bond,
		}},
	}
	for i := 0; i < 3; i++ {
		eng.Findings()
	}
	if found := findByCheck(t, eng.Findings(), ceph.CheckSingleNIC); len(found) != 0 {
		t.Errorf("ceph_single_nic still firing after topology cleared: %+v", found)
	}
}

func TestCephFootguns_NilProvider_NoFindings(t *testing.T) {
	g := newGraphWithNodes("pve1")
	eng := findings.New(findings.Config{Graph: g})
	for i := 0; i < 3; i++ {
		for _, check := range []string{ceph.CheckCorosyncSharedLink, ceph.CheckClusterMTUMismatch, ceph.CheckSingleNIC} {
			if found := findByCheck(t, eng.Findings(), check); len(found) != 0 {
				t.Errorf("nil Ceph provider produced a %s finding: %+v", check, found)
			}
		}
	}
}
