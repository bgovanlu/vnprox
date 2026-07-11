package change_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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
//
// fakePVEGateway is a second, test-side implementation of change.PVEGateway
// against a real *pve.Client/pvemock server — deliberately not reusing
// cmd/vnproxd's production pveSDNGateway (an unexported type in package
// main, unreachable from this test package), the same "two independent
// implementations of one seam" precedent this type's original ApplySDN-only
// form already set. fail injects an sdn.apply task-level failure (position
// 4 of TestApply_StepFailure_AtEachPosition); T-402's post-apply zone
// health verification is exercised for real (GetSDNZoneStatus against
// pvemock, which itself derives "error" from a genuinely missing bridge —
// see internal/pvemock/sdn.go's handleSDNZoneStatus), not injected here.
type fakePVEGateway struct {
	client   *pve.Client
	pollNode string
	fail     bool
}

func (g *fakePVEGateway) SDNStageOp(ctx context.Context, op change.Op, subnetVnet string) error {
	switch p := op.Params.(type) {
	case *change.SdnZoneCreateParams:
		return g.client.CreateSDNZone(ctx, pve.SDNZone{
			ID: op.Target.ID, Type: p.Type, Bridge: p.Bridge, Controller: p.Controller,
			Nodes: p.Nodes, VrfVxlan: p.VrfVxlan, MTU: p.MTU,
		})
	case *change.SdnZoneUpdateParams:
		z := pve.SDNZone{ID: op.Target.ID}
		if p.Bridge != nil {
			z.Bridge = *p.Bridge
		}
		if p.Controller != nil {
			z.Controller = *p.Controller
		}
		if p.Nodes != nil {
			z.Nodes = *p.Nodes
		}
		if p.VrfVxlan != nil {
			z.VrfVxlan = *p.VrfVxlan
		}
		if p.MTU != nil {
			z.MTU = *p.MTU
		}
		return g.client.UpdateSDNZone(ctx, op.Target.ID, z)
	case *change.SdnZoneDeleteParams:
		return g.client.DeleteSDNZone(ctx, op.Target.ID)
	case *change.SdnVnetCreateParams:
		return g.client.CreateSDNVnet(ctx, pve.SDNVnet{
			ID: pve.SDNVnetID(op.Target.ID), Zone: p.Zone, Alias: p.Alias, Tag: p.Tag, VlanAware: p.VlanAware,
		})
	case *change.SdnVnetUpdateParams:
		id := pve.SDNVnetID(op.Target.ID)
		v := pve.SDNVnet{ID: id}
		if p.Alias != nil {
			v.Alias = *p.Alias
		}
		if p.Tag != nil {
			v.Tag = *p.Tag
		}
		if p.VlanAware != nil {
			v.VlanAware = *p.VlanAware
		}
		return g.client.UpdateSDNVnet(ctx, id, v)
	case *change.SdnVnetDeleteParams:
		return g.client.DeleteSDNVnet(ctx, pve.SDNVnetID(op.Target.ID))
	case *change.SdnSubnetCreateParams:
		start, end := firstDHCPRange(p.DHCPRanges)
		return g.client.CreateSDNSubnet(ctx, pve.SDNVnetID(p.Vnet), pve.SDNSubnet{
			ID: pve.SDNSubnetID(op.Target.ID), Vnet: pve.SDNVnetID(p.Vnet), CIDR: p.CIDR, Gateway: p.Gateway,
			DHCPRangeStart: start, DHCPRangeEnd: end, SNAT: p.SNAT,
		})
	case *change.SdnSubnetUpdateParams:
		vnet := pve.SDNVnetID(subnetVnet)
		s := pve.SDNSubnet{ID: pve.SDNSubnetID(op.Target.ID), Vnet: vnet, CIDR: op.Target.ID}
		if p.Gateway != nil {
			s.Gateway = *p.Gateway
		}
		if p.DHCPRanges != nil {
			s.DHCPRangeStart, s.DHCPRangeEnd = firstDHCPRange(*p.DHCPRanges)
		}
		if p.SNAT != nil {
			s.SNAT = *p.SNAT
		}
		return g.client.UpdateSDNSubnet(ctx, vnet, pve.SDNSubnetID(op.Target.ID), s)
	case *change.SdnSubnetDeleteParams:
		return g.client.DeleteSDNSubnet(ctx, pve.SDNVnetID(subnetVnet), pve.SDNSubnetID(op.Target.ID))
	default:
		return &injectedError{"fakePVEGateway: unsupported sdn stage op " + string(op.Type)}
	}
}

func firstDHCPRange(ranges []string) (start, end string) {
	if len(ranges) == 0 {
		return "", ""
	}
	parts := strings.SplitN(ranges[0], "-", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

func (g *fakePVEGateway) ApplySDN(ctx context.Context, affectedZones []string) (change.SDNApplyResult, error) {
	if g.fail {
		return change.SDNApplyResult{}, &injectedError{"injected sdn.apply failure"}
	}
	upid, err := g.client.ApplySDN(ctx)
	if err != nil {
		return change.SDNApplyResult{}, err
	}
	result := change.SDNApplyResult{UPID: upid, Node: g.pollNode}
	if _, err := g.client.WaitTask(ctx, g.pollNode, upid, pve.WaitOptions{Interval: 5 * time.Millisecond, Timeout: 5 * time.Second}); err != nil {
		return result, err
	}
	for _, zoneID := range affectedZones {
		statuses, err := g.client.GetSDNZoneStatus(ctx, zoneID)
		if err != nil {
			return result, err
		}
		zh := change.SDNZoneHealth{Zone: zoneID}
		for _, st := range statuses {
			zh.Nodes = append(zh.Nodes, change.SDNNodeHealth{Node: st.Node, Status: st.Status, Detail: st.Detail})
		}
		result.Zones = append(result.Zones, zh)
	}
	return result, nil
}

func (g *fakePVEGateway) SDNConfig(ctx context.Context) (change.SDNConfig, error) {
	zones, err := g.client.ListSDNZones(ctx)
	if err != nil {
		return change.SDNConfig{}, err
	}
	vnets, err := g.client.ListSDNVnets(ctx)
	if err != nil {
		return change.SDNConfig{}, err
	}
	var cfg change.SDNConfig
	for _, z := range zones {
		cfg.Zones = append(cfg.Zones, change.SDNZoneConfig{
			ID: z.ID, Type: z.Type, Bridge: z.Bridge, Controller: z.Controller,
			Nodes: z.Nodes, VrfVxlan: z.VrfVxlan, MTU: z.MTU,
		})
	}
	for _, v := range vnets {
		refID := v.Zone + "/" + v.ID
		cfg.Vnets = append(cfg.Vnets, change.SDNVnetConfig{
			ID: refID, Zone: v.Zone, Alias: v.Alias, Tag: v.Tag, VlanAware: v.VlanAware,
		})
		subnets, err := g.client.ListSDNSubnets(ctx, v.ID)
		if err != nil {
			return change.SDNConfig{}, err
		}
		for _, s := range subnets {
			var ranges []string
			if s.DHCPRangeStart != "" && s.DHCPRangeEnd != "" {
				ranges = []string{s.DHCPRangeStart + "-" + s.DHCPRangeEnd}
			}
			cfg.Subnets = append(cfg.Subnets, change.SDNSubnetConfig{
				ID: s.CIDR, Vnet: refID, Gateway: s.Gateway, DHCPRanges: ranges, SNAT: s.SNAT,
			})
		}
	}
	return cfg, nil
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
// commit-confirm deadline can be fired deterministically.
func newHarness(t *testing.T, fixturePath string) *applyHarness {
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
	svc := newService(t, change.Config{
		Changesets: csRepo, Audit: auditRepo, WS: ws,
		Nodes: agent, Snapshots: snapRepo, Blobs: blobRepo, Refresher: refresher,
		TimerFunc: timers.New, ProtectedPath: protectedPath,
	})

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

// setSDNZoneStatusFail flips pvemock's per-node SDN zone status failure
// injection (T-402) via its documented control endpoint — models a node
// whose SDN apply task succeeded but which nonetheless failed to realize
// the config (see MockOptions.SDNZoneStatusFail's doc comment).
func (h *applyHarness) setSDNZoneStatusFail(t *testing.T, node string, fail bool) {
	t.Helper()
	body, _ := json.Marshal(map[string]bool{"fail": fail})
	resp, err := http.Post(h.server.URL+"/mock/nodes/"+node+"/sdn-status-fail", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("setSDNZoneStatusFail: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setSDNZoneStatusFail: status %d", resp.StatusCode)
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
