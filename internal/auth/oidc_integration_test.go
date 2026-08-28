// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/auth"
	"github.com/bgovanlu/vnprox/internal/auth/oidcmock"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/store"
)

// oidcTestRig bundles a running mock IdP + a mounted auth.Service with OIDC
// enabled, plus the HTTP client (cookie jar) to drive it.
type oidcTestRig struct {
	idp    *oidcmock.Provider
	daemon string
	client *http.Client
	mapped oidcmock.User
}

// zeroCipher is an all-zero-key SessionCipher — fine for tests, never
// production (the same convention helpers_test.go's newTestStore uses).
func zeroCipher(t *testing.T) *store.SessionCipher {
	t.Helper()
	c, err := store.NewSessionCipher(make([]byte, store.KeySize))
	if err != nil {
		t.Fatalf("NewSessionCipher: %v", err)
	}
	return c
}

// linkGroupToAuditor seals the auditor@pve fixture user (read-only PVE ACLs) as
// the PVE identity that `group` maps to on the local cluster, against the
// pvemock at pveURL. Ticket-mode is used because pvemock has no API-token auth
// (internal/config's [pve] dev-ticket note); production linkages use a PVE API
// token.
func linkGroupToAuditor(t *testing.T, repo *store.OIDCPVELinkRepo, cipher *store.SessionCipher, group string) {
	t.Helper()
	sealed, err := auth.SealLinkCredential(cipher, auth.LinkCredential{
		Kind: auth.LinkCredentialTicket, Username: "auditor", Password: "readonly", Realm: "pve",
	})
	if err != nil {
		t.Fatalf("SealLinkCredential: %v", err)
	}
	if err := repo.Upsert(context.Background(), store.OIDCPVELink{
		ID: store.NewULID(), ClusterID: "", OIDCGroup: group, PVEUsername: "auditor@pve",
		CredentialEnc: sealed, CreatedBy: "root@pam", CreatedAt: time.Now().Unix(),
	}); err != nil {
		t.Fatalf("Upsert link: %v", err)
	}
}

// buildOIDCService assembles an OIDCService for the mock IdP with the given
// group→bundle mappings and PVE-link resolver.
func buildOIDCService(t *testing.T, idp *oidcmock.Provider, resolver auth.PVELinkResolver, mappings []auth.GroupMapping) *auth.OIDCService {
	t.Helper()
	provider, err := auth.NewOIDCProvider(auth.OIDCProviderConfig{
		Issuer: idp.Issuer(), ClientID: testClientID, RedirectURL: "https://vnprox.example/oidc/callback",
		HTTPClient: idp.HTTPClient(), Scopes: []string{"profile", "groups"}, GroupsClaim: "groups",
	})
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}
	svc, err := auth.NewOIDCService(auth.OIDCConfig{Provider: provider, Resolver: resolver, Mappings: mappings})
	if err != nil {
		t.Fatalf("NewOIDCService: %v", err)
	}
	return svc
}

// runOIDCLogin drives GET /auth/oidc/login → IdP code issue → POST
// /auth/oidc/callback and returns the callback response and decoded caps.
func runOIDCLogin(t *testing.T, rig oidcTestRig, groups []string) (*http.Response, map[string]auth.Capabilities) {
	t.Helper()
	ctx := context.Background()

	loginResp, err := rig.client.Get(rig.daemon + "/api/v1/auth/oidc/login")
	if err != nil {
		t.Fatalf("GET /auth/oidc/login: %v", err)
	}
	var login struct {
		AuthorizationURL string `json:"authorizationUrl"`
		State            string `json:"state"`
	}
	if decErr := json.NewDecoder(loginResp.Body).Decode(&login); decErr != nil {
		t.Fatalf("decode login: %v", decErr)
	}
	_ = loginResp.Body.Close()

	u, err := url.Parse(login.AuthorizationURL)
	if err != nil {
		t.Fatalf("parse authorizationUrl: %v", err)
	}
	q := u.Query()
	user := rig.mapped
	user.Groups = groups
	code, err := rig.idp.IssueCode(q.Get("nonce"), q.Get("code_challenge"), user)
	if err != nil {
		t.Fatalf("IssueCode: %v", err)
	}

	body, err := json.Marshal(map[string]string{"code": code, "state": login.State})
	if err != nil {
		t.Fatalf("marshal callback: %v", err)
	}
	cbResp, err := rig.client.Post(rig.daemon+"/api/v1/auth/oidc/callback", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /auth/oidc/callback: %v", err)
	}
	var cb struct {
		Caps map[string]auth.Capabilities `json:"caps"`
		User struct{ Username, Realm string }
	}
	if decErr := json.NewDecoder(cbResp.Body).Decode(&cb); decErr != nil {
		t.Fatalf("decode callback: %v", decErr)
	}
	_ = cbResp.Body.Close()
	_ = ctx
	return cbResp, cb.Caps
}

func newOIDCRig(t *testing.T, svc *auth.Service, idp *oidcmock.Provider) oidcTestRig {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) { svc.MountRoutes(r) })
	server := httptest.NewServer(r)
	t.Cleanup(server.Close)
	daemon := server.URL
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	return oidcTestRig{
		idp: idp, daemon: daemon, client: &http.Client{Jar: jar},
		mapped: oidcmock.User{Subject: "u-1", PreferredUsername: "alice", Email: "alice@example"},
	}
}

// TestIntegration_OIDCCappedAtPVEACLs is T-1207 AC1 + AC3: an OIDC login issues
// a session cookie with the same security properties as the ticket bridge, and
// the OIDC-mapped (full-write) bundle is capped at the linked auditor identity's
// read-only PVE ACLs.
func TestIntegration_OIDCCappedAtPVEACLs(t *testing.T) {
	ts, _ := newMockServer(t, fixtureSingleNode)
	sessions, audit, db := newTestStore(t)
	cipher := zeroCipher(t)
	linkRepo := store.NewOIDCPVELinkRepo(db)
	linkGroupToAuditor(t, linkRepo, cipher, "vnprox-readers")

	idp, err := oidcmock.New(testClientID)
	if err != nil {
		t.Fatalf("oidcmock.New: %v", err)
	}
	t.Cleanup(idp.Close)

	resolver := auth.NewStorePVELinkResolver(linkRepo, cipher, "", pve.Config{APIURL: ts.URL, HTTPClient: ts.Client()})
	// The group maps to a *full-write* bundle; the PVE cap must strip the writes.
	oidcSvc := buildOIDCService(t, idp, resolver, []auth.GroupMapping{{Group: "vnprox-readers", Caps: fullWrite}})

	svc, err := auth.NewService(auth.Config{
		Sessions: sessions, Audit: audit, NewIdentity: stubFactory(stubIdentity{}), OIDC: oidcSvc,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	rig := newOIDCRig(t, svc, idp)

	cbResp, caps := runOIDCLogin(t, rig, []string{"vnprox-readers"})
	if cbResp.StatusCode != http.StatusOK {
		t.Fatalf("callback status = %d, want 200", cbResp.StatusCode)
	}

	// AC1: session cookie carries HttpOnly + Secure + SameSite=Strict.
	assertSessionCookieSecure(t, cbResp)

	// AC3: full-write OIDC bundle ∩ auditor read-only PVE ACLs = read-only.
	want := auth.Capabilities{NetRead: true, SDNRead: true, FWRead: true, Audit: true}
	if caps["pve1"] != want {
		t.Errorf("caps[pve1] = %+v, want %+v (capped at auditor ACLs)", caps["pve1"], want)
	}
	if caps["pve1"].NetWrite || caps["pve1"].SDNWrite || caps["pve1"].FWWrite || caps["pve1"].GuestNet {
		t.Errorf("write flags survived the PVE cap: %+v", caps["pve1"])
	}

	// The session persisted: /auth/me returns the same capped caps.
	meResp, err := rig.client.Get(rig.daemon + "/api/v1/auth/me")
	if err != nil {
		t.Fatalf("GET /auth/me: %v", err)
	}
	var me struct {
		Caps map[string]auth.Capabilities `json:"caps"`
	}
	if decErr := json.NewDecoder(meResp.Body).Decode(&me); decErr != nil {
		t.Fatalf("decode /auth/me: %v", decErr)
	}
	_ = meResp.Body.Close()
	if me.Caps["pve1"] != want {
		t.Errorf("/auth/me caps[pve1] = %+v, want %+v", me.Caps["pve1"], want)
	}
}

// TestIntegration_OIDCNoLinkageDeniesWrites is T-1207 AC2: a user whose group
// grants a full-write OIDC bundle but has NO PVE linkage for the cluster is
// authenticated yet holds zero cluster-scoped capability — the authn/authz
// split, proven end-to-end.
func TestIntegration_OIDCNoLinkageDeniesWrites(t *testing.T) {
	ts, _ := newMockServer(t, fixtureSingleNode)
	sessions, audit, db := newTestStore(t)
	cipher := zeroCipher(t)
	linkRepo := store.NewOIDCPVELinkRepo(db)
	// Deliberately NO linkage for the group below.

	idp, err := oidcmock.New(testClientID)
	if err != nil {
		t.Fatalf("oidcmock.New: %v", err)
	}
	t.Cleanup(idp.Close)

	resolver := auth.NewStorePVELinkResolver(linkRepo, cipher, "", pve.Config{APIURL: ts.URL, HTTPClient: ts.Client()})
	oidcSvc := buildOIDCService(t, idp, resolver, []auth.GroupMapping{{Group: "vnprox-writers", Caps: fullWrite}})

	svc, err := auth.NewService(auth.Config{
		Sessions: sessions, Audit: audit, NewIdentity: stubFactory(stubIdentity{}), OIDC: oidcSvc,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	rig := newOIDCRig(t, svc, idp)

	cbResp, caps := runOIDCLogin(t, rig, []string{"vnprox-writers"})
	if cbResp.StatusCode != http.StatusOK {
		t.Fatalf("callback status = %d, want 200 (authenticated)", cbResp.StatusCode)
	}
	// With no linkage the cluster-wide fallback entry "" carries no capability.
	for node, c := range caps {
		if (c != auth.Capabilities{}) {
			t.Errorf("caps[%q] = %+v, want zero (no PVE linkage → no capability)", node, c)
		}
	}
}

// TestIntegration_OIDCSessionIdleTimeout is T-1207 AC4 (idle timeout applied to
// OIDC sessions): an OIDC session honors the same idle-expiry contract every
// cookie session does — advancing past the idle window invalidates it.
func TestIntegration_OIDCSessionIdleTimeout(t *testing.T) {
	ts, _ := newMockServer(t, fixtureSingleNode)
	sessions, audit, db := newTestStore(t)
	cipher := zeroCipher(t)
	linkRepo := store.NewOIDCPVELinkRepo(db)
	linkGroupToAuditor(t, linkRepo, cipher, "vnprox-readers")

	idp, err := oidcmock.New(testClientID)
	if err != nil {
		t.Fatalf("oidcmock.New: %v", err)
	}
	t.Cleanup(idp.Close)

	clk := &testClock{now: time.Unix(1_700_000_000, 0)}
	resolver := auth.NewStorePVELinkResolver(linkRepo, cipher, "", pve.Config{APIURL: ts.URL, HTTPClient: ts.Client()})
	oidcSvc := buildOIDCService(t, idp, resolver, []auth.GroupMapping{{Group: "vnprox-readers", Caps: fullWrite}})

	svc, err := auth.NewService(auth.Config{
		Sessions: sessions, Audit: audit, NewIdentity: stubFactory(stubIdentity{}), OIDC: oidcSvc,
		Now: clk.Now, IdleTimeout: 30 * time.Minute, HardTimeout: 12 * time.Hour,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	rig := newOIDCRig(t, svc, idp)

	cbResp, _ := runOIDCLogin(t, rig, []string{"vnprox-readers"})
	if cbResp.StatusCode != http.StatusOK {
		t.Fatalf("callback status = %d, want 200", cbResp.StatusCode)
	}

	// Within the idle window: /auth/me works.
	clk.advance(20 * time.Minute)
	within, getErr := rig.client.Get(rig.daemon + "/api/v1/auth/me")
	if getErr != nil {
		t.Fatalf("GET /auth/me: %v", getErr)
	}
	_ = within.Body.Close()
	if within.StatusCode != http.StatusOK {
		t.Fatalf("/auth/me within idle window: status = %d, want 200", within.StatusCode)
	}

	// Past the idle window: the session is invalidated (401), same contract as
	// a ticket-bridge session.
	clk.advance(40 * time.Minute)
	past, getErr := rig.client.Get(rig.daemon + "/api/v1/auth/me")
	if getErr != nil {
		t.Fatalf("GET /auth/me: %v", getErr)
	}
	_ = past.Body.Close()
	if past.StatusCode != http.StatusUnauthorized {
		t.Errorf("/auth/me past idle window: status = %d, want 401", past.StatusCode)
	}
}

// testClock is a manually-advanced clock for the idle-timeout test.
type testClock struct {
	now time.Time
	mu  sync.Mutex
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// assertSessionCookieSecure checks the vnprox_session cookie's security flags on
// resp (AC1 parity with the ticket-bridge session).
func assertSessionCookieSecure(t *testing.T, resp *http.Response) {
	t.Helper()
	for _, c := range resp.Cookies() {
		if c.Name != auth.SessionCookieName {
			continue
		}
		if !c.HttpOnly {
			t.Error("session cookie: HttpOnly = false, want true")
		}
		if !c.Secure {
			t.Error("session cookie: Secure = false, want true")
		}
		if c.SameSite != http.SameSiteStrictMode {
			t.Errorf("session cookie: SameSite = %v, want Strict", c.SameSite)
		}
		return
	}
	t.Fatalf("no %s cookie set on the OIDC callback response", auth.SessionCookieName)
}
