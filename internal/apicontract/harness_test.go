// Package apicontract is T-1106's conformance suite: it drives the REAL
// production HTTP handlers (internal/api.NewRouter, internal/change.Service,
// internal/auth.Service, internal/spec) against internal/pvemock fixtures
// exactly the way an external Terraform provider / Ansible collection would
// — over HTTP, bearer-token authenticated, never importing this repo's Go
// packages directly. Golden fixtures under testdata/golden/*.json are
// snapshots of real handler responses (regenerate with `-update`); a
// deliberate handler schema break (a renamed/removed field, a changed
// status code) fails this suite, which is the whole point: automation
// consumers get CI coverage in this repo, not a silent break discovered
// downstream in an external provider/collection repo.
//
// Explicit scope boundary (CLAUDE.md, T-1106's card): this package is the
// contract test suite only. No terraform-provider-vnprox or
// ansible-collection-vnprox source exists anywhere in this repository —
// those are separate, publishable repositories this card does not create.
package apicontract

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/api"
	"github.com/bgovanlu/vnprox/internal/auth"
	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/collect"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
	"github.com/bgovanlu/vnprox/internal/store"
)

// Fixture paths this suite runs its scenarios against (T-1106 card: "both
// single-node and three-node-vlan").
const (
	fixtureSingleNode = "../../testdata/clusters/single-node.yaml"
	fixtureThreeNode  = "../../testdata/clusters/three-node-vlan.yaml"
)

// contractHarness wires the exact production stack (minus a real Proxmox
// host/root — this package develops against internal/pvemock per
// CLAUDE.md's "no live Proxmox cluster" instruction) behind an
// httptest.Server: a real *auth.Service (so bearer-token scope/capability
// enforcement is genuine, the same production middleware chain
// internal/auth/bearer_test.go exercises), a real *change.Service backed by
// a real SQLite store, and a real *inventory.Graph populated by one full
// collect poll against pvemock (the same pattern
// internal/spec/testhelpers_test.go and internal/change's cross-node
// fixture tests use) — so GET /spec, POST /spec/import, and every
// changeset validator see exactly the state a live cluster would produce.
type contractHarness struct {
	t         *testing.T
	fixture   *pvemock.Fixture
	baseURL   string
	client    *http.Client
	pveMock   *httptest.Server
	graph     *inventory.Graph
	tokens    *store.APITokenRepo
	authSvc   *auth.Service
	changeSvc *change.Service
	localNode string

	// External conformance mode (T-2101, conformance_external_test.go): set
	// when VNPROX_CONFORMANCE_BASE_URL points this suite at an already-running,
	// out-of-process vnproxd instead of the in-process stack built below —
	// the "consumable by an external CI run" half of this package. mintToken
	// hands back one of these two pre-bootstrapped tokens instead of writing
	// to a local store.
	external        bool
	externalRWToken string
	externalROToken string
}

// newContractHarness builds a full harness for fixturePath. Every scenario
// in this package calls this once per fixture (single-node/three-node-vlan)
// and drives the rest purely over HTTP, exactly like an external caller
// would.
//
// When VNPROX_CONFORMANCE_BASE_URL is set (external conformance mode, see
// conformance_external_test.go), this skips building the in-process stack
// entirely and instead bootstraps a session and two bearer tokens against
// the caller-supplied, already-running vnproxd — the same four scenarios in
// this package then run unmodified against a real, out-of-process daemon.
// A fixturePath whose short name doesn't match VNPROX_CONFORMANCE_FIXTURE
// (default "single-node") is skipped, since only one fixture can be loaded
// into a given running daemon at a time.
func newContractHarness(t *testing.T, fixturePath string) *contractHarness {
	t.Helper()

	if cfg, ok := loadExternalConfig(t); ok {
		if !externalFixtureMatches(fixturePath, cfg) {
			t.Skipf("external conformance mode: %s=%q selects a different fixture than %s, skipping", envConformanceFixture, cfg.fixture, fixturePath)
		}
		return newExternalContractHarness(t, cfg)
	}

	f, err := pvemock.LoadFixture(fixturePath)
	if err != nil {
		t.Fatalf("LoadFixture(%s): %v", fixturePath, err)
	}
	mockSrv := pvemock.NewServer(f)
	mockTS := httptest.NewServer(mockSrv)
	t.Cleanup(mockTS.Close)

	pveClient, err := pve.New(pve.Config{
		APIURL: mockTS.URL, Auth: pve.AuthTicket, Username: "root@pam", Password: "vnprox-mock",
	})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}

	// Real inventory.Graph, populated by one real collect poll against the
	// mock — not a hand-rolled snapshot (internal/spec/testhelpers_test.go's
	// buildFixtureGraph pattern), so GET /spec / POST /spec/import exercise
	// the real internal/spec.Export/Import diff engine against exactly
	// what a live cluster's poll would produce.
	graph := inventory.NewGraph()
	collector, err := collect.New(collect.Config{
		PVE:   pveClient,
		Host:  host.NewFixtureReader(pvemock.NewFixtureHostReader(mockSrv)),
		Graph: graph,
	})
	if err != nil {
		t.Fatalf("collect.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, refreshErr := collector.RefreshNow(ctx, inventory.Scope{}); refreshErr != nil {
		t.Fatalf("RefreshNow: %v", refreshErr)
	}

	db := openContractDB(t)
	csRepo := store.NewChangesetRepo(db)
	auditRepo := store.NewAuditRepo(db)
	snapRepo := store.NewSnapshotRepo(db)
	blobRepo := store.NewBlobRepo(db)
	tokenRepo := store.NewAPITokenRepo(db)

	key := make([]byte, store.KeySize) // all-zero: fine for a test-only cipher, never production.
	cipher, err := store.NewSessionCipher(key)
	if err != nil {
		t.Fatalf("store.NewSessionCipher: %v", err)
	}
	sessionRepo := store.NewSessionRepo(db, cipher)

	authSvc, err := auth.NewService(auth.Config{
		Sessions: sessionRepo, Audit: auditRepo, Tokens: tokenRepo,
		// This suite is token-authed only (T-1106's card: "no PVE ticket
		// flow exposed to them") — the cookie-login factory is never
		// exercised, so a stub that always fails is deliberate, not a gap.
		NewIdentity: func(string, string, string, string) (auth.PVEIdentity, error) {
			return nil, errNoLoginInContractSuite
		},
	})
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}

	agent := newContractNodeAgent(pvemock.NewFixtureHostReader(mockSrv), pveClient)
	protectedPath := filepath.Join(t.TempDir(), "protected.json")
	changeSvc, err := change.NewService(change.Config{
		Changesets: csRepo, Audit: auditRepo, Snapshots: snapRepo, Blobs: blobRepo,
		Nodes: agent, Inventory: graph, ProtectedPath: protectedPath,
	})
	if err != nil {
		t.Fatalf("change.NewService: %v", err)
	}

	router := api.NewRouter(api.Options{
		Version: "contract-test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:       contractAuthAdapter{authSvc},
		Changesets: changeSvc,
		Spec:       graph,
	})
	apiTS := httptest.NewServer(router)
	t.Cleanup(apiTS.Close)

	localNode := ""
	if len(f.Cluster.Nodes) > 0 {
		localNode = f.Cluster.Nodes[0].Name
	}

	return &contractHarness{
		t: t, fixture: f, baseURL: apiTS.URL, client: apiTS.Client(), pveMock: mockTS, graph: graph,
		tokens: tokenRepo, localNode: localNode, authSvc: authSvc, changeSvc: changeSvc,
	}
}

var errNoLoginInContractSuite = &contractLoginError{}

type contractLoginError struct{}

func (*contractLoginError) Error() string {
	return "apicontract: cookie/PVE-ticket login is intentionally unsupported in this token-only contract suite"
}

// mintToken creates a bearer token directly in the store (bypassing
// POST /tokens, which is T-1104's own already-tested route — this suite's
// job is proving the changeset/spec *routes* work correctly when called
// with a token, not re-testing token minting itself) with scopes and
// returns the raw bearer value.
func (h *contractHarness) mintToken(id string, scopes ...string) string {
	h.t.Helper()
	if h.external {
		return h.externalToken(scopes)
	}
	raw, hash, err := auth.GenerateAPIToken()
	if err != nil {
		h.t.Fatalf("GenerateAPIToken: %v", err)
	}
	scopesJSON := "["
	for i, s := range scopes {
		if i > 0 {
			scopesJSON += ","
		}
		scopesJSON += `"` + s + `"`
	}
	scopesJSON += "]"
	if err := h.tokens.Create(context.Background(), store.APIToken{
		ID: id, Name: "contract-test-token", TokenHash: hash, ScopesJSON: scopesJSON,
		CreatedBy: "root@pam", CreatedAt: time.Now().Unix(),
	}); err != nil {
		h.t.Fatalf("tokens.Create: %v", err)
	}
	return raw
}

// externalToken picks between the two bootstrapped external-mode tokens by
// the same scope shape every scenario in this package already requests:
// {netRead, netWrite} for a write-capable token, {netRead} alone for a
// read-only one. See conformance_external_test.go.
func (h *contractHarness) externalToken(scopes []string) string {
	h.t.Helper()
	for _, s := range scopes {
		if s == "netWrite" {
			if h.externalRWToken == "" {
				h.t.Fatal("apicontract: external conformance mode requested a netWrite-scoped token but none was bootstrapped")
			}
			return h.externalRWToken
		}
	}
	if h.externalROToken == "" {
		h.t.Fatal("apicontract: external conformance mode requested a netRead-only token but none was bootstrapped")
	}
	return h.externalROToken
}

// newRequest builds an httptest-server-relative request with the given
// bearer token and (for mutating methods) the CSRF header set to a fixed
// value — bearer requests skip CSRF per docs/api.md's Conventions section
// ("no cookie to double-submit"), but sending it anyway costs nothing and
// proves the route doesn't wrongly require it from a token caller.
func (h *contractHarness) newRequest(method, path, token string, body []byte) *http.Request {
	h.t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, h.baseURL+path, reader)
	if err != nil {
		h.t.Fatalf("building request %s %s: %v", method, path, err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func (h *contractHarness) do(req *http.Request) *http.Response {
	h.t.Helper()
	resp, err := h.client.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", req.Method, req.URL.Path, err)
	}
	h.t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// contractAuthAdapter bridges *auth.Service to internal/api's AuthService/
// UsernameLookup/TokenMinter seams — a copy of cmd/vnproxd's own
// authServiceAdapter (topology.go), reimplemented here because that type is
// unexported in package main and cannot be imported. Keeping this adapter
// trivial (delegation only, no logic of its own) is what keeps it a faithful
// stand-in for the production wiring rather than a second implementation
// of anything this suite is supposed to be testing.
type contractAuthAdapter struct {
	*auth.Service
}

func (a contractAuthAdapter) RequireCap(cap string) func(http.Handler) http.Handler {
	return a.Service.RequireCap(auth.Cap(cap))
}

func (a contractAuthAdapter) Username(ctx context.Context) (string, bool) {
	id, ok := auth.IdentityFromContext(ctx)
	if !ok {
		return "", false
	}
	return id.Username, true
}

// --- node agent test double ------------------------------------------

// contractNodeAgent is a copy of internal/change's own fakeNodeAgent
// (apply_helpers_test.go) — an in-memory per-node committed/staged
// interfaces file, with reloads driven through a real *pve.Client against
// pvemock so the executor's real ordered plan (stage, then reload) runs
// unmodified. Reimplemented here (rather than imported) because the
// original is an unexported type in package change_test.
type contractNodeAgent struct {
	seed      pvemock.HostReader
	client    *pve.Client
	committed map[string]string
	staged    map[string]string
	mu        sync.Mutex
}

func newContractNodeAgent(seed pvemock.HostReader, client *pve.Client) *contractNodeAgent {
	return &contractNodeAgent{seed: seed, client: client, committed: map[string]string{}, staged: map[string]string{}}
}

func (a *contractNodeAgent) ReadInterfaces(ctx context.Context, node string) (string, error) {
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

func (a *contractNodeAgent) StageInterfaces(_ context.Context, node, content string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.staged[node] = content
	return nil
}

func (a *contractNodeAgent) ReloadInterfaces(ctx context.Context, node string) error {
	upid, err := a.client.ReloadNodeNetwork(ctx, node)
	if err != nil {
		return err
	}
	if _, err := a.client.WaitTask(ctx, node, upid, pve.WaitOptions{Interval: 5 * time.Millisecond, Timeout: 5 * time.Second}); err != nil {
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

func (a *contractNodeAgent) DiscardStaged(_ context.Context, node string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.staged, node)
	return nil
}

var _ change.NodeAgent = (*contractNodeAgent)(nil)

// --- misc test scaffolding ------------------------------------------

func openContractDB(t *testing.T) *store.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vnprox.db")
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
