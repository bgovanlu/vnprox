package auth_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/auth"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
	"github.com/bgovanlu/vnprox/internal/store"
)

// countingTicketHandler wraps the mock server and counts POST
// /access/ticket calls, mirroring internal/pve's own renewal_test.go
// helper — the same technique, applied one layer up, to prove a real
// second ticket login happens through this package's renewal loop, not
// just internal/pve's own client-level timer.
type countingTicketHandler struct {
	inner http.Handler
	mu    sync.Mutex
	calls int
}

func (h *countingTicketHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api2/json/access/ticket" {
		h.mu.Lock()
		h.calls++
		h.mu.Unlock()
	}
	h.inner.ServeHTTP(w, r)
}

func (h *countingTicketHandler) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls
}

// TestRenewalLoop_RenewsTicketBeforeShortTTLExpiry is T-105 acceptance
// criterion 5's happy path: a live session's PVE ticket is renewed
// transparently in the background, without any request from the client,
// under a short-TTL fixture config (mirroring internal/pve's own
// TestTicketAuth_RenewsBeforeShortTTLExpiry, one layer up).
func TestRenewalLoop_RenewsTicketBeforeShortTTLExpiry(t *testing.T) {
	f, err := pvemock.LoadFixture(fixtureSingleNode)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	counter := &countingTicketHandler{inner: pvemock.NewServer(f)}
	ts := httptest.NewServer(counter)
	defer ts.Close()

	sessions, audit, _ := newTestStore(t)
	svc, err := auth.NewService(auth.Config{
		Sessions:                 sessions,
		Audit:                    audit,
		NewIdentity:              fixtureIdentityFactory(ts, f, 20*time.Millisecond),
		TicketRenewCheckInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}

	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) { svc.MountRoutes(r) })

	ctx, cancel := context.WithCancel(context.Background())
	loopDone := make(chan error, 1)
	go func() { loopDone <- svc.RunRenewalLoop(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-loopDone
	})

	sessionID, _ := loginViaHandler(t, r, "root", "vnprox-mock", "pam")
	if got := counter.count(); got != 1 {
		t.Fatalf("ticket calls after login = %d, want 1", got)
	}

	// Well within the 20ms renewal threshold: no renewal yet.
	time.Sleep(10 * time.Millisecond)
	if got := counter.count(); got != 1 {
		t.Fatalf("ticket calls before threshold elapsed = %d, want still 1", got)
	}

	// Past the threshold, and past at least one renewal-loop tick: a real
	// renewal must have happened.
	deadline := time.Now().Add(2 * time.Second)
	for counter.count() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := counter.count(); got < 2 {
		t.Fatalf("ticket calls after threshold elapsed = %d, want >= 2 (a renewal should have happened)", got)
	}

	// The session must still be valid (renewal succeeding, not expiring
	// it) — GET /auth/me should still work.
	meReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	meReq.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sessionID})
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, meReq)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /auth/me after renewal: status = %d, want 200", rr.Code)
	}
}

// TestRenewalLoop_FailedRenewalInvalidatesSessionCleanly is T-105
// acceptance criterion 5's failure path: once the underlying PVEIdentity's
// Renew starts failing (simulating the PVE ticket no longer being
// renewable — e.g. the mock/PVE became unreachable, or credentials were
// revoked), the session must be invalidated outright: a subsequent lookup
// must report "not authenticated" (401), never a stale or partially
// updated session.
func TestRenewalLoop_FailedRenewalInvalidatesSessionCleanly(t *testing.T) {
	sessions, audit, _ := newTestStore(t)

	id := &controllableIdentity{
		ticket: "tkt-1", csrf: "csrf-1",
		perms: pve.Permissions{"/": {"Sys.Audit": true}},
	}
	factory := func(_, _, _, _ string) (auth.PVEIdentity, error) { return id, nil }

	svc, err := auth.NewService(auth.Config{
		Sessions:                 sessions,
		Audit:                    audit,
		NewIdentity:              factory,
		TicketRenewCheckInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}

	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) { svc.MountRoutes(r) })

	ctx, cancel := context.WithCancel(context.Background())
	loopDone := make(chan error, 1)
	go func() { loopDone <- svc.RunRenewalLoop(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-loopDone
	})

	sessionID, _ := loginViaHandler(t, r, "someone", "pw", "pam")

	// Session valid immediately after login.
	meReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	meReq.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sessionID})
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, meReq)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /auth/me right after login: status = %d, want 200", rr.Code)
	}

	// Now make every future renewal fail.
	id.mu.Lock()
	id.renewErr = errors.New("simulated: PVE ticket could not be renewed")
	id.mu.Unlock()

	// Wait for the renewal loop to notice and invalidate the session.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := sessions.Get(context.Background(), sessionID); errors.Is(err, store.ErrNotFound) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := sessions.Get(context.Background(), sessionID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("session still present in store after failed renewal, want it deleted (err=%v)", err)
	}

	meReq2 := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	meReq2.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sessionID})
	rr2 := httptest.NewRecorder()
	r.ServeHTTP(rr2, meReq2)
	if rr2.Code != http.StatusUnauthorized {
		t.Fatalf("GET /auth/me after failed renewal: status = %d, want 401 (not authenticated)", rr2.Code)
	}
}

// controllableIdentity is a PVEIdentity whose Renew behavior can be
// flipped to failing mid-test, to exercise the renewal loop's failure
// path deterministically (no need to actually break a real mock server
// mid-test, which would race with other tests sharing it).
type controllableIdentity struct {
	renewErr error
	perms    pve.Permissions
	ticket   string
	csrf     string
	mu       sync.Mutex
}

func (c *controllableIdentity) Login(context.Context) (string, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ticket, c.csrf, nil
}

func (c *controllableIdentity) Renew(context.Context) (string, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.renewErr != nil {
		return "", "", c.renewErr
	}
	return c.ticket, c.csrf, nil
}

func (c *controllableIdentity) Permissions(context.Context) (pve.Permissions, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.perms, nil
}

func (c *controllableIdentity) ClusterNodes(context.Context) ([]string, error) {
	return nil, nil
}

// loginViaHandler drives POST /auth/login directly against r (no real TCP
// listener needed) and returns the session/CSRF cookie values the response
// set.
func loginViaHandler(t *testing.T, r http.Handler, username, password, realm string) (sessionID, csrfToken string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginBody(t, username, password, realm, "")))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	for _, c := range rr.Result().Cookies() {
		switch c.Name {
		case auth.SessionCookieName:
			sessionID = c.Value
		case auth.CSRFCookieName:
			csrfToken = c.Value
		}
	}
	if sessionID == "" {
		t.Fatal("login response set no session cookie")
	}
	return sessionID, csrfToken
}
