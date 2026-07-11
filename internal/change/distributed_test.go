package change_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
	"github.com/bgovanlu/vnprox/internal/store"
)

// --- T-304: three-daemon distributed-rollback harness ---------------------
//
// This models three independent vnproxd daemons (pve1 coordinates; pve2 and
// pve3 are peers reached over a real HTTP+HMAC peer.Server/peer.Client pair,
// via an httptest.Server per node) sharing one pvemock-backed three-node
// cluster for the actual PVE-facing reload calls. Each daemon has its own
// SQLite-backed node_timers store and its own independently-fireable
// TimerFunc, so a test can fire pve2's or pve3's local rollback timer
// without the coordinator (pve1) being involved at all — the point of T-304.

// fakeHostWriter completes fakeNodeAgent (apply_helpers_test.go) into
// peer.HostWriter (RestoreInterfaces) so it can back a peer.Server's Writer.
// The distributed rollback path this file tests never calls
// RestoreInterfaces (it reuses stage+reload, same as T-205's restoreNode) —
// this exists only so fakeNodeAgent satisfies the interface.
type fakeHostWriter struct{ *fakeNodeAgent }

func (w fakeHostWriter) RestoreInterfaces(ctx context.Context, node, content string) error {
	if err := w.StageInterfaces(ctx, node, content); err != nil {
		return err
	}
	return w.ReloadInterfaces(ctx, node)
}

// fakeHostReader adapts fakeNodeAgent to peer.HostReader; only
// InterfacesFile is exercised (ClusterNodeAgent.ReadInterfaces is all
// captureSnapshot calls — see apply.go's nodeAgentReader for the same
// narrow-adapter pattern).
type fakeHostReader struct{ agent *fakeNodeAgent }

func (r fakeHostReader) InterfacesFile(ctx context.Context, node string, _ bool) (string, error) {
	return r.agent.ReadInterfaces(ctx, node)
}
func (r fakeHostReader) LLDP(context.Context, string) ([]byte, error) { return nil, nil }
func (r fakeHostReader) Stats(context.Context, string) (map[string]host.IfaceStats, error) {
	return nil, nil
}
func (r fakeHostReader) Links(context.Context, string) ([]host.LinkState, error) {
	return nil, nil
}
func (r fakeHostReader) Services(context.Context, string) (map[string]bool, error) {
	return nil, nil
}

// partitionableTransport is an http.RoundTripper that fails every request to
// a "cut" host (address, as in req.URL.Host) with a network-shaped error —
// exactly what peer.Client.do wraps as peer.ErrPeerUnreachable — so tests
// can simulate and heal a coordinator<->node partition deterministically,
// without real timing or an actually-closed listener (which, once closed,
// cannot heal).
type partitionableTransport struct {
	inner http.RoundTripper
	cut   map[string]*atomic.Bool
	mu    sync.Mutex
}

func newPartitionableTransport() *partitionableTransport {
	return &partitionableTransport{inner: http.DefaultTransport, cut: map[string]*atomic.Bool{}}
}

func (t *partitionableTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	flag, ok := t.cut[req.URL.Host]
	t.mu.Unlock()
	if ok && flag.Load() {
		return nil, fmt.Errorf("simulated partition to %s", req.URL.Host)
	}
	return t.inner.RoundTrip(req)
}

func (t *partitionableTransport) setCut(addr string, cut bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	flag, ok := t.cut[addr]
	if !ok {
		flag = &atomic.Bool{}
		t.cut[addr] = flag
	}
	flag.Store(cut)
}

// peerDaemon models one non-coordinating node's daemon: its own file state,
// its own node_timers DB, its own LocalTimerAgent (with an independently
// fireable TimerFunc), and its own peer.Server exposed over httptest.
type peerDaemon struct {
	node   string
	agent  *fakeNodeAgent
	repo   *store.NodeTimerRepo
	local  *change.LocalTimerAgent
	timers *fakeTimers
	http   *httptest.Server
	peer   peer.Peer
}

// threeDaemonHarness wires pve1 as the coordinator (its own change.Service,
// using its fakeNodeAgent directly as the "local" NodeAgent/LocalTimerAgent)
// plus pve2/pve3 as real peer daemons reachable over HTTP.
type threeDaemonHarness struct {
	now        time.Time
	svc        *change.Service
	coordAgent *fakeNodeAgent
	coordTimer *fakeTimers
	coordRepo  *store.NodeTimerRepo
	svcTimers  *fakeTimers
	peers      map[string]*peerDaemon
	transport  *partitionableTransport
	client     *pve.Client
	mockURL    string
	nowMu      sync.Mutex
}

// clock is the Now func every daemon in the harness shares (coordinator's
// change.Service and LocalTimerAgent, the peer client, and every peer
// daemon's own LocalTimerAgent/peer.Server): it auto-advances by one second
// on every read, so two calls are never simultaneous — mirroring the fact
// that real wall-clock time always moves between two genuinely separate
// network calls, however small the gap. This matters because
// internal/peer's replay cache keys on the exact signed request (method,
// path, body, whole-second timestamp): a frozen test clock would make two
// legitimately-distinct requests to the same node (e.g. this changeset's
// apply-time reload of pve2, and a later, separate reload of pve2 during
// rollback) sign identically and collide, which a real clock never does.
// Safe for concurrent use (cancelNodeTimers fans out across goroutines).
func (h *threeDaemonHarness) clock() time.Time {
	h.nowMu.Lock()
	defer h.nowMu.Unlock()
	t := h.now
	h.now = h.now.Add(time.Second)
	return t
}

func newThreeDaemonHarness(t *testing.T) *threeDaemonHarness {
	t.Helper()
	f, err := pvemock.LoadFixture(fixtureThreeNode)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	mockSrv := pvemock.NewServer(f)
	ts := httptest.NewServer(mockSrv)
	t.Cleanup(ts.Close)

	client, err := pve.New(pve.Config{APIURL: ts.URL, Auth: pve.AuthTicket, Username: "root@pam", Password: "vnprox-mock"})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}

	h := &threeDaemonHarness{now: time.Unix(1_700_000_000, 0), client: client, peers: map[string]*peerDaemon{}, mockURL: ts.URL}

	// One shared cluster-secret file, loaded independently by every daemon —
	// the same "generated once, pmxcfs-replicated" story docs/architecture.md
	// §5 describes, just backed by a shared tempdir instead of pmxcfs.
	secretPath := filepath.Join(t.TempDir(), "cluster.secret")
	coordSecrets, err := peer.LoadOrGenerateSecret(secretPath, nil)
	if err != nil {
		t.Fatalf("LoadOrGenerateSecret: %v", err)
	}

	h.coordAgent = newFakeNodeAgent(pvemock.NewFixtureHostReader(mockSrv), client)
	h.coordTimer = &fakeTimers{}
	coordDB := openTestDB(t)
	h.coordRepo = store.NewNodeTimerRepo(coordDB)
	coordLocal := change.NewLocalTimerAgent(change.LocalTimerConfig{
		Nodes: h.coordAgent, Repo: h.coordRepo, TimerFunc: h.coordTimer.New,
		Now: h.clock,
	})

	h.transport = newPartitionableTransport()
	peerClient := peer.NewClient(peer.ClientOptions{
		Secrets:    coordSecrets,
		Scheme:     "http",
		HTTPClient: &http.Client{Transport: h.transport},
		Now:        h.clock,
	})

	locator := change.StaticPeerLocator{}
	for _, node := range []string{"pve2", "pve3"} {
		secrets, secErr := peer.LoadOrGenerateSecret(secretPath, nil)
		if secErr != nil {
			t.Fatalf("LoadOrGenerateSecret(%s): %v", node, secErr)
		}
		h.peers[node] = h.newPeerDaemon(t, node, mockSrv, client, secrets)
		locator[node] = h.peers[node].peer
	}

	localNode := func() string { return "pve1" }
	clusterNodes := change.NewClusterNodeAgent(localNode, h.coordAgent, peerClient, locator)
	clusterTimers := change.NewClusterTimerAgent(localNode, coordLocal, peerClient, locator)

	h.svcTimers = &fakeTimers{}
	svc, err := change.NewService(change.Config{
		Changesets: store.NewChangesetRepo(coordDB), Audit: store.NewAuditRepo(coordDB),
		Nodes: clusterNodes, Timers: clusterTimers,
		Snapshots: store.NewSnapshotRepo(coordDB), Blobs: store.NewBlobRepo(coordDB),
		TimerFunc: h.svcTimers.New, Now: h.clock,
		ProtectedPath: filepath.Join(t.TempDir(), "protected.json"),
	})
	if err != nil {
		t.Fatalf("change.NewService: %v", err)
	}
	h.svc = svc
	return h
}

func (h *threeDaemonHarness) newPeerDaemon(t *testing.T, node string, mockSrv *pvemock.Server, client *pve.Client, secrets *peer.SecretStore) *peerDaemon {
	t.Helper()
	agent := newFakeNodeAgent(pvemock.NewFixtureHostReader(mockSrv), client)
	repo := store.NewNodeTimerRepo(openTestDB(t))
	timers := &fakeTimers{}
	local := change.NewLocalTimerAgent(change.LocalTimerConfig{
		Nodes: agent, Repo: repo, TimerFunc: timers.New, Now: h.clock,
	})

	srv := peer.NewServer(peer.ServerOptions{
		Secrets: secrets,
		Reader:  fakeHostReader{agent},
		Writer:  fakeHostWriter{agent},
		Timers:  local,
		Version: "test",
		Now:     h.clock,
	})
	r := chi.NewRouter()
	srv.MountRoutes(r)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)

	return &peerDaemon{
		node: node, agent: agent, repo: repo, local: local, timers: timers, http: ts,
		peer: peer.Peer{Node: node, Addr: ts.Listener.Addr().String()},
	}
}

// cutPeer/healPeer simulate/heal a coordinator<->node partition.
func (h *threeDaemonHarness) cutPeer(node string) { h.transport.setCut(h.peers[node].peer.Addr, true) }
func (h *threeDaemonHarness) healPeer(node string) {
	h.transport.setCut(h.peers[node].peer.Addr, false)
}

// committed returns node's current committed interfaces content, whichever
// daemon (coordinator or a peer) owns it.
func (h *threeDaemonHarness) committed(node string) string {
	if node == "pve1" {
		return h.coordAgent.committedFile(node)
	}
	return h.peers[node].agent.committedFile(node)
}

// advance moves the shared clock forward by d (all three daemons' Now
// closures read the same field, so this keeps every peer.Client HMAC
// timestamp and every LocalTimerAgent deadline calculation in lockstep —
// the same "one shared clock" convention internal/peer's own tests use).
func (h *threeDaemonHarness) advance(d time.Duration) {
	h.nowMu.Lock()
	defer h.nowMu.Unlock()
	h.now = h.now.Add(d)
}

// fireDeadline fires node's own fakeTimers (the most recently armed,
// not-yet-cancelled one) — simulating that node's local rollback deadline
// elapsing, entirely independent of the coordinator.
func (h *threeDaemonHarness) fireDeadline(t *testing.T, node string) {
	t.Helper()
	if node == "pve1" {
		h.coordTimer.fireLatest(t)
		return
	}
	h.peers[node].timers.fireLatest(t)
}

// nodeTimer reads node's own node_timers row directly (bypassing the peer
// API — this is the "DB" half of AC1's "asserted via logs/DB on each"),
// whichever daemon (coordinator or peer) owns it.
func (h *threeDaemonHarness) nodeTimer(t *testing.T, changesetID, node string) store.NodeTimer {
	t.Helper()
	repo := h.coordRepo
	if node != "pve1" {
		repo = h.peers[node].repo
	}
	row, err := repo.Get(context.Background(), changesetID, node)
	if err != nil {
		t.Fatalf("NodeTimerRepo.Get(%s,%s): %v", changesetID, node, err)
	}
	return row
}

func (h *threeDaemonHarness) applyLog(t *testing.T, id string) change.ApplyLog {
	t.Helper()
	cs, err := h.svc.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	var log change.ApplyLog
	if len(cs.ApplyLog) > 0 {
		if err := json.Unmarshal(cs.ApplyLog, &log); err != nil {
			t.Fatalf("decode apply log: %v", err)
		}
	}
	return log
}

func (h *threeDaemonHarness) get(t *testing.T, id string) change.Changeset {
	t.Helper()
	cs, err := h.svc.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return cs
}

func (h *threeDaemonHarness) mustCreate(t *testing.T, ops []change.Op) change.Changeset {
	t.Helper()
	cs, err := h.svc.Create(context.Background(), "root@pam", "three-node", ops)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return cs
}

// setReloadFail flips pvemock's per-node network-reload failure injection,
// same control endpoint applyHarness.setReloadFail uses.
func (h *threeDaemonHarness) setReloadFail(t *testing.T, node string, fail bool) {
	t.Helper()
	body, _ := json.Marshal(map[string]bool{"fail": fail})
	resp, err := http.Post(h.mockURL+"/mock/nodes/"+node+"/network-reload-fail", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("setReloadFail: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setReloadFail: status %d", resp.StatusCode)
	}
}

// agentFor returns node's own fakeNodeAgent, whichever daemon owns it.
func (h *threeDaemonHarness) agentFor(node string) *fakeNodeAgent {
	if node == "pve1" {
		return h.coordAgent
	}
	return h.peers[node].agent
}

// mustReadNode reads node's current interfaces content directly from its
// owning daemon's agent (populating/confirming the fake's lazily-seeded
// "committed" state), for capturing a pre-apply baseline before Apply runs.
func mustReadNode(t *testing.T, h *threeDaemonHarness, node string) string {
	t.Helper()
	content, err := h.agentFor(node).ReadInterfaces(context.Background(), node)
	if err != nil {
		t.Fatalf("ReadInterfaces(%s): %v", node, err)
	}
	return content
}

func threeNodeOps() []change.Op {
	return []change.Op{
		bridgeCreateOp("pve1", "vmbrA", nil),
		bridgeCreateOp("pve2", "vmbrB", nil),
		bridgeCreateOp("pve3", "vmbrC", nil),
	}
}
