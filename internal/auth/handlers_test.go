package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/auth"
	"github.com/bgovanlu/vnprox/internal/pve"
)

func newTestService(t *testing.T, factory auth.IdentityFactory, opts ...func(*auth.Config)) *auth.Service {
	t.Helper()
	sessions, audit, _ := newTestStore(t)
	cfg := auth.Config{
		Sessions:    sessions,
		Audit:       audit,
		NewIdentity: factory,
	}
	for _, o := range opts {
		o(&cfg)
	}
	svc, err := auth.NewService(cfg)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	return svc
}

func newTestServer(t *testing.T, svc *auth.Service) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		svc.MountRoutes(r)
	})
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts
}

func loginBody(t *testing.T, username, password, realm, otp string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]string{
		"username": username, "password": password, "realm": realm, "otp": otp,
	})
	if err != nil {
		t.Fatalf("marshaling login body: %v", err)
	}
	return b
}

// --- OTP passthrough: handler-level unit coverage of the login handler's
// OTP forwarding and error mapping in isolation (stub identity, no
// network). The end-to-end TOTP flow against pvemock's TOTP-required
// fixture user is TestIntegration_TOTPLoginAgainstMock. -------------------

func TestHandleLogin_OTPPassthrough(t *testing.T) {
	factory := stubFactory(stubIdentity{
		wantOTP: "654321",
		ticket:  "tkt-1", csrf: "csrf-1",
		perms: pve.Permissions{"/": {"Sys.Audit": true}},
	})
	svc := newTestService(t, factory)
	ts := newTestServer(t, svc)

	t.Run("correct OTP succeeds", func(t *testing.T) {
		resp, err := http.Post(ts.URL+"/api/v1/auth/login", "application/json",
			bytes.NewReader(loginBody(t, "totp-user", "pw", "pve", "654321")))
		if err != nil {
			t.Fatalf("POST /auth/login: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
	})

	t.Run("missing OTP is rejected as invalid credentials", func(t *testing.T) {
		resp, err := http.Post(ts.URL+"/api/v1/auth/login", "application/json",
			bytes.NewReader(loginBody(t, "totp-user", "pw", "pve", "")))
		if err != nil {
			t.Fatalf("POST /auth/login: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
		assertErrorCode(t, resp, "invalid_credentials")
	})

	t.Run("wrong OTP is rejected as invalid credentials", func(t *testing.T) {
		resp, err := http.Post(ts.URL+"/api/v1/auth/login", "application/json",
			bytes.NewReader(loginBody(t, "totp-user", "pw", "pve", "000000")))
		if err != nil {
			t.Fatalf("POST /auth/login: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
	})
}

func assertErrorCode(t *testing.T, resp *http.Response, want string) {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding error envelope: %v", err)
	}
	if body.Error.Code != want {
		t.Errorf("error code = %q, want %q", body.Error.Code, want)
	}
}

// --- validation -----------------------------------------------------------

func TestHandleLogin_ValidationErrors(t *testing.T) {
	svc := newTestService(t, stubFactory(stubIdentity{}))
	ts := newTestServer(t, svc)

	cases := []struct {
		body map[string]string
		name string
	}{
		{name: "missing username", body: map[string]string{"password": "pw", "realm": "pam"}},
		{name: "missing password", body: map[string]string{"username": "u", "realm": "pam"}},
		{name: "missing realm and no @ in username", body: map[string]string{"username": "u", "password": "pw"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := json.Marshal(tc.body)
			resp, err := http.Post(ts.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(b))
			if err != nil {
				t.Fatalf("POST /auth/login: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", resp.StatusCode)
			}
			assertErrorCode(t, resp, "validation_failed")
		})
	}
}

// --- cookie attributes (T-105 acceptance criterion 2) ----------------------

func TestHandleLogin_SetsCookiesWithDocumentedAttributes(t *testing.T) {
	factory := stubFactory(stubIdentity{
		ticket: "tkt", csrf: "csrf-secret",
		perms: pve.Permissions{"/": {"Sys.Audit": true}},
	})
	svc := newTestService(t, factory)
	ts := newTestServer(t, svc)

	resp, err := http.Post(ts.URL+"/api/v1/auth/login", "application/json",
		bytes.NewReader(loginBody(t, "root", "pw", "pam", "")))
	if err != nil {
		t.Fatalf("POST /auth/login: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	cookies := resp.Cookies()
	var session, csrf *http.Cookie
	for _, c := range cookies {
		switch c.Name {
		case auth.SessionCookieName:
			session = c
		case auth.CSRFCookieName:
			csrf = c
		}
	}
	if session == nil {
		t.Fatal("no vnprox_session cookie set")
	}
	if !session.HttpOnly {
		t.Error("session cookie: HttpOnly = false, want true")
	}
	if !session.Secure {
		t.Error("session cookie: Secure = false, want true")
	}
	if session.SameSite != http.SameSiteStrictMode {
		t.Errorf("session cookie: SameSite = %v, want Strict", session.SameSite)
	}
	if session.Value == "" {
		t.Error("session cookie value is empty")
	}

	if csrf == nil {
		t.Fatal("no vnprox_csrf cookie set")
	}
	if csrf.HttpOnly {
		t.Error("csrf cookie: HttpOnly = true, want false (must be JS-readable for double-submit)")
	}
	if !csrf.Secure {
		t.Error("csrf cookie: Secure = false, want true")
	}
	if csrf.SameSite != http.SameSiteStrictMode {
		t.Errorf("csrf cookie: SameSite = %v, want Strict", csrf.SameSite)
	}
	if csrf.Value != "csrf-secret" {
		t.Errorf("csrf cookie value = %q, want the session's CSRF token", csrf.Value)
	}
}

// --- CSRF double-submit (T-105 acceptance criterion 2) ---------------------

func TestCSRF_MutatingRequestWithoutHeaderIs403(t *testing.T) {
	factory := stubFactory(stubIdentity{
		ticket: "tkt", csrf: "csrf-secret",
		perms: pve.Permissions{"/": {"Sys.Audit": true}},
	})
	svc := newTestService(t, factory)
	ts := newTestServer(t, svc)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	client := &http.Client{Jar: jar}

	loginResp, err := client.Post(ts.URL+"/api/v1/auth/login", "application/json",
		bytes.NewReader(loginBody(t, "root", "pw", "pam", "")))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	_ = loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", loginResp.StatusCode)
	}

	// POST /auth/logout without X-VNPROX-CSRF: must be rejected before it
	// destroys the session.
	logoutReq, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/auth/logout", nil)
	if err != nil {
		t.Fatalf("building logout request: %v", err)
	}
	resp, err := client.Do(logoutReq)
	if err != nil {
		t.Fatalf("logout without CSRF header: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	assertErrorCode(t, resp, "csrf_required")

	// The session must still be valid afterwards (CSRF rejection is not a
	// pass-through to the handler that would have deleted it).
	meResp, err := client.Get(ts.URL + "/api/v1/auth/me")
	if err != nil {
		t.Fatalf("GET /auth/me: %v", err)
	}
	defer func() { _ = meResp.Body.Close() }()
	if meResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /auth/me after rejected logout: status = %d, want 200 (session should still be valid)", meResp.StatusCode)
	}
}

func TestCSRF_MutatingRequestWithCorrectHeaderSucceeds(t *testing.T) {
	factory := stubFactory(stubIdentity{
		ticket: "tkt", csrf: "csrf-secret",
		perms: pve.Permissions{"/": {"Sys.Audit": true}},
	})
	svc := newTestService(t, factory)
	ts := newTestServer(t, svc)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	loginResp, err := client.Post(ts.URL+"/api/v1/auth/login", "application/json",
		bytes.NewReader(loginBody(t, "root", "pw", "pam", "")))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	_ = loginResp.Body.Close()

	logoutReq, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/auth/logout", nil)
	if err != nil {
		t.Fatalf("building logout request: %v", err)
	}
	logoutReq.Header.Set(auth.CSRFHeaderName, "csrf-secret")
	resp, err := client.Do(logoutReq)
	if err != nil {
		t.Fatalf("logout with CSRF header: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}

	meResp, err := client.Get(ts.URL + "/api/v1/auth/me")
	if err != nil {
		t.Fatalf("GET /auth/me: %v", err)
	}
	defer func() { _ = meResp.Body.Close() }()
	if meResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /auth/me after logout: status = %d, want 401", meResp.StatusCode)
	}
}

// --- full login/logout/me cycle (T-105 acceptance criterion 1) ------------

func TestLoginLogoutMeCycle(t *testing.T) {
	factory := stubFactory(stubIdentity{
		ticket: "tkt", csrf: "csrf-secret",
		perms: pve.Permissions{"/": {"Sys.Audit": true, "Sys.Modify": true}},
		nodes: []string{"pve1", "pve2"},
	})
	svc := newTestService(t, factory)
	ts := newTestServer(t, svc)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	loginResp, err := client.Post(ts.URL+"/api/v1/auth/login", "application/json",
		bytes.NewReader(loginBody(t, "root", "pw", "pam", "")))
	if err != nil {
		t.Fatalf("login: %v", err)
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
		t.Fatalf("login user = %+v, want {root pam}", loginBody.User)
	}
	for _, node := range []string{"pve1", "pve2"} {
		if !loginBody.Caps[node].NetWrite {
			t.Errorf("caps[%s].NetWrite = false, want true", node)
		}
	}

	meResp, err := client.Get(ts.URL + "/api/v1/auth/me")
	if err != nil {
		t.Fatalf("GET /auth/me: %v", err)
	}
	if meResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /auth/me status = %d, want 200", meResp.StatusCode)
	}
	_ = meResp.Body.Close()

	logoutReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/auth/logout", nil)
	logoutReq.Header.Set(auth.CSRFHeaderName, "csrf-secret")
	logoutResp, err := client.Do(logoutReq)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	_ = logoutResp.Body.Close()
	if logoutResp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", logoutResp.StatusCode)
	}

	meAfterLogout, err := client.Get(ts.URL + "/api/v1/auth/me")
	if err != nil {
		t.Fatalf("GET /auth/me after logout: %v", err)
	}
	defer func() { _ = meAfterLogout.Body.Close() }()
	if meAfterLogout.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /auth/me after logout: status = %d, want 401", meAfterLogout.StatusCode)
	}
}

// --- rate limiting (T-105 acceptance criterion 4) --------------------------

// doLoginFrom drives one POST /auth/login directly against r (bypassing a
// real TCP listener) with a caller-chosen RemoteAddr, so the per-IP bucket
// can be exercised precisely — a real httptest.Server would have every
// request arrive from 127.0.0.1 regardless of which "attacker"/"good user"
// it simulates, since they all originate from the same test process.
func doLoginFrom(t *testing.T, r http.Handler, remoteAddr string, body []byte) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remoteAddr
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	return rr.Result()
}

// bruteForceFactory fails Login for every username except goodUsername
// (simulating an attacker guessing passwords for one account while a
// legitimate, different user logs in successfully elsewhere) — a single
// static stubIdentity template can't express this per-username branching,
// since stubFactory always returns a copy of the same template regardless
// of who's logging in.
func bruteForceFactory(goodUsername string) auth.IdentityFactory {
	return func(username, _, _, _ string) (auth.PVEIdentity, error) {
		if username == goodUsername {
			return &stubIdentity{ticket: "tkt", csrf: "csrf", perms: pve.Permissions{"/": {"Sys.Audit": true}}}, nil
		}
		// wantOTP set to a value no submitted otp ("") can ever match, so
		// Login always fails with *pve.ErrPVEAuth — simulating bad
		// credentials for every other username.
		return &stubIdentity{wantOTP: "never-matches"}, nil
	}
}

func TestRateLimiter_TenRapidBadLoginsThen429WithAuditEntries(t *testing.T) {
	sessions, audit, _ := newTestStore(t)
	factory := bruteForceFactory("gooduser")
	svc, err := auth.NewService(auth.Config{
		Sessions:    sessions,
		Audit:       audit,
		NewIdentity: factory,
		RateLimit:   auth.RateLimitConfig{Capacity: 10, RefillEvery: time.Hour},
	})
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) { svc.MountRoutes(r) })

	const attackerIP = "10.0.0.9:54321"
	body := loginBody(t, "attacker", "wrong", "pam", "")
	for i := 0; i < 10; i++ {
		resp := doLoginFrom(t, r, attackerIP, body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401 (bad credentials, not yet rate limited)", i, resp.StatusCode)
		}
	}

	resp := doLoginFrom(t, r, attackerIP, body)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("11th attempt: status = %d, want 429", resp.StatusCode)
	}
	assertErrorCode(t, resp, "rate_limited")

	entries, err := audit.List(context.Background(), "", 0)
	if err != nil {
		t.Fatalf("listing audit entries: %v", err)
	}
	var denied, rateLimited int
	for _, e := range entries {
		if e.Action != "login" {
			continue
		}
		switch e.Result {
		case "denied":
			denied++
		case "rate_limited":
			rateLimited++
		}
	}
	if denied != 10 {
		t.Errorf("audited 'denied' login entries = %d, want 10", denied)
	}
	if rateLimited != 1 {
		t.Errorf("audited 'rate_limited' login entries = %d, want 1", rateLimited)
	}

	// A good login from a different IP, through the SAME service/limiter,
	// must be unaffected by the attacker IP's exhaustion. The stub's
	// wantOTP is empty ("no OTP required") and its Login never errors, so
	// this attempt succeeds.
	goodResp := doLoginFrom(t, r, "10.0.0.99:11111", loginBody(t, "gooduser", "correct", "pam", ""))
	defer func() { _ = goodResp.Body.Close() }()
	if goodResp.StatusCode != http.StatusOK {
		t.Fatalf("good login from a different IP: status = %d, want 200", goodResp.StatusCode)
	}
}
