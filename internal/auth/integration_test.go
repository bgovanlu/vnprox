package auth_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/auth"
)

// TestIntegration_LoginLogoutMeAgainstMock is T-105 acceptance criterion 1
// against a real internal/pvemock server: POST /access/ticket, GET
// /cluster/status, and (see helpers_test.go's fixturePermissionsIdentity)
// a fixture-sourced stand-in for GET /access/permissions (which pvemock
// does not implement — see doc.go) are all exercised for real over HTTP
// against the mock for the login step; logout and a subsequent /auth/me
// exercise this package's own session/cookie machinery on top of that.
func TestIntegration_LoginLogoutMeAgainstMock(t *testing.T) {
	ts, fixture := newMockServer(t, fixtureSingleNode)
	sessions, audit, _ := newTestStore(t)
	svc, err := auth.NewService(auth.Config{
		Sessions:    sessions,
		Audit:       audit,
		NewIdentity: fixtureIdentityFactory(ts, fixture, 0),
	})
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}

	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) { svc.MountRoutes(r) })
	daemon := httptest.NewServer(r)
	defer daemon.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	client := &http.Client{Jar: jar}

	body, err := json.Marshal(map[string]string{
		"username": "root", "password": "vnprox-mock", "realm": "pam",
	})
	if err != nil {
		t.Fatalf("marshaling login body: %v", err)
	}
	loginResp, err := client.Post(daemon.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /auth/login: %v", err)
	}
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", loginResp.StatusCode)
	}
	var loginBody struct {
		Caps map[string]auth.Capabilities
		User struct{ Username, Realm string }
	}
	if decodeErr := json.NewDecoder(loginResp.Body).Decode(&loginBody); decodeErr != nil {
		t.Fatalf("decoding login response: %v", decodeErr)
	}
	_ = loginResp.Body.Close()

	if loginBody.User.Username != "root" || loginBody.User.Realm != "pam" {
		t.Errorf("login user = %+v, want {root pam}", loginBody.User)
	}
	// single-node.yaml has one cluster node, pve1; root@pam is "*"
	// (wildcard) so every capability should be granted there.
	pve1Caps, ok := loginBody.Caps["pve1"]
	if !ok {
		t.Fatalf("caps map missing pve1 entry: %+v", loginBody.Caps)
	}
	want := auth.Capabilities{NetRead: true, NetWrite: true, SDNRead: true, SDNWrite: true, FWRead: true, FWWrite: true, GuestNet: true, Audit: true}
	if pve1Caps != want {
		t.Errorf("caps[pve1] = %+v, want %+v (root@pam wildcard)", pve1Caps, want)
	}

	meResp, err := client.Get(daemon.URL + "/api/v1/auth/me")
	if err != nil {
		t.Fatalf("GET /auth/me: %v", err)
	}
	if meResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /auth/me status = %d, want 200", meResp.StatusCode)
	}
	var meBody struct {
		Caps map[string]auth.Capabilities
	}
	if decodeErr := json.NewDecoder(meResp.Body).Decode(&meBody); decodeErr != nil {
		t.Fatalf("decoding /auth/me response: %v", decodeErr)
	}
	_ = meResp.Body.Close()
	if meBody.Caps["pve1"] != want {
		t.Errorf("/auth/me caps[pve1] = %+v, want %+v", meBody.Caps["pve1"], want)
	}

	// Need the CSRF value for logout; read it from the cookie jar rather
	// than assuming a fixed value (it's server-generated).
	csrfToken := csrfFromJar(t, jar, daemon.URL)
	logoutReq, err := http.NewRequest(http.MethodPost, daemon.URL+"/api/v1/auth/logout", nil)
	if err != nil {
		t.Fatalf("building logout request: %v", err)
	}
	logoutReq.Header.Set(auth.CSRFHeaderName, csrfToken)
	logoutResp, err := client.Do(logoutReq)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	_ = logoutResp.Body.Close()
	if logoutResp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", logoutResp.StatusCode)
	}

	meAfterLogout, err := client.Get(daemon.URL + "/api/v1/auth/me")
	if err != nil {
		t.Fatalf("GET /auth/me after logout: %v", err)
	}
	defer func() { _ = meAfterLogout.Body.Close() }()
	if meAfterLogout.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /auth/me after logout: status = %d, want 401", meAfterLogout.StatusCode)
	}
}

// TestIntegration_AuditorCapabilitiesAgainstMock proves the auditor@pve
// fixture user (Sys.Audit, VM.Audit, SDN.Audit — single-node.yaml's
// "read-only auditor") gets exactly read-only capabilities from a real
// login against the mock, complementing caps_test.go's pure unit test of
// the same fixture privilege list.
func TestIntegration_AuditorCapabilitiesAgainstMock(t *testing.T) {
	ts, fixture := newMockServer(t, fixtureSingleNode)
	sessions, audit, _ := newTestStore(t)
	svc, err := auth.NewService(auth.Config{
		Sessions:    sessions,
		Audit:       audit,
		NewIdentity: fixtureIdentityFactory(ts, fixture, 0),
	})
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) { svc.MountRoutes(r) })
	daemon := httptest.NewServer(r)
	defer daemon.Close()

	body, _ := json.Marshal(map[string]string{"username": "auditor", "password": "readonly", "realm": "pve"})
	resp, err := http.Post(daemon.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /auth/login: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", resp.StatusCode)
	}
	var loginBody struct {
		Caps map[string]auth.Capabilities
	}
	if err := json.NewDecoder(resp.Body).Decode(&loginBody); err != nil {
		t.Fatalf("decoding login response: %v", err)
	}

	want := auth.Capabilities{NetRead: true, NetWrite: false, SDNRead: true, SDNWrite: false, FWRead: true, FWWrite: false, GuestNet: false, Audit: true}
	if got := loginBody.Caps["pve1"]; got != want {
		t.Errorf("caps[pve1] for auditor@pve = %+v, want %+v", got, want)
	}
}

// TestIntegration_BadCredentialsAgainstMock proves a wrong password on a
// real fixture user is rejected the same way handlers_test.go's stub-based
// tests assert, but through a genuine PVE 401 (internal/pvemock's
// handleTicket) rather than a simulated one.
func TestIntegration_BadCredentialsAgainstMock(t *testing.T) {
	ts, fixture := newMockServer(t, fixtureSingleNode)
	sessions, audit, _ := newTestStore(t)
	svc, err := auth.NewService(auth.Config{
		Sessions:    sessions,
		Audit:       audit,
		NewIdentity: fixtureIdentityFactory(ts, fixture, 0),
	})
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) { svc.MountRoutes(r) })
	daemon := httptest.NewServer(r)
	defer daemon.Close()

	body, _ := json.Marshal(map[string]string{"username": "root", "password": "not-the-password", "realm": "pam"})
	resp, err := http.Post(daemon.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /auth/login: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	assertErrorCode(t, resp, "invalid_credentials")
}

func csrfFromJar(t *testing.T, jar *cookiejar.Jar, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parsing URL %s: %v", rawURL, err)
	}
	for _, c := range jar.Cookies(u) {
		if c.Name == auth.CSRFCookieName {
			return c.Value
		}
	}
	t.Fatalf("no %s cookie found for %s", auth.CSRFCookieName, rawURL)
	return ""
}
