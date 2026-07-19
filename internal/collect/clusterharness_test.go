package collect_test

// T-303 acceptance criteria 1, 2, and 4: a three-daemon harness — three
// independent *collect.Collector instances, each with its own
// *inventory.Graph and *topology.Service, wired to each other through real
// internal/peer.Server/Client instances over real HTTP (loopback), exactly
// the shape production cmd/vnproxd wiring builds (see cmd/vnproxd/collect.go
// and server.go) — proving the host-poller extension's cluster-wide fan-out,
// per-peer staleness/healing, and node-tag attribution end to end rather
// than only at the unit level.

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/collect"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/pvemock"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// clusterHarnessNodes is the three-node-vlan fixture's member list, in
// fixture order.
var clusterHarnessNodes = []string{"pve1", "pve2", "pve3"}

// nodeRestrictedReader wraps a host.Reader that (like the fixture reader)
// can serve any node's data and restricts it to exactly one node, ErrNotFound
// otherwise — mimicking host.Real's documented "only ever serves its own
// node" restriction, so this harness's peer fan-out genuinely has to cross
// the peer API to see another node's host state, not just call straight
// through a shared fixture reader.
type nodeRestrictedReader struct {
	inner host.Reader
	node  string
}

func (r nodeRestrictedReader) InterfacesFile(ctx context.Context, node string, pending bool) (string, error) {
	if node != r.node {
		return "", host.ErrNotFound
	}
	return r.inner.InterfacesFile(ctx, node, pending)
}

func (r nodeRestrictedReader) Links(ctx context.Context, node string) ([]host.LinkState, error) {
	if node != r.node {
		return nil, host.ErrNotFound
	}
	return r.inner.Links(ctx, node)
}

func (r nodeRestrictedReader) LLDP(ctx context.Context, node string) ([]byte, error) {
	if node != r.node {
		return nil, host.ErrNotFound
	}
	return r.inner.LLDP(ctx, node)
}

func (r nodeRestrictedReader) Stats(ctx context.Context, node string) (map[string]host.IfaceStats, error) {
	if node != r.node {
		return nil, host.ErrNotFound
	}
	return r.inner.Stats(ctx, node)
}

func (r nodeRestrictedReader) FRRBGPSummary(ctx context.Context, node string) ([]byte, error) {
	if node != r.node {
		return nil, host.ErrNotFound
	}
	return r.inner.FRRBGPSummary(ctx, node)
}

func (r nodeRestrictedReader) FRREVPNVNI(ctx context.Context, node string) ([]byte, error) {
	if node != r.node {
		return nil, host.ErrNotFound
	}
	return r.inner.FRREVPNVNI(ctx, node)
}

func (r nodeRestrictedReader) Services(ctx context.Context, node string) (map[string]bool, error) {
	if node != r.node {
		return nil, host.ErrNotFound
	}
	return r.inner.Services(ctx, node)
}

func (r nodeRestrictedReader) DHCPLeases(ctx context.Context, node string) ([]byte, error) {
	if node != r.node {
		return nil, host.ErrNotFound
	}
	return r.inner.DHCPLeases(ctx, node)
}

func (r nodeRestrictedReader) CorosyncStatus(ctx context.Context, node string) ([]byte, error) {
	if node != r.node {
		return nil, host.ErrNotFound
	}
	return r.inner.CorosyncStatus(ctx, node)
}

func (r nodeRestrictedReader) Neighbors(ctx context.Context, node string) ([]host.Neighbor, error) {
	if node != r.node {
		return nil, host.ErrNotFound
	}
	return r.inner.Neighbors(ctx, node)
}

func (r nodeRestrictedReader) ContainerInterior(ctx context.Context, node string, vmid int) (host.ContainerInteriorRaw, error) {
	if node != r.node {
		return host.ContainerInteriorRaw{}, host.ErrNotFound
	}
	return r.inner.ContainerInterior(ctx, node, vmid)
}

func (r nodeRestrictedReader) ContainerPing(ctx context.Context, node string, vmid int, targetIP string) (bool, error) {
	if node != r.node {
		return false, host.ErrNotFound
	}
	return r.inner.ContainerPing(ctx, node, vmid, targetIP)
}

// reorderLocalFirst returns a copy of nodes with local moved to index 0 —
// pvemock's GET /cluster/status marks index 0 "local" unconditionally
// (internal/pvemock/cluster.go), so this is how each simulated daemon gets
// its own distinct "local node" identity from an otherwise-identical
// fixture clone.
func reorderLocalFirst(nodes []pvemock.ClusterNodeSpec, local string) []pvemock.ClusterNodeSpec {
	out := make([]pvemock.ClusterNodeSpec, 0, len(nodes))
	for _, n := range nodes {
		if n.Name == local {
			out = append(out, n)
		}
	}
	for _, n := range nodes {
		if n.Name != local {
			out = append(out, n)
		}
	}
	return out
}

// clusterDaemon is one simulated vnproxd instance in the harness.
type clusterDaemon struct {
	peerHandler http.Handler
	graph       *inventory.Graph
	collector   *collect.Collector
	topo        *topology.Service
	pveSrv      *httptest.Server
	peerTS      *httptest.Server
	t           *testing.T
	reader      nodeRestrictedReader
	node        string
	peerAddr    string
}

// stopPeer closes this daemon's peer-API listener (simulating "this
// daemon's process died" from every other daemon's point of view — T-303
// AC2's "kill one peer").
func (d *clusterDaemon) stopPeer() {
	d.peerTS.Close()
}

// startPeer rebinds this daemon's peer-API listener on the exact same
// address and resumes serving the same handler — simulating the peer
// coming back without any daemon (this one or any other) being restarted,
// per AC2's "peer's return heals everything without restarts".
func (d *clusterDaemon) startPeer() {
	d.t.Helper()
	lis, err := net.Listen("tcp", d.peerAddr)
	if err != nil {
		d.t.Fatalf("restarting peer listener for %s on %s: %v", d.node, d.peerAddr, err)
	}
	ts := &httptest.Server{Listener: lis, Config: &http.Server{Handler: d.peerHandler}}
	ts.Start()
	d.t.Cleanup(ts.Close)
	d.peerTS = ts
}

// clusterHarness wires up clusterHarnessNodes daemons: each gets its own
// PVE mock (a clone of the shared fixture, reordered so GET /cluster/status
// reports that daemon's own node as local), its own node-restricted host
// reader, its own peer.Server (mounted over real HTTP on a distinct
// loopback address sharing one common port, since production peer.Client
// addressing assumes one cluster-wide port per docs/architecture.md §5),
// and its own peer.Client/Collector/topology.Service pair.
type clusterHarness struct {
	daemons map[string]*clusterDaemon
}

// peerListenerFor picks a free loopback port once, then binds addr on that
// same port for the given node — see peerAddrFor's doc comment for why
// every peer needs the *same* port with a *distinct* IP.
func peerListenerFor(t *testing.T, port int, node string) net.Listener {
	t.Helper()
	addr := peerAddrFor(node, port)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("listening on %s for node %s: %v", addr, node, err)
	}
	return lis
}

// peerIPFor deterministically maps a fixture node name to a loopback IP
// (127.0.0.11/.12/.13 for pve1/pve2/pve3) so every simulated daemon's peer
// server can be reached at a distinct address while all sharing one port
// number, matching peer.Client's single-Port-for-the-whole-cluster design
// (docs/architecture.md §5: peers are reached at a fixed cluster-wide
// port). Also used to override each fixture clone's GET /cluster/status IP
// field, so real peer discovery (via pollClusterStatus, exactly as
// production code does it) resolves to these test-reachable addresses
// instead of the fixture's documentation-only 10.10.0.1x IPs.
func peerIPFor(node string) string {
	idx := 1
	for i, n := range clusterHarnessNodes {
		if n == node {
			idx = i + 1
		}
	}
	return fmt.Sprintf("127.0.0.%d", 10+idx)
}

func peerAddrFor(node string, port int) string {
	return fmt.Sprintf("%s:%d", peerIPFor(node), port)
}

func newClusterHarness(t *testing.T) *clusterHarness {
	t.Helper()
	base, err := pvemock.LoadFixture(fixtureThreeNode)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}

	// One free port, shared by every simulated peer server (see
	// peerAddrFor). A short race window exists between picking it and
	// each daemon's own bind below; acceptable for a test harness (the
	// same pattern testhelpers_test.go's restartableMock already uses).
	probe, err := net.Listen("tcp", "127.0.0.11:0")
	if err != nil {
		t.Fatalf("probing a free port: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()

	secretPath := filepath.Join(t.TempDir(), "cluster.secret")
	logger := discardCollectLogger()

	h := &clusterHarness{daemons: map[string]*clusterDaemon{}}

	for idx, node := range clusterHarnessNodes {
		fx := *base
		nodes := make([]pvemock.ClusterNodeSpec, len(base.Cluster.Nodes))
		copy(nodes, base.Cluster.Nodes)
		for i := range nodes {
			nodes[i].IP = peerIPFor(nodes[i].Name)
		}
		fx.Cluster.Nodes = reorderLocalFirst(nodes, node)

		pveMock := pvemock.NewServer(&fx)
		pveTS := httptest.NewServer(pveMock)
		t.Cleanup(pveTS.Close)

		reader := nodeRestrictedReader{inner: newFixtureHostReader(pveMock), node: node}

		secrets, err := peer.LoadOrGenerateSecret(secretPath, logger)
		if err != nil {
			t.Fatalf("LoadOrGenerateSecret: %v", err)
		}
		peerSrv := peer.NewServer(peer.ServerOptions{
			Secrets: secrets, Reader: reader, Version: "test", Logger: logger,
		})
		r := chi.NewRouter()
		peerSrv.MountRoutes(r)
		peerAddr := peerAddrFor(node, port)
		peerTS := &httptest.Server{Listener: peerListenerFor(t, port, node), Config: &http.Server{Handler: r}}
		peerTS.Start()
		t.Cleanup(peerTS.Close)

		pveClient := newTicketClient(t, pveTS.URL)
		peerClient := peer.NewClient(peer.ClientOptions{
			ClusterStatus: pveClient, Secrets: secrets, Scheme: "http", Port: port, Logger: logger,
			// BreakerResetTimeout defaults to 15s (peer.DefaultBreakerReset
			// Timeout) — production-appropriate, but far longer than this
			// harness's short poll intervals should have to wait to observe
			// AC2's "peer's return heals everything" once a killed peer's
			// listener is back up.
			BreakerResetTimeout: 1500 * time.Millisecond,
		})

		graph := inventory.NewGraph()
		topoSvc := topology.NewService(graph, logger)
		// Intervals must stay above 1s: internal/peer's HMAC signature covers
		// a whole-second Unix timestamp plus a short-lived exact-replay
		// cache (docs/security.md), so two requests to the very same path
		// within the same wall-clock second are indistinguishable from a
		// replay and the second one is correctly rejected — a real risk
		// only at this harness's deliberately-fast polling cadence, never
		// at production's 5s+ default intervals. Each daemon also gets a
		// slightly different base interval (offset by idx) on top of the
		// existing ±10% jitter, so three daemons continuously polling each
		// other don't stay resonantly in phase across many cycles, which
		// would otherwise keep re-creating exactly this same-second
		// collision risk.
		collector, err := collect.New(collect.Config{
			PVE: pveClient, Host: reader, Peer: peerClient, Graph: graph,
			PVEInterval:  time.Duration(700+50*idx) * time.Millisecond,
			HostInterval: time.Duration(1300+150*idx) * time.Millisecond,
			LLDPInterval: 5 * time.Second,
			Logger:       logger, OnDelta: topoSvc.OnDelta,
		})
		if err != nil {
			t.Fatalf("collect.New(%s): %v", node, err)
		}

		h.daemons[node] = &clusterDaemon{
			node: node, graph: graph, collector: collector, topo: topoSvc,
			pveSrv: pveTS, peerTS: peerTS, peerHandler: r, peerAddr: peerAddr,
			reader: reader, t: t,
		}
	}
	return h
}

// run starts every daemon's PVE and host poll loops under ctx. Each loop's
// very first attempt fires immediately (runLoop's documented behavior,
// deliberately — a freshly-started daemon shouldn't wait a full interval
// for its first poll); with three daemons all polling each other's peer
// API for the same handful of paths, launching every loop in lockstep would
// make it likely that two different daemons' independently-signed-but-
// otherwise-identical first requests to the same target land in the same
// wall-clock second, which internal/peer's replay cache (correctly, by
// design) can't distinguish from an actual replay. A small random startup
// stagger per loop decorrelates the phases, matching how real daemons
// starting at slightly different times naturally would.
func (h *clusterHarness) run(ctx context.Context) {
	startStaggered := func(fn func(context.Context) error) {
		go func() {
			select {
			case <-time.After(time.Duration(rand.Intn(900)) * time.Millisecond):
			case <-ctx.Done():
				return
			}
			_ = fn(ctx)
		}()
	}
	for _, d := range h.daemons {
		startStaggered(d.collector.RunPVELoop)
		startStaggered(d.collector.RunHostLoop)
	}
}

// hostNetlinkRefs returns, for every (daemon, cluster node) pair, whether
// that daemon's graph carries a SourceHostNetlink contribution for
// cluster node n's bond0 — the fixture's netlink-observed signal (bond
// slave membership, NIC driver/speed — see pvemock.FixtureHostReader.Links)
// this harness uses to prove cluster-wide host-poller fan-out.
func (d *clusterDaemon) hasHostNetlink(node string) bool {
	ref := inventory.Ref{Kind: inventory.KindBond, Node: node, ID: "bond0"}
	_, ok := d.graph.Snapshot().RawSource(ref)[inventory.SourceHostNetlink]
	return ok
}

// discardCollectLogger silences logging for the harness's several poll
// loops (this package's other tests mostly accept the default logger; a
// 3-daemon harness running short poll intervals is noisy enough to be
// worth discarding).
func discardCollectLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
