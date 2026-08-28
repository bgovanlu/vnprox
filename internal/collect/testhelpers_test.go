// SPDX-License-Identifier: Apache-2.0

package collect_test

// Test helpers shared by this package's tests: spinning up
// internal/pvemock (T-004) behind an httptest.Server (optionally on a
// fixed, restartable address for the outage-recovery test), and building a
// Collector wired against it with ticket auth — pvemock does not implement
// PVE API-token authentication (a documented T-101 gap; see
// internal/pve/integration_test.go's own TestAPIToken commentary), so every
// test in this package authenticates the same way internal/pve's own
// integration tests do: AuthTicket against the fixture's built-in
// root@pam/vnprox-mock user.

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/collect"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
)

const fixtureThreeNode = "../../testdata/clusters/three-node-vlan.yaml"

// loadFixtureServer loads fixturePath and builds a fresh, unstarted
// *pvemock.Server over it.
func loadFixtureServer(t testing.TB, fixturePath string) *pvemock.Server {
	t.Helper()
	f, err := pvemock.LoadFixture(fixturePath)
	if err != nil {
		t.Fatalf("LoadFixture(%s): %v", fixturePath, err)
	}
	return pvemock.NewServer(f)
}

// newTicketClient builds a pve.Client authenticating via ticket auth
// against a running mock at apiURL.
func newTicketClient(t testing.TB, apiURL string) *pve.Client {
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

// newFixtureHostReader builds a host.Reader over srv's fixture state, so
// host/LLDP polls see the same three-node-vlan world the PVE API does.
func newFixtureHostReader(srv *pvemock.Server) host.Reader {
	return host.NewFixtureReader(pvemock.NewFixtureHostReader(srv))
}

// newTestCollector builds a Collector (and the *inventory.Graph it feeds,
// returned so tests can assert against it directly) against a plain
// (non-restartable) httptest.Server wrapping the given mock server, with
// short poll intervals suited to fast tests. t.Cleanup stops the
// underlying HTTP server.
func newTestCollector(t testing.TB, srv *pvemock.Server, opts ...func(*collect.Config)) (*collect.Collector, *inventory.Graph, *httptest.Server) {
	t.Helper()
	return newTestCollectorHandler(t, srv, srv, opts...)
}

// newTestCollectorHandler is newTestCollector with the HTTP side served by
// handler instead of srv directly, letting a test interpose on the mock's
// API responses (e.g. the cluster-membership filter in
// TestDepartedNodeRetired) while host/LLDP reads still come from srv's
// fixture state.
func newTestCollectorHandler(t testing.TB, srv *pvemock.Server, handler http.Handler, opts ...func(*collect.Config)) (*collect.Collector, *inventory.Graph, *httptest.Server) {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	graph := inventory.NewGraph()
	cfg := collect.Config{
		PVE:          newTicketClient(t, ts.URL),
		Host:         newFixtureHostReader(srv),
		Graph:        graph,
		PVEInterval:  50 * time.Millisecond,
		HostInterval: 50 * time.Millisecond,
		LLDPInterval: 50 * time.Millisecond,
	}
	for _, o := range opts {
		o(&cfg)
	}

	c, err := collect.New(cfg)
	if err != nil {
		t.Fatalf("collect.New: %v", err)
	}
	return c, graph, ts
}

// restartableMock wraps a pvemock.Server behind an httptest.Server bound to
// a fixed address, so it can be Stop()ped (closing the listener, so
// in-flight and new connections fail exactly like a stopped daemon) and
// later Start()ed again on the same address (like the daemon restarting) —
// used by the PVE-outage-recovery test.
type restartableMock struct {
	handler http.Handler
	ts      *httptest.Server
	addr    string
}

func newRestartableMock(t *testing.T, handler http.Handler) *restartableMock {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	m := &restartableMock{handler: handler, addr: lis.Addr().String()}
	m.startOn(t, lis)
	t.Cleanup(func() {
		if m.ts != nil {
			m.ts.Close()
		}
	})
	return m
}

func (m *restartableMock) startOn(t *testing.T, lis net.Listener) {
	t.Helper()
	ts := httptest.NewUnstartedServer(m.handler)
	_ = ts.Listener.Close()
	ts.Listener = lis
	ts.Start()
	m.ts = ts
}

func (m *restartableMock) URL() string { return "http://" + m.addr }

// Stop closes the listener, so subsequent client requests fail with a
// connection-refused transport error — simulating "the mock process
// stopped".
func (m *restartableMock) Stop() {
	m.ts.Close()
	m.ts = nil
}

// Start rebinds a listener on the same address and serves again —
// simulating "the mock process restarted".
func (m *restartableMock) Start(t *testing.T) {
	t.Helper()
	lis, err := net.Listen("tcp", m.addr)
	if err != nil {
		t.Fatalf("net.Listen (restart) on %s: %v", m.addr, err)
	}
	m.startOn(t, lis)
}

// waitFor polls check every 20ms until it returns true or timeout elapses,
// failing the test otherwise.
func waitFor(t testing.TB, timeout time.Duration, msg string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if check() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for: %s", msg)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
