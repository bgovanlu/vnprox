package change_test

// T-801 acceptance criteria 1–3 and 5, exercised through the public
// change.Validate against the *real* pvemock fixtures the card names
// (three-node-vlan, evpn-lab), complementing the white-box unit tests in
// internal/change's validate_crossnode_test.go. The graph is built the same
// way internal/drift's messybrownfield_test.go builds its own: one full
// pvemock -> collect -> inventory.Graph poll cycle, so the cross-node
// bridges, cluster nodes, and SDN zones all resolve exactly as they would on
// a live cluster.

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/collect"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
)

func buildFixtureGraph(t *testing.T, path string) *inventory.Graph {
	t.Helper()
	f, err := pvemock.LoadFixture(path)
	if err != nil {
		t.Fatalf("LoadFixture(%s): %v", path, err)
	}
	srv := pvemock.NewServer(f)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	client, err := pve.New(pve.Config{APIURL: ts.URL, Auth: pve.AuthTicket, Username: "root@pam", Password: "vnprox-mock"})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	graph := inventory.NewGraph()
	c, err := collect.New(collect.Config{
		PVE:   client,
		Host:  host.NewFixtureReader(pvemock.NewFixtureHostReader(srv)),
		Graph: graph,
	})
	if err != nil {
		t.Fatalf("collect.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.RefreshNow(ctx, inventory.Scope{}); err != nil {
		t.Fatalf("RefreshNow: %v", err)
	}
	return graph
}

func findByCode(findings []change.Finding, code string) []change.Finding {
	var out []change.Finding
	for _, f := range findings {
		if f.Code == code {
			out = append(out, f)
		}
	}
	return out
}

func bref(node, name string) inventory.Ref {
	return inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: name}
}

// TestCrossnodeFixture_MTU_ThreeNodeVlan is T-801 acceptance criterion 2
// against the real three-node-vlan fixture: bumping pve1's vmbr0 MTU to 9000
// (the cluster's same-named bridge is 1500 everywhere) is a
// crossnode.mtu_consistency error with a majority-alignment fix; applying the
// fix revalidates clean.
func TestCrossnodeFixture_MTU_ThreeNodeVlan(t *testing.T) {
	snap := buildFixtureGraph(t, "../../testdata/clusters/three-node-vlan.yaml").Snapshot()

	mtu9000 := 9000
	bump := change.Op{Type: change.OpBridgeUpdate, Target: bref("pve1", "vmbr0"),
		Params: &change.BridgeUpdateParams{MTU: &mtu9000}}

	findings := change.Validate([]change.Op{bump}, snap)
	mtu := findByCode(findings, "crossnode.mtu_consistency")
	if len(mtu) != 1 {
		t.Fatalf("crossnode.mtu_consistency = %d, want 1: %+v", len(mtu), findings)
	}
	if mtu[0].Severity != change.SeverityError || len(mtu[0].Fix) != 1 {
		t.Fatalf("finding = %+v, want one error with a single fix op", mtu[0])
	}
	p, ok := mtu[0].Fix[0].Params.(*change.BridgeUpdateParams)
	if !ok || p.MTU == nil || *p.MTU != 1500 || mtu[0].Fix[0].Target.Node != "pve1" {
		t.Fatalf("fix = %+v, want bridge.update pve1 MTU=1500", mtu[0].Fix[0])
	}

	after := change.Validate(mtu[0].Fix, snap)
	if len(findByCode(after, "crossnode.mtu_consistency")) != 0 {
		t.Errorf("fix did not clear the divergence: %+v", after)
	}
}

// TestCrossnodeFixture_SDN_EvpnLab is T-801 acceptance criterion 3 against the
// real evpn-lab fixture: a bare bridge.delete (no sdn.* op) that removes vmbr0
// on pve2 — the realizing bridge for the vlanz/qinqz zones — is a
// crossnode.sdn_realization error with no fix, and T-402's SDN class (whose
// codes are all "sdn.*") catches nothing of the sort.
func TestCrossnodeFixture_SDN_EvpnLab(t *testing.T) {
	snap := buildFixtureGraph(t, "../../testdata/clusters/evpn-lab.yaml").Snapshot()

	del := change.Op{Type: change.OpBridgeDelete, Target: bref("pve2", "vmbr0"),
		Params: &change.BridgeDeleteParams{}}

	findings := change.Validate([]change.Op{del}, snap)
	sdn := findByCode(findings, "crossnode.sdn_realization")
	if len(sdn) == 0 {
		t.Fatalf("expected at least one crossnode.sdn_realization finding, got: %+v", findings)
	}
	for _, f := range sdn {
		if f.Severity != change.SeverityError {
			t.Errorf("sdn_realization severity = %s, want error", f.Severity)
		}
		if len(f.Fix) != 0 {
			t.Errorf("sdn_realization must have no fix, got %+v", f.Fix)
		}
	}

	// Companion negative: T-402's SDN class produces no "sdn.*" finding for
	// this shape — the gap T-801 closes. (A crossnode.* code is not an sdn.*
	// code.)
	for _, f := range findings {
		if len(f.Code) >= 4 && f.Code[:4] == "sdn." {
			t.Errorf("T-402's SDN class unexpectedly fired for a bare bridge.delete: %s (%s)", f.Code, f.Message)
		}
	}
}

// TestCrossnodeFixture_Lockstep_NoFindings is T-801 acceptance criterion 5
// against both named fixtures: a changeset changing every node's same-named
// bridge MTU in lockstep introduces no divergence, so no crossnode.* finding
// is produced.
func TestCrossnodeFixture_Lockstep_NoFindings(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
		bridge  string
		mtu     int
	}{
		// three-node-vlan's vmbr0 is 1500 everywhere; evpn-lab's is 9216.
		{"three-node-vlan", "../../testdata/clusters/three-node-vlan.yaml", "vmbr0", 1400},
		{"evpn-lab", "../../testdata/clusters/evpn-lab.yaml", "vmbr0", 9000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap := buildFixtureGraph(t, tc.fixture).Snapshot()
			mtu := tc.mtu
			var ops []change.Op
			for _, n := range []string{"pve1", "pve2", "pve3"} {
				ops = append(ops, change.Op{Type: change.OpBridgeUpdate, Target: bref(n, tc.bridge),
					Params: &change.BridgeUpdateParams{MTU: &mtu}})
			}
			findings := change.Validate(ops, snap)
			for _, f := range findings {
				if len(f.Code) >= 10 && f.Code[:10] == "crossnode." {
					t.Errorf("lockstep change produced a cross-node finding: %s (%s)", f.Code, f.Message)
				}
			}
		})
	}
}
