// SPDX-License-Identifier: Apache-2.0

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
// /access/permissions, and GET /cluster/status are all exercised for real
// over HTTP against the mock for the login step (the exact production
// derivation path); logout and a subsequent /auth/me exercise this
// package's own session/cookie machinery on top of that.
func TestIntegration_LoginLogoutMeAgainstMock(t *testing.T) {
	ts, _ := newMockServer(t, fixtureSingleNode)
	sessions, audit, _ := newTestStore(t)
	svc, err := auth.NewService(auth.Config{
		Sessions:    sessions,
		Audit:       audit,
		NewIdentity: fixtureIdentityFactory(ts, 0),
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
	want := auth.Capabilities{NetRead: true, NetWrite: true, SDNRead: true, SDNWrite: true, FWRead: true, FWWrite: true, GuestNet: true, Audit: true, Capture: true}
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

// TestIntegration_CapabilityMatrixAgainstMock is T-105 acceptance
// criterion 3, driven end-to-end: all four documented personas (root,
// auditor, sdn-only, vm-user — three-node-vlan.yaml fixture users) log in
// for real against the mock, with capability derivation flowing through
// the genuine GET /access/permissions + GET /cluster/status HTTP path,
// and must yield exactly the documented caps (docs/security.md's mapping
// table, internal/auth/caps.go).
//
// The sdn-only and vm-user personas lack Sys.Audit, so the mock (like
// real PVE) denies them GET /cluster/status; the login handler then falls
// back to the single cluster-wide capability entry keyed "" (see
// caps.go's BuildCapabilities) — asserted as such here.
func TestIntegration_CapabilityMatrixAgainstMock(t *testing.T) {
	ts, _ := newMockServer(t, fixtureThreeNode)
	sessions, audit, _ := newTestStore(t)
	svc, err := auth.NewService(auth.Config{
		Sessions:    sessions,
		Audit:       audit,
		NewIdentity: fixtureIdentityFactory(ts, 0),
	})
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) { svc.MountRoutes(r) })
	daemon := httptest.NewServer(r)
	defer daemon.Close()

	allNodes := []string{"pve1", "pve2", "pve3"}
	cases := []struct {
		persona  string
		username string
		password string
		wantKeys []string // capability-map keys: node names, or "" for the cluster-wide fallback
		want     auth.Capabilities
	}{
		{
			persona: "root", username: "root", password: "vnprox-mock",
			wantKeys: allNodes,
			want: auth.Capabilities{
				NetRead: true, NetWrite: true, SDNRead: true, SDNWrite: true,
				FWRead: true, FWWrite: true, GuestNet: true, Audit: true, Capture: true,
			},
		},
		{
			persona: "auditor", username: "auditor", password: "readonly",
			wantKeys: allNodes,
			want:     auth.Capabilities{NetRead: true, SDNRead: true, FWRead: true, Audit: true},
		},
		{
			persona: "sdn-only", username: "sdn-only", password: "sdn-only",
			wantKeys: []string{""},
			want:     auth.Capabilities{SDNRead: true, SDNWrite: true},
		},
		{
			persona: "vm-user", username: "vm-user", password: "vm-user",
			wantKeys: []string{""},
			want:     auth.Capabilities{GuestNet: true},
		},
	}

	for _, tc := range cases {
		t.Run(tc.persona, func(t *testing.T) {
			realm := "pve"
			if tc.username == "root" {
				realm = "pam"
			}
			body, _ := json.Marshal(map[string]string{"username": tc.username, "password": tc.password, "realm": realm})
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
			if len(loginBody.Caps) != len(tc.wantKeys) {
				t.Fatalf("caps map has %d entries %v, want keys %v", len(loginBody.Caps), loginBody.Caps, tc.wantKeys)
			}
			for _, key := range tc.wantKeys {
				got, ok := loginBody.Caps[key]
				if !ok {
					t.Fatalf("caps map missing key %q: %+v", key, loginBody.Caps)
				}
				if got != tc.want {
					t.Errorf("caps[%q] = %+v, want %+v", key, got, tc.want)
				}
			}
		})
	}
}

// TestIntegration_TOTPLoginAgainstMock is T-105 acceptance criterion 1's
// TOTP half against a real mock server: single-node.yaml's totp-user@pve
// requires otp=246810 at POST /access/ticket, so the full /auth/login →
// PVE login → capability derivation flow must succeed with the code and
// fail as invalid_credentials without it (or with a wrong one) — the OTP
// passthrough exercised over genuine HTTP, not a stub identity.
func TestIntegration_TOTPLoginAgainstMock(t *testing.T) {
	ts, _ := newMockServer(t, fixtureSingleNode)
	sessions, audit, _ := newTestStore(t)
	svc, err := auth.NewService(auth.Config{
		Sessions:    sessions,
		Audit:       audit,
		NewIdentity: fixtureIdentityFactory(ts, 0),
	})
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) { svc.MountRoutes(r) })
	daemon := httptest.NewServer(r)
	defer daemon.Close()

	login := func(otp string) *http.Response {
		t.Helper()
		body, _ := json.Marshal(map[string]string{
			"username": "totp-user", "password": "with-2fa", "realm": "pve", "otp": otp,
		})
		resp, err := http.Post(daemon.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("POST /auth/login: %v", err)
		}
		return resp
	}

	t.Run("correct OTP succeeds with derived caps", func(t *testing.T) {
		resp := login("246810")
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
		want := auth.Capabilities{NetRead: true, SDNRead: true, FWRead: true, Audit: true}
		if got := loginBody.Caps["pve1"]; got != want {
			t.Errorf("caps[pve1] = %+v, want %+v (totp-user is auditor-privileged)", got, want)
		}
	})

	t.Run("missing OTP rejected as invalid_credentials", func(t *testing.T) {
		resp := login("")
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("login status = %d, want 401", resp.StatusCode)
		}
		assertErrorCode(t, resp, "invalid_credentials")
	})

	t.Run("wrong OTP rejected as invalid_credentials", func(t *testing.T) {
		resp := login("000000")
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("login status = %d, want 401", resp.StatusCode)
		}
		assertErrorCode(t, resp, "invalid_credentials")
	})
}

// TestIntegration_BadCredentialsAgainstMock proves a wrong password on a
// real fixture user is rejected the same way handlers_test.go's stub-based
// tests assert, but through a genuine PVE 401 (internal/pvemock's
// handleTicket) rather than a simulated one.
func TestIntegration_BadCredentialsAgainstMock(t *testing.T) {
	ts, _ := newMockServer(t, fixtureSingleNode)
	sessions, audit, _ := newTestStore(t)
	svc, err := auth.NewService(auth.Config{
		Sessions:    sessions,
		Audit:       audit,
		NewIdentity: fixtureIdentityFactory(ts, 0),
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
