package spec_test

// Test helpers: build a real inventory.Graph from a pvemock cluster fixture
// via one full collect poll cycle — the same pattern
// internal/change/validate_crossnode_fixture_test.go and internal/drift's
// messy-brownfield test use, so bridges, bonds, VLAN sub-interfaces, cluster
// nodes and SDN objects all resolve exactly as they would on a live cluster.

import (
	"context"
	"flag"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/collect"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
)

// updateGolden regenerates the *.golden.yaml fixtures instead of asserting
// against them (go test ./internal/spec -run TestExport_GoldenSchema -update).
var updateGolden = flag.Bool("update", false, "update golden spec files")

const (
	fixtureSingleNode = "../../testdata/clusters/single-node.yaml"
	fixtureThreeNode  = "../../testdata/clusters/three-node-vlan.yaml"
	fixtureEVPNLab    = "../../testdata/clusters/evpn-lab.yaml"
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

// goldenPath returns the testdata path for a fixture's golden spec file.
func goldenPath(name string) string {
	return filepath.Join("testdata", name+".golden.yaml")
}

// assertGolden compares got against the golden file for name, or rewrites it
// under -update.
func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := goldenPath(name)
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (run with -update to create): %v", path, err)
	}
	if string(got) != string(want) {
		t.Errorf("exported spec for %s does not match golden %s.\n--- got ---\n%s\n--- want ---\n%s",
			name, path, got, want)
	}
}
