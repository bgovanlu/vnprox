package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/ipam"
	"github.com/bgovanlu/vnprox/internal/store"
	"github.com/bgovanlu/vnprox/internal/tenant"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// ---- test doubles -------------------------------------------------------

type fakeIPAMService struct{ subnets []ipam.Subnet }

func (f fakeIPAMService) Subnets(context.Context) (ipam.SubnetsResponse, error) {
	return ipam.SubnetsResponse{Items: f.subnets, GeneratedAt: 1}, nil
}
func (f fakeIPAMService) Allocations(context.Context, string) (ipam.AllocationList, error) {
	return ipam.AllocationList{}, nil
}
func (f fakeIPAMService) AllocationsCSV(context.Context, string) ([]byte, error) {
	return []byte{}, nil
}
func (f fakeIPAMService) V6Plan(context.Context, string) (ipam.V6PlanResponse, error) {
	return ipam.V6PlanResponse{}, nil
}

type fakeApprovalNotifier struct{ notices []ApprovalNotice }

func (f *fakeApprovalNotifier) NotifyApprovalPending(_ context.Context, notice ApprovalNotice) error {
	f.notices = append(f.notices, notice)
	return nil
}

// tenantEnv wires a real store + tenant service + change engine, plus fakes for
// the read surfaces, so tenant-scoping is proven end to end through the router.
type tenantEnv struct {
	repo      *store.TenantRepo
	tenantSvc *tenant.Service
	changeSvc *change.Service
	notifier  *fakeApprovalNotifier
	flows     *fakeFlowLocalSource
	findings  fakeFindingsService
	ipam      fakeIPAMService
	topo      fakeTopologyService
}

func newTenantEnv(t *testing.T) *tenantEnv {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "vnprox.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	changeSvc, err := change.NewService(change.Config{
		Changesets: store.NewChangesetRepo(db),
		Audit:      store.NewAuditRepo(db),
		Now:        func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	if err != nil {
		t.Fatalf("change.NewService: %v", err)
	}
	repo := store.NewTenantRepo(db)
	// No expander: the tenant's visible set is exactly its stored scope refs,
	// so the test controls both sides of the filter deterministically.
	tenantSvc, err := tenant.NewService(tenant.Config{Store: repo, Now: func() int64 { return 1000 }})
	if err != nil {
		t.Fatalf("tenant.NewService: %v", err)
	}

	return &tenantEnv{
		repo: repo, tenantSvc: tenantSvc, changeSvc: changeSvc,
		notifier: &fakeApprovalNotifier{},
		topo: fakeTopologyService{nodes: []topology.Node{
			{ID: "guest:pve1:100", Kind: "guest", Label: "t1-guest"},
			{ID: "sdn-subnet::10.0.0.0/24", Kind: "sdn-subnet", Label: "t1-subnet"},
			{ID: "guest:pve2:200", Kind: "guest", Label: "t2-guest"},
			{ID: "bridge:pve1:vmbr0", Kind: "bridge", Label: "shared-bridge"},
		}, searchHit: []topology.SearchResult{
			{Ref: "guest:pve1:100", Label: "t1-guest"},
			{Ref: "guest:pve2:200", Label: "t2-guest"},
		}},
		findings: fakeFindingsService{findings: []findings.Finding{
			{ID: "f1", Source: "drift", Severity: "warning", Refs: []string{"guest:pve1:100"}, Nodes: []string{"pve1"}},
			{ID: "f2", Source: "drift", Severity: "warning", Refs: []string{"guest:pve2:200"}, Nodes: []string{"pve2"}},
			{ID: "f3", Source: "health", Severity: "error", Nodes: []string{"pve1"}}, // cluster/node-wide, no refs
		}},
		ipam: fakeIPAMService{subnets: []ipam.Subnet{
			{CIDR: "10.0.0.0/24", Source: "sdn"},
			{CIDR: "10.9.9.0/24", Source: "sdn"},
		}},
		flows: &fakeFlowLocalSource{samples: []store.FlowSample{
			{Node: "pve1", SrcIP: "10.0.0.5", DstIP: "10.0.0.6", SrcRef: "guest:pve1:100", DstRef: "sdn-subnet::10.0.0.0/24", Source: "conntrack"},
			{Node: "pve2", SrcIP: "10.9.9.5", DstIP: "10.9.9.6", SrcRef: "guest:pve2:200", DstRef: "guest:pve2:201", Source: "conntrack"},
		}},
	}
}

// router builds a router acting as username (a fresh Options each time so the
// acting identity varies while every backing service is shared).
func (e *tenantEnv) router(username string) http.Handler {
	auth := fullCapsAuth(username)
	// IPAM reads are gated on sdnRead (capIPAMRead); grant the read caps the
	// scoped read routes need so scoping — not capability — is what these
	// tests exercise.
	auth.caps[capSDNRead] = true
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:           auth,
		Topology:       e.topo,
		Findings:       e.findings,
		IPAM:           e.ipam,
		Flows:          e.flows,
		Changesets:     e.changeSvc,
		Tenant:         e.tenantSvc,
		TenantStore:    e.repo,
		TenantNotifier: e.notifier,
	})
}

func (e *tenantEnv) seedTenant(t *testing.T, id string, members map[string]string, refs ...string) {
	t.Helper()
	ctx := context.Background()
	if err := e.repo.InsertTenant(ctx, store.Tenant{ID: id, Name: id, CreatedBy: "admin@pve", CreatedAt: 1}); err != nil {
		t.Fatalf("InsertTenant: %v", err)
	}
	for identity, role := range members {
		if err := e.repo.PutMember(ctx, store.TenantMember{TenantID: id, Identity: identity, Role: role}); err != nil {
			t.Fatalf("PutMember: %v", err)
		}
	}
	for _, ref := range refs {
		if err := e.repo.AddScope(ctx, id, ref); err != nil {
			t.Fatalf("AddScope: %v", err)
		}
	}
}

func getJSON(t *testing.T, r http.Handler, user, path string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var body map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
	}
	return rec.Code, body
}

// ---- AC1: a tenant sees only its own scope --------------------------------

func TestTenantScoping_ReadRoutesFilterToScope(t *testing.T) {
	env := newTenantEnv(t)
	env.seedTenant(t, "t1", map[string]string{"alice@pve": store.TenantRoleMember},
		"guest:pve1:100", "sdn-subnet::10.0.0.0/24")
	r := env.router("alice@pve")

	// Topology: only t1's guest + subnet nodes; the one edge-eligible pair.
	code, body := getJSON(t, r, "alice@pve", "/api/v1/topology")
	if code != 200 {
		t.Fatalf("topology status %d", code)
	}
	nodes, _ := body["nodes"].([]any)
	if len(nodes) != 2 {
		t.Errorf("topology nodes = %d, want 2 (t1 guest+subnet only): %v", len(nodes), body["nodes"])
	}
	for _, n := range nodes {
		id, _ := n.(map[string]any)["id"].(string)
		if id == "guest:pve2:200" || id == "bridge:pve1:vmbr0" {
			t.Errorf("LEAK: topology returned out-of-scope node %q", id)
		}
	}

	// Findings: only f1 (references t1's guest); f2 (t2) and f3 (cluster-wide) hidden.
	_, body = getJSON(t, r, "alice@pve", "/api/v1/findings")
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Errorf("findings = %d, want 1 (f1 only): %v", len(items), body["items"])
	}

	// IPAM subnets: only 10.0.0.0/24.
	_, body = getJSON(t, r, "alice@pve", "/api/v1/ipam/subnets")
	subs, _ := body["items"].([]any)
	if len(subs) != 1 {
		t.Errorf("ipam subnets = %d, want 1: %v", len(subs), body["items"])
	}

	// Flows: only the t1 flow.
	_, body = getJSON(t, r, "alice@pve", "/api/v1/flows")
	fl, _ := body["items"].([]any)
	if len(fl) != 1 {
		t.Errorf("flows = %d, want 1: %v", len(fl), body["items"])
	}

	// Search: only the visible result.
	_, body = getJSON(t, r, "alice@pve", "/api/v1/inventory/search?q=guest")
	res, _ := body["results"].([]any)
	if len(res) != 1 {
		t.Errorf("search results = %d, want 1: %v", len(res), body["results"])
	}

	// Direct ref lookup: out-of-scope 404 (not 403), never confirming existence.
	if code, _ = getJSON(t, r, "alice@pve", "/api/v1/inventory/guest:pve2:200"); code != 404 {
		t.Errorf("out-of-scope inventory lookup = %d, want 404", code)
	}
}

// An unscoped (non-tenant) caller reads everything — multi-tenancy only
// narrows a member's view, never widens or breaks an ordinary operator's.
func TestTenantScoping_NonMemberUnscoped(t *testing.T) {
	env := newTenantEnv(t)
	env.seedTenant(t, "t1", map[string]string{"alice@pve": store.TenantRoleMember}, "guest:pve1:100")
	r := env.router("operator@pve") // not a tenant member

	_, body := getJSON(t, r, "operator@pve", "/api/v1/topology")
	nodes, _ := body["nodes"].([]any)
	if len(nodes) != 4 {
		t.Errorf("unscoped operator topology nodes = %d, want 4 (all)", len(nodes))
	}
	_, body = getJSON(t, r, "operator@pve", "/api/v1/findings")
	items, _ := body["items"].([]any)
	if len(items) != 3 {
		t.Errorf("unscoped operator findings = %d, want 3 (all)", len(items))
	}
}

// ---- AC4: two tenants, zero cross-tenant leakage --------------------------

// TestTenantScoping_NoCrossTenantLeakage now additionally walks the
// /tenants* admin read routes (GET /tenants, GET /tenants/{id}) alongside
// /topology and /flows (T-3002-followup-01, 2026-08-19): the version of
// this test that existed before the fix exercised only /topology and
// /flows and never called /tenants at all, which is exactly why the leak —
// any tenant member could enumerate every tenant and read another tenant's
// scopes/members via plain netRead — went unnoticed. Extending the same
// loop, rather than a separate one-shot test, is deliberate: it is the
// enumeration this docstring's predecessor was missing, so the next GET
// route added to the /tenants* family gets the same randomized-order
// coverage automatically instead of silently repeating the miss.
//
// It now ALSO walks the mutating routes — POST/DELETE /tenants, PUT/DELETE
// .../scopes, PUT/DELETE .../members (T-3002-followup-02, 2026-08-19). Those
// were deliberately left out by the read-only fix above (T-3002-followup-01
// covered only reads), and doing so left a WORSE gap open: a caller holding
// netWrite and membership in one tenant could mutate ANY tenant's
// scopes/members, not just their own — rewriting another tenant's boundary,
// not merely reading it. This block proves bob (member of t2 only) can never
// mutate t1 via any of the five routes below, and symmetrically for alice
// against t2; every attempt is 404, never 403, for the same "existence not
// confirmed" reason the read side already established. Before
// T-3002-followup-02, every one of these calls succeeded (200/204) instead.
func TestTenantScoping_NoCrossTenantLeakage(t *testing.T) {
	env := newTenantEnv(t)
	env.seedTenant(t, "t1", map[string]string{"alice@pve": store.TenantRoleMember},
		"guest:pve1:100", "sdn-subnet::10.0.0.0/24")
	env.seedTenant(t, "t2", map[string]string{"bob@pve": store.TenantRoleMember},
		"guest:pve2:200")

	aliceR := env.router("alice@pve")
	bobR := env.router("bob@pve")

	// Repeated randomized-order reads: alice never sees t2, bob never sees t1.
	for i := 0; i < 20; i++ {
		_, ab := getJSON(t, aliceR, "alice@pve", "/api/v1/topology")
		for _, n := range asNodes(ab) {
			if n == "guest:pve2:200" {
				t.Fatalf("LEAK: alice saw t2's guest")
			}
		}
		_, bb := getJSON(t, bobR, "bob@pve", "/api/v1/topology")
		for _, n := range asNodes(bb) {
			if n == "guest:pve1:100" || n == "sdn-subnet::10.0.0.0/24" {
				t.Fatalf("LEAK: bob saw t1's resources")
			}
		}
		// bob's flows must never contain t1 refs.
		_, bf := getJSON(t, bobR, "bob@pve", "/api/v1/flows")
		fl, _ := bf["items"].([]any)
		for _, it := range fl {
			m, _ := it.(map[string]any)
			if m["srcRef"] == "guest:pve1:100" || m["dstRef"] == "sdn-subnet::10.0.0.0/24" {
				t.Fatalf("LEAK: bob saw a t1 flow")
			}
		}

		// The whole /tenants* read family (T-3002-followup-01): alice never
		// sees t2's tenant row (list or direct get), bob never sees t1's.
		_, at := getJSON(t, aliceR, "alice@pve", "/api/v1/tenants")
		if items, _ := at["items"].([]any); tenantListContainsID(items, "t2") {
			t.Fatalf("LEAK: alice's GET /tenants listed t2")
		}
		if code, body := getJSON(t, aliceR, "alice@pve", "/api/v1/tenants/t2"); code != http.StatusNotFound {
			t.Fatalf("LEAK: alice's GET /tenants/t2 = %d (want 404): %v", code, body)
		}

		_, bt := getJSON(t, bobR, "bob@pve", "/api/v1/tenants")
		if items, _ := bt["items"].([]any); tenantListContainsID(items, "t1") {
			t.Fatalf("LEAK: bob's GET /tenants listed t1")
		}
		if code, body := getJSON(t, bobR, "bob@pve", "/api/v1/tenants/t1"); code != http.StatusNotFound {
			t.Fatalf("LEAK: bob's GET /tenants/t1 = %d (want 404): %v", code, body)
		}

		// The mutating /tenants* family (T-3002-followup-02): bob, a member
		// only of t2, must never be able to mutate t1 via any of these five
		// routes — every attempt is 404, and (because each is refused before
		// touching the store) none of them actually change t1's state, so
		// the loop can safely repeat this 20 times without the fixture
		// drifting.
		for _, req := range []struct {
			method, path, body string
		}{
			{http.MethodPut, "/api/v1/tenants/t1/scopes", `{"scopeRef":"guest:pve9:999"}`},
			{http.MethodDelete, "/api/v1/tenants/t1/scopes?scopeRef=guest:pve1:100", ""},
			{http.MethodPut, "/api/v1/tenants/t1/members", `{"identity":"mallory@pve","role":"approver"}`},
			{http.MethodDelete, "/api/v1/tenants/t1/members/alice@pve", ""},
			{http.MethodDelete, "/api/v1/tenants/t1", ""},
		} {
			if code := doReq(t, bobR, req.method, req.path, req.body).Code; code != http.StatusNotFound {
				t.Fatalf("LEAK: bob's %s %s = %d (want 404)", req.method, req.path, code)
			}
		}
		// ...and symmetrically, alice (member only of t1) against t2.
		for _, req := range []struct {
			method, path, body string
		}{
			{http.MethodPut, "/api/v1/tenants/t2/scopes", `{"scopeRef":"guest:pve9:999"}`},
			{http.MethodDelete, "/api/v1/tenants/t2/scopes?scopeRef=guest:pve2:200", ""},
			{http.MethodPut, "/api/v1/tenants/t2/members", `{"identity":"mallory@pve","role":"approver"}`},
			{http.MethodDelete, "/api/v1/tenants/t2/members/bob@pve", ""},
			{http.MethodDelete, "/api/v1/tenants/t2", ""},
		} {
			if code := doReq(t, aliceR, req.method, req.path, req.body).Code; code != http.StatusNotFound {
				t.Fatalf("LEAK: alice's %s %s = %d (want 404)", req.method, req.path, code)
			}
		}
	}
}

func tenantListContainsID(items []any, id string) bool {
	for _, it := range items {
		m, _ := it.(map[string]any)
		if m["id"] == id {
			return true
		}
	}
	return false
}

func asNodes(body map[string]any) []string {
	var out []string
	nodes, _ := body["nodes"].([]any)
	for _, n := range nodes {
		if id, ok := n.(map[string]any)["id"].(string); ok {
			out = append(out, id)
		}
	}
	return out
}

// ---- AC2/AC3: request-changeset creation, notification, approval ----------

func post(t *testing.T, r http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// doReq issues an arbitrary-method request, JSON body optional (empty string
// omits it — used for DELETE/PUT-with-no-body calls the plain post() helper
// above can't express since it's hardcoded to POST).
func doReq(t *testing.T, r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var bodyReader *bytes.Buffer
	if body == "" {
		bodyReader = bytes.NewBuffer(nil)
	} else {
		bodyReader = bytes.NewBufferString(body)
	}
	req := httptest.NewRequest(method, path, bodyReader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestRequestChangeset_CreateNotifyApproveFlow(t *testing.T) {
	env := newTenantEnv(t)
	env.seedTenant(t, "t1",
		map[string]string{"alice@pve": store.TenantRoleMember, "boss@pve": store.TenantRoleApprover},
		"bridge:pve1:vmbr5")

	// alice raises a request-changeset touching her in-scope bridge.
	aliceR := env.router("alice@pve")
	rec := post(t, aliceR, "/api/v1/changesets",
		`{"tenantId":"t1","title":"add vmbr5","ops":[{"op":"bridge.create","target":"bridge:pve1:vmbr5","params":{"mtu":1500}}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create request status = %d, body %s", rec.Code, rec.Body.String())
	}
	var created changesetResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Status != "requested" {
		t.Fatalf("status = %q, want requested", created.Status)
	}

	// AC3: the approver was notified via the routed notifier.
	if len(env.notifier.notices) != 1 {
		t.Fatalf("notices = %d, want 1", len(env.notifier.notices))
	}
	notice := env.notifier.notices[0]
	if notice.ChangesetID != created.ID || len(notice.Approvers) != 1 || notice.Approvers[0] != "boss@pve" {
		t.Errorf("notice = %+v", notice)
	}

	// AC2: a member cannot approve their own tenant's request.
	rec = post(t, aliceR, "/api/v1/changesets/"+created.ID+"/approve", "")
	if rec.Code != http.StatusForbidden {
		t.Errorf("member approve status = %d, want 403", rec.Code)
	}

	// AC2/AC3: an approver can, and it becomes an ordinary draft.
	bossR := env.router("boss@pve")
	rec = post(t, bossR, "/api/v1/changesets/"+created.ID+"/approve", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("approver approve status = %d, body %s", rec.Code, rec.Body.String())
	}
	var approved changesetResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &approved)
	if approved.Status != "draft" {
		t.Errorf("post-approve status = %q, want draft", approved.Status)
	}
}

// A tenant may only request changes to resources within its own scope.
func TestRequestChangeset_RejectsOutOfScopeOp(t *testing.T) {
	env := newTenantEnv(t)
	env.seedTenant(t, "t1", map[string]string{"alice@pve": store.TenantRoleMember}, "bridge:pve1:vmbr5")
	r := env.router("alice@pve")

	rec := post(t, r, "/api/v1/changesets",
		`{"tenantId":"t1","title":"sneaky","ops":[{"op":"bridge.create","target":"bridge:pve1:vmbr9","params":{"mtu":1500}}]}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("out-of-scope request status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
}

// A non-member naming a tenant they don't belong to gets 404 (never confirming
// the tenant exists), not 403.
func TestRequestChangeset_NonMemberTenantIs404(t *testing.T) {
	env := newTenantEnv(t)
	env.seedTenant(t, "t1", map[string]string{"alice@pve": store.TenantRoleMember}, "bridge:pve1:vmbr5")
	r := env.router("stranger@pve")

	rec := post(t, r, "/api/v1/changesets",
		`{"tenantId":"t1","title":"x","ops":[]}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("non-member request status = %d, want 404", rec.Code)
	}
}

// Approving a nonexistent / non-request changeset is 404, never a 403 that
// would confirm its existence.
func TestApprove_NonRequestChangesetIs404(t *testing.T) {
	env := newTenantEnv(t)
	env.seedTenant(t, "t1", map[string]string{"boss@pve": store.TenantRoleApprover}, "bridge:pve1:vmbr5")
	r := env.router("boss@pve")
	rec := post(t, r, "/api/v1/changesets/nonexistent/approve", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("approve nonexistent status = %d, want 404", rec.Code)
	}
}

// ---- WS fail-closed guard (cross-tenant leak prevention) ------------------

// TestTenantScoping_WSUpgradeDeniedForTenant proves the /api/ws topology-delta
// feed is refused for a tenant-scoped principal (the feed is cluster-wide and
// not yet per-subscriber filtered, so an unscoped upgrade would leak
// cross-tenant deltas) while a non-tenant/admin principal still gets it.
func TestTenantScoping_WSUpgradeDeniedForTenant(t *testing.T) {
	env := newTenantEnv(t)
	env.seedTenant(t, "t1", map[string]string{"alice@pve": store.TenantRoleMember}, "guest:pve1:100")

	// A tenant member is refused the WS upgrade with 403.
	req := httptest.NewRequest(http.MethodGet, "/api/ws", nil)
	rec := httptest.NewRecorder()
	env.router("alice@pve").ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("tenant WS upgrade = %d, want 403 (fail-closed): %s", rec.Code, rec.Body.String())
	}

	// A non-tenant operator still gets the feed (fake ServeWS returns 200).
	req = httptest.NewRequest(http.MethodGet, "/api/ws", nil)
	rec = httptest.NewRecorder()
	env.router("operator@pve").ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("non-tenant WS upgrade = %d, want 200 (unaffected)", rec.Code)
	}
}

// ---- AC6: scoped dashboard ------------------------------------------------

func TestTenantDashboard_ScopedCounts(t *testing.T) {
	env := newTenantEnv(t)
	env.seedTenant(t, "t1", map[string]string{"alice@pve": store.TenantRoleMember},
		"guest:pve1:100", "sdn-subnet::10.0.0.0/24")
	r := env.router("alice@pve")

	code, body := getJSON(t, r, "alice@pve", "/api/v1/dashboard?tenantId=t1")
	if code != 200 {
		t.Fatalf("dashboard status %d body %v", code, body)
	}
	if g, _ := body["guests"].(float64); g != 1 {
		t.Errorf("guests = %v, want 1", body["guests"])
	}
	if s, _ := body["subnets"].(float64); s != 1 {
		t.Errorf("subnets = %v, want 1", body["subnets"])
	}

	// Asking for a tenant you don't belong to is 404.
	code, _ = getJSON(t, r, "alice@pve", "/api/v1/dashboard?tenantId=t2")
	if code != 404 {
		t.Errorf("dashboard for foreign tenant = %d, want 404", code)
	}
}

// ---- tenant admin CRUD ----------------------------------------------------

func TestTenantAdmin_CRUD(t *testing.T) {
	env := newTenantEnv(t)
	r := env.router("admin@pve")

	rec := post(t, r, "/api/v1/tenants", `{"id":"t9","name":"Team Nine"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create tenant = %d body %s", rec.Code, rec.Body.String())
	}

	// Add scope + member.
	req := httptest.NewRequest(http.MethodPut, "/api/v1/tenants/t9/scopes", bytes.NewBufferString(`{"scopeRef":"guest:pve1:100"}`))
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("add scope = %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodPut, "/api/v1/tenants/t9/members", bytes.NewBufferString(`{"identity":"u@pve","role":"member"}`))
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("add member = %d", rec.Code)
	}

	code, body := getJSON(t, r, "admin@pve", "/api/v1/tenants/t9")
	if code != 200 {
		t.Fatalf("get tenant = %d", code)
	}
	if scopes, _ := body["scopes"].([]any); len(scopes) != 1 {
		t.Errorf("scopes = %v", body["scopes"])
	}
	if members, _ := body["members"].([]any); len(members) != 1 {
		t.Errorf("members = %v", body["members"])
	}
}

// ---- T-3002-followup-02: mutation authorization ----------------------------

// TestTenantCreate_ScopedCallerForbidden pins the POST /tenants rule this
// task added: tenant creation has no existing tenant for membership to gate
// against, so it is refused outright for any tenant-scoped caller (403) and
// left to an unscoped (fleet-admin) netWrite holder. Before this fix, alice
// — a member of t1 only — could create tenants freely.
func TestTenantCreate_ScopedCallerForbidden(t *testing.T) {
	env := newTenantEnv(t)
	env.seedTenant(t, "t1", map[string]string{"alice@pve": store.TenantRoleMember}, "guest:pve1:100")

	rec := post(t, env.router("alice@pve"), "/api/v1/tenants", `{"name":"sneaky"}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("tenant member's POST /tenants = %d, want 403: %s", rec.Code, rec.Body.String())
	}

	// An unscoped caller (not a member of any tenant) is unaffected.
	rec = post(t, env.router("admin@pve"), "/api/v1/tenants", `{"name":"fine"}`)
	if rec.Code != http.StatusCreated {
		t.Errorf("unscoped POST /tenants = %d, want 201: %s", rec.Code, rec.Body.String())
	}
}

// TestTenantScopeMutation_MemberForbiddenEvenForOwnTenant pins the
// stricter-than-membership rule on PUT/DELETE .../scopes: a member may not
// widen (or narrow) their OWN tenant's scope boundary, because AddScope
// never validates the ref against anything — a member permitted to call this
// could hand their tenant visibility into any resource on the cluster,
// including another tenant's exclusive scope. Only an unscoped (fleet-admin)
// caller may. Before this task's fix, alice could freely add/remove scope
// refs on t1 (and, worse, on any OTHER tenant too — see
// TestTenantScoping_NoCrossTenantLeakage for the foreign-tenant case).
func TestTenantScopeMutation_MemberForbiddenEvenForOwnTenant(t *testing.T) {
	env := newTenantEnv(t)
	env.seedTenant(t, "t1", map[string]string{"alice@pve": store.TenantRoleMember}, "guest:pve1:100")
	aliceR := env.router("alice@pve")

	if code := doReq(t, aliceR, http.MethodPut, "/api/v1/tenants/t1/scopes",
		`{"scopeRef":"guest:pve9:999"}`).Code; code != http.StatusForbidden {
		t.Errorf("member's PUT own tenant's /scopes = %d, want 403", code)
	}
	if code := doReq(t, aliceR, http.MethodDelete, "/api/v1/tenants/t1/scopes?scopeRef=guest:pve1:100",
		"").Code; code != http.StatusForbidden {
		t.Errorf("member's DELETE own tenant's /scopes = %d, want 403", code)
	}

	// An unscoped admin can still manage t1's scope.
	adminR := env.router("admin@pve")
	if code := doReq(t, adminR, http.MethodPut, "/api/v1/tenants/t1/scopes",
		`{"scopeRef":"guest:pve9:999"}`).Code; code != http.StatusNoContent {
		t.Errorf("unscoped admin's PUT /scopes = %d, want 204", code)
	}
}

// TestTenantSelfService_MemberCanManageOwnTenant proves the fix above is a
// narrow, membership-scoped restriction and not an accidental lockout: a
// member of t1 can still delete t1 and manage t1's own membership — the
// self-service latitude T-3002-followup-02's reasoning explicitly preserves
// (docs: "Tenants & self-service"), just not its scope boundary.
func TestTenantSelfService_MemberCanManageOwnTenant(t *testing.T) {
	env := newTenantEnv(t)
	env.seedTenant(t, "t1", map[string]string{"alice@pve": store.TenantRoleMember}, "guest:pve1:100")
	aliceR := env.router("alice@pve")

	if code := doReq(t, aliceR, http.MethodPut, "/api/v1/tenants/t1/members",
		`{"identity":"carol@pve","role":"member"}`).Code; code != http.StatusNoContent {
		t.Fatalf("member's PUT own tenant's /members = %d, want 204", code)
	}
	if code := doReq(t, aliceR, http.MethodDelete, "/api/v1/tenants/t1/members/carol@pve",
		"").Code; code != http.StatusNoContent {
		t.Fatalf("member's DELETE own tenant's member = %d, want 204", code)
	}
	if code := doReq(t, aliceR, http.MethodDelete, "/api/v1/tenants/t1", "").Code; code != http.StatusNoContent {
		t.Fatalf("member's DELETE own tenant = %d, want 204", code)
	}
}
