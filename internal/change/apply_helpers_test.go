package change_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
	"github.com/bgovanlu/vnprox/internal/store"
)

const (
	fixtureSingleNode = "../../testdata/clusters/single-node.yaml"
	fixtureThreeNode  = "../../testdata/clusters/three-node-vlan.yaml"
	fixtureOVSLab     = "../../testdata/clusters/ovs-lab.yaml"
)

// --- fake NodeAgent -------------------------------------------------------
//
// fakeNodeAgent models the host writer + reload against an in-memory
// per-node committed/staged interfaces file. Reloads are driven through a
// real *pve.Client against the pvemock server, so the documented network-
// reload failure-injection flag (per-node NetworkReloadFail) exercises the
// real user-ticket task path. Stage failures are injectable at the seam
// (pvemock has no host-write failure flag — a residual-risk item noted in
// the T-205 report).
type fakeNodeAgent struct {
	seed        pvemock.HostReader
	client      *pve.Client
	committed   map[string]string
	staged      map[string]string
	failStage   map[string]bool
	failDiscard map[string]bool
	stageCalls  int
	loadCalls   int
	mu          sync.Mutex
}

func newFakeNodeAgent(seed pvemock.HostReader, client *pve.Client) *fakeNodeAgent {
	return &fakeNodeAgent{
		seed:        seed,
		client:      client,
		committed:   map[string]string{},
		staged:      map[string]string{},
		failStage:   map[string]bool{},
		failDiscard: map[string]bool{},
	}
}

func (a *fakeNodeAgent) ReadInterfaces(ctx context.Context, node string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.committed[node]; !ok {
		content, err := a.seed.InterfacesFile(ctx, node, false)
		if err != nil {
			return "", err
		}
		a.committed[node] = content
	}
	return a.committed[node], nil
}

func (a *fakeNodeAgent) StageInterfaces(_ context.Context, node, content string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stageCalls++
	if a.failStage[node] {
		return errInjectedStage
	}
	a.staged[node] = content
	return nil
}

func (a *fakeNodeAgent) ReloadInterfaces(ctx context.Context, node string) error {
	a.mu.Lock()
	a.loadCalls++
	a.mu.Unlock()

	upid, err := a.client.ReloadNodeNetwork(ctx, node)
	if err != nil {
		return err
	}
	if _, err := a.client.WaitTask(ctx, node, upid, pve.WaitOptions{Interval: 5 * time.Millisecond, Timeout: 5 * time.Second}); err != nil {
		// Reload failed: leave the committed file untouched (contract).
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if staged, ok := a.staged[node]; ok {
		a.committed[node] = staged
		delete(a.staged, node)
	}
	return nil
}

func (a *fakeNodeAgent) DiscardStaged(_ context.Context, node string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failDiscard[node] {
		return &injectedError{"injected discard failure"}
	}
	delete(a.staged, node)
	return nil
}

func (a *fakeNodeAgent) committedFile(node string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.committed[node]
}

func (a *fakeNodeAgent) setFailStage(node string, fail bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failStage[node] = fail
}

func (a *fakeNodeAgent) setFailDiscard(node string, fail bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failDiscard[node] = fail
}

var errInjectedStage = &injectedError{"injected stage failure"}

type injectedError struct{ msg string }

func (e *injectedError) Error() string { return e.msg }

// --- fake PVEGateway ------------------------------------------------------

type fakePVEGateway struct {
	client   *pve.Client
	pollNode string
	fail     bool
}

func (g *fakePVEGateway) ApplySDN(ctx context.Context) error {
	if g.fail {
		return &injectedError{"injected sdn.apply failure"}
	}
	upid, err := g.client.ApplySDN(ctx)
	if err != nil {
		return err
	}
	_, err = g.client.WaitTask(ctx, g.pollNode, upid, pve.WaitOptions{Interval: 5 * time.Millisecond, Timeout: 5 * time.Second})
	return err
}

// --- fake timer -----------------------------------------------------------

type fakeTimer struct {
	fn      func()
	parent  *fakeTimers
	stopped bool
}

func (t *fakeTimer) Stop() bool {
	t.parent.mu.Lock()
	defer t.parent.mu.Unlock()
	was := !t.stopped
	t.stopped = true
	return was
}

type fakeTimers struct {
	timers []*fakeTimer
	mu     sync.Mutex
}

func (ft *fakeTimers) New(_ time.Duration, f func()) change.Stopper {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	t := &fakeTimer{fn: f, parent: ft}
	ft.timers = append(ft.timers, t)
	return t
}

// armedCount returns how many timers are currently armed (not stopped).
func (ft *fakeTimers) armedCount() int {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	n := 0
	for _, t := range ft.timers {
		if !t.stopped {
			n++
		}
	}
	return n
}

// fireLatest invokes the most recently armed, not-yet-stopped timer's
// callback synchronously, simulating the deadline elapsing.
func (ft *fakeTimers) fireLatest(t *testing.T) {
	t.Helper()
	ft.mu.Lock()
	var target *fakeTimer
	for i := len(ft.timers) - 1; i >= 0; i-- {
		if !ft.timers[i].stopped {
			target = ft.timers[i]
			break
		}
	}
	if target != nil {
		target.stopped = true
	}
	ft.mu.Unlock()
	if target == nil {
		t.Fatal("fireLatest: no armed timer")
	}
	target.fn()
}

// --- fake Broadcaster -----------------------------------------------------

type statusEvent struct {
	ConfirmDeadline *int64 `json:"confirmDeadline,omitempty"`
	Event           string `json:"event"`
	ID              string `json:"id"`
	Status          string `json:"status"`
}

type fakeBroadcaster struct {
	events []statusEvent
	mu     sync.Mutex
}

func (b *fakeBroadcaster) Broadcast(_ string, payload []byte) {
	var e statusEvent
	if err := json.Unmarshal(payload, &e); err != nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, e)
}

func (b *fakeBroadcaster) statuses(id string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for _, e := range b.events {
		if e.ID == id {
			out = append(out, e.Status)
		}
	}
	return out
}

// --- fake Refresher -------------------------------------------------------

type fakeRefresher struct {
	calls []inventory.Scope
	mu    sync.Mutex
}

func (r *fakeRefresher) RefreshNow(_ context.Context, scope inventory.Scope) (inventory.Delta, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, scope)
	return inventory.Delta{}, nil
}

func (r *fakeRefresher) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// --- static inventory source ------------------------------------------

// staticInventorySource is a change.InventorySource over a fixed snapshot,
// for tests whose ops reference an entity (e.g. an OVS bond's slave
// physnic) that only ever comes from the snapshot — the v1 op vocabulary
// has no "physnic.create" (physnics are hardware, never op-created) — so
// newHarness's default nil Inventory (an always-empty snapshot) isn't
// enough (see newHarness's doc comment).
type staticInventorySource struct{ snap inventory.Snapshot }

func (s staticInventorySource) Snapshot() inventory.Snapshot { return s.snap }

// withInventory is a newHarness opt that seeds entities (a minimal set,
// not a full fixture replay) into a fresh graph and wires it as the
// service's InventorySource.
func withInventory(entities ...inventory.Entity) func(*change.Config) {
	g := inventory.NewGraph()
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{}, entities)
	snap := g.Snapshot()
	return func(cfg *change.Config) {
		cfg.Inventory = staticInventorySource{snap: snap}
	}
}

// --- harness --------------------------------------------------------------

type applyHarness struct {
	svc       *change.Service
	db        *store.DB
	csRepo    *store.ChangesetRepo
	auditRepo *store.AuditRepo
	snapRepo  *store.SnapshotRepo
	blobRepo  *store.BlobRepo
	server    *httptest.Server
	client    *pve.Client
	agent     *fakeNodeAgent
	timers    *fakeTimers
	ws        *fakeBroadcaster
	refresher *fakeRefresher
}

// newHarness wires a full apply-capable Service against a fresh SQLite DB and
// a pvemock server for the given fixture, with the fake TimerFunc so the
// commit-confirm deadline can be fired deterministically. opts (T-407) let a
// caller override Config fields the base harness leaves zero — most tests in
// this package never reference a pre-existing snapshot entity by name (their
// ops only create fresh ones), so Inventory has always been left nil
// (inventorySnapshot() then reads an empty graph); a test whose ops *do*
// reference an existing entity (e.g. an OVS bond's slave physnic) needs
// change.Config{Inventory: ...} set, hence this seam.
func newHarness(t *testing.T, fixturePath string, opts ...func(*change.Config)) *applyHarness {
	t.Helper()
	f, err := pvemock.LoadFixture(fixturePath)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	srv := pvemock.NewServer(f)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	client, err := pve.New(pve.Config{APIURL: ts.URL, Auth: pve.AuthTicket, Username: "root@pam", Password: "vnprox-mock"})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}

	db := openTestDB(t)
	csRepo := store.NewChangesetRepo(db)
	auditRepo := store.NewAuditRepo(db)
	snapRepo := store.NewSnapshotRepo(db)
	blobRepo := store.NewBlobRepo(db)

	agent := newFakeNodeAgent(pvemock.NewFixtureHostReader(srv), client)
	timers := &fakeTimers{}
	ws := &fakeBroadcaster{}
	refresher := &fakeRefresher{}

	protectedPath := filepath.Join(t.TempDir(), "protected.json")
	cfg := change.Config{
		Changesets: csRepo, Audit: auditRepo, WS: ws,
		Nodes: agent, Snapshots: snapRepo, Blobs: blobRepo, Refresher: refresher,
		TimerFunc: timers.New, ProtectedPath: protectedPath,
	}
	for _, o := range opts {
		o(&cfg)
	}
	svc := newService(t, cfg)

	return &applyHarness{
		svc: svc, db: db, csRepo: csRepo, auditRepo: auditRepo, snapRepo: snapRepo, blobRepo: blobRepo,
		server: ts, client: client, agent: agent, timers: timers, ws: ws, refresher: refresher,
	}
}

func newService(t *testing.T, cfg change.Config) *change.Service {
	t.Helper()
	svc, err := change.NewService(cfg)
	if err != nil {
		t.Fatalf("change.NewService: %v", err)
	}
	return svc
}

func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vnprox.db")
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// setReloadFail flips the pvemock per-node network-reload failure injection
// via its documented control endpoint.
func (h *applyHarness) setReloadFail(t *testing.T, node string, fail bool) {
	t.Helper()
	body, _ := json.Marshal(map[string]bool{"fail": fail})
	resp, err := http.Post(h.server.URL+"/mock/nodes/"+node+"/network-reload-fail", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("setReloadFail: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setReloadFail: status %d", resp.StatusCode)
	}
}

// mustCreate creates a draft and fails the test on error.
func (h *applyHarness) mustCreate(t *testing.T, author, title string, ops []change.Op) change.Changeset {
	t.Helper()
	cs, err := h.svc.Create(context.Background(), author, title, ops)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return cs
}

func (h *applyHarness) get(t *testing.T, id string) change.Changeset {
	t.Helper()
	cs, err := h.svc.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return cs
}

func (h *applyHarness) applyLog(t *testing.T, id string) change.ApplyLog {
	t.Helper()
	cs := h.get(t, id)
	var log change.ApplyLog
	if len(cs.ApplyLog) > 0 {
		if err := json.Unmarshal(cs.ApplyLog, &log); err != nil {
			t.Fatalf("decode apply log: %v", err)
		}
	}
	return log
}

func (h *applyHarness) plan(t *testing.T, id string) change.Plan {
	t.Helper()
	cs := h.get(t, id)
	var p change.Plan
	if len(cs.Plan) > 0 {
		if err := json.Unmarshal(cs.Plan, &p); err != nil {
			t.Fatalf("decode plan: %v", err)
		}
	}
	return p
}

// bridgeCreateOp builds a valid bridge.create op for node.
func bridgeCreateOp(node, name string, ports []string) change.Op {
	return change.Op{
		Type:   change.OpBridgeCreate,
		Target: inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: name},
		Params: &change.BridgeCreateParams{Ports: ports, Comments: "created by T-205 test"},
	}
}

func sdnApplyOp() change.Op {
	return change.Op{Type: change.OpSdnApply, Params: &change.SdnApplyParams{}}
}

func hasKind(snaps []store.Snapshot, kind string) bool {
	for _, s := range snaps {
		if s.Kind == kind {
			return true
		}
	}
	return false
}

func hasAudit(entries []store.AuditEntry, action, username string) bool {
	for _, e := range entries {
		if e.Action == action && e.Username == username {
			return true
		}
	}
	return false
}
