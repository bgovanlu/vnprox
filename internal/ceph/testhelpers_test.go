package ceph_test

// Shared test scaffolding: spin up internal/pvemock behind an
// httptest.Server and run a real internal/collect.Collector against it to
// populate a real inventory.Graph — the exact same pattern
// internal/topology's own tests use (internal/topology/testhelpers_test.go),
// reused here so this package's acceptance tests exercise the full
// pvemock -> collect -> inventory.Graph -> ceph.Project pipeline rather
// than hand-built fixtures.

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/ceph"
	"github.com/bgovanlu/vnprox/internal/collect"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
)

const (
	fixtureCephClean              = "../../testdata/ceph/clean.yaml"
	fixtureCephCorosyncSharedLink = "../../testdata/ceph/corosync-shared-link.yaml"
	fixtureCephMTUMismatch        = "../../testdata/ceph/mtu-mismatch.yaml"
	fixtureCephSingleNIC          = "../../testdata/ceph/single-nic.yaml"
)

func newTicketClient(t *testing.T, apiURL string) *pve.Client {
	t.Helper()
	c, err := pve.New(pve.Config{
		APIURL:   apiURL,
		Auth:     pve.AuthTicket,
		Username: "root@pam",
		Password: "vnprox-mock",
	})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	return c
}

// buildSnapshotAndStatus loads fixturePath, runs one full collector poll to
// build a real inventory.Snapshot, and discovers Ceph status (config + OSD
// placement) via the same *pve.Client the collector used — the two live
// reads ceph.Project's real callers (cmd/vnproxd) combine.
func buildSnapshotAndStatus(t *testing.T, fixturePath string) (inventory.Snapshot, ceph.Status) {
	t.Helper()

	f, err := pvemock.LoadFixture(fixturePath)
	if err != nil {
		t.Fatalf("LoadFixture(%s): %v", fixturePath, err)
	}
	srv := pvemock.NewServer(f)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	pveClient := newTicketClient(t, ts.URL)

	graph := inventory.NewGraph()
	cfg := collect.Config{
		PVE:   pveClient,
		Host:  host.NewFixtureReader(pvemock.NewFixtureHostReader(srv)),
		Graph: graph,
	}
	c, err := collect.New(cfg)
	if err != nil {
		t.Fatalf("collect.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, refreshErr := c.RefreshNow(ctx, inventory.Scope{}); refreshErr != nil {
		t.Fatalf("RefreshNow: %v", refreshErr)
	}

	var nodes []string
	for name := range f.Nodes {
		nodes = append(nodes, name)
	}
	status, err := ceph.Discover(ctx, pveClient, nodes)
	if err != nil {
		t.Fatalf("ceph.Discover: %v", err)
	}

	return graph.Snapshot(), status
}
