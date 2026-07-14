package topology_test

// Shared test scaffolding: spinning up internal/pvemock (T-004) behind an
// httptest.Server and running a real internal/collect.Collector against it
// to populate a real inventory.Graph — the same pattern internal/collect's
// own tests use (see internal/collect/testhelpers_test.go), reused here so
// this package's acceptance tests exercise the full pvemock -> collect ->
// inventory.Graph -> topology.Project pipeline rather than hand-built
// fixtures.

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/collect"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
)

const (
	fixtureSingleNode    = "../../testdata/clusters/single-node.yaml"
	fixtureThreeNodeVlan = "../../testdata/clusters/three-node-vlan.yaml"
	// fixtureVlanMgmt is T-702's dedicated fixture: a single node whose
	// management IP lives on a VLAN sub-interface rather than directly on a
	// bridge (testdata/clusters/vlan-mgmt.yaml's own doc comment).
	fixtureVlanMgmt = "../../testdata/clusters/vlan-mgmt.yaml"
)

// loadFixtureServer loads fixturePath and builds a fresh, unstarted
// *pvemock.Server over it.
func loadFixtureServer(t *testing.T, fixturePath string) *pvemock.Server {
	t.Helper()
	f, err := pvemock.LoadFixture(fixturePath)
	if err != nil {
		t.Fatalf("LoadFixture(%s): %v", fixturePath, err)
	}
	return pvemock.NewServer(f)
}

// newTicketClient builds a pve.Client authenticating via ticket auth against
// a running mock at apiURL (pvemock does not implement PVE API-token auth).
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

// buildGraph runs one full PVE+host+LLDP poll cycle (via RefreshNow) against
// fixturePath's mock server and returns the resulting *inventory.Graph, the
// *collect.Collector (for further RefreshNow calls / OnDelta wiring in
// WS tests), and the running *httptest.Server (closed on test cleanup).
func buildGraph(t *testing.T, fixturePath string, opts ...func(*collect.Config)) (*inventory.Graph, *collect.Collector, *httptest.Server) {
	t.Helper()
	graph, c, ts, _ := buildGraphWithMock(t, fixturePath, opts...)
	return graph, c, ts
}

// buildGraphWithMock is buildGraph, additionally returning the underlying
// *pvemock.Server for tests that need direct fixture access (e.g. comparing
// GET /inventory/{ref}'s raw source against the fixture's own interfaces
// file).
func buildGraphWithMock(t *testing.T, fixturePath string, opts ...func(*collect.Config)) (*inventory.Graph, *collect.Collector, *httptest.Server, *pvemock.Server) {
	t.Helper()
	srv := loadFixtureServer(t, fixturePath)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	graph := inventory.NewGraph()
	cfg := collect.Config{
		PVE:   newTicketClient(t, ts.URL),
		Host:  host.NewFixtureReader(pvemock.NewFixtureHostReader(srv)),
		Graph: graph,
	}
	for _, o := range opts {
		o(&cfg)
	}

	c, err := collect.New(cfg)
	if err != nil {
		t.Fatalf("collect.New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.RefreshNow(ctx, inventory.Scope{}); err != nil {
		t.Fatalf("RefreshNow: %v", err)
	}
	return graph, c, ts, srv
}
