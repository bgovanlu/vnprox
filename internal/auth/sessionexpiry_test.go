// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/auth"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// fakeClock is a mutable, mutex-guarded clock for tests that need to
// deterministically cross docs/security.md's session idle/hard-cap
// boundaries ("vnprox sessions idle out at 2h, hard cap 12h") without
// actually sleeping for hours — this claim had no automated test at all
// before T-604's security hardening pass found the gap (SessionMiddleware's
// idle-slide and hard-cap logic, middleware.go, was previously exercised
// only indirectly, never at its actual expiry boundaries).
type fakeClock struct {
	now time.Time
	mu  sync.Mutex
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newExpirySvc(t *testing.T, clock *fakeClock, idle, hard time.Duration) (svc *auth.Service, r http.Handler) {
	t.Helper()
	sessions, audit, _ := newTestStore(t)
	id := &controllableIdentity{
		ticket: "tkt-1", csrf: "csrf-1",
		perms: pve.Permissions{"/": {"Sys.Audit": true}},
	}
	factory := func(_, _, _, _ string) (auth.PVEIdentity, error) { return id, nil }

	svc, err := auth.NewService(auth.Config{
		Sessions:    sessions,
		Audit:       audit,
		NewIdentity: factory,
		IdleTimeout: idle,
		HardTimeout: hard,
		Now:         clock.Now,
	})
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	router := chi.NewRouter()
	router.Route("/api/v1", func(r chi.Router) { svc.MountRoutes(r) })
	return svc, router
}

func getMe(t *testing.T, r http.Handler, sessionID string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sessionID})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec.Code
}

// TestSessionMiddleware_IdleTimeoutExpiresSession is docs/security.md's
// "vnprox sessions idle out at 2h" claim, on a short configured idle
// timeout: no request at all for longer than the idle window must expire
// the session, even though the session is well within its hard cap.
func TestSessionMiddleware_IdleTimeoutExpiresSession(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	// Session expiry is stored (store.Session.ExpiresAt) as whole Unix
	// seconds, so idle/hard durations and clock advances both need to be
	// second-granular — a sub-second timeout would round-trip through
	// time.Time.Unix() truncation and never appear to lapse at all.
	_, r := newExpirySvc(t, clock, 2*time.Second, time.Hour)

	sessionID, _ := loginViaHandler(t, r, "someone", "pw", "pam")

	if code := getMe(t, r, sessionID); code != http.StatusOK {
		t.Fatalf("GET /auth/me immediately after login: status = %d, want 200", code)
	}

	// Advance well past the idle timeout without any intervening request
	// (a request would slide the deadline forward — this is deliberately
	// simulating silence, not activity).
	clock.Advance(5 * time.Second)

	if code := getMe(t, r, sessionID); code != http.StatusUnauthorized {
		t.Errorf("GET /auth/me after the idle timeout elapsed: status = %d, want 401", code)
	}
}

// TestSessionMiddleware_IdleTimeoutSlidesOnActivity proves the "idle" half
// of "idle out at 2h" actually means idle — continued activity within the
// idle window keeps sliding the deadline forward indefinitely (until the
// hard cap, covered separately below), rather than the session expiring on
// a fixed schedule regardless of use.
func TestSessionMiddleware_IdleTimeoutSlidesOnActivity(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	// Second-granular, per ExpiresAt's storage precision — see the sibling
	// idle-expiry test's doc comment.
	idle := 3 * time.Second
	_, r := newExpirySvc(t, clock, idle, 10*time.Hour)

	sessionID, _ := loginViaHandler(t, r, "someone", "pw", "pam")

	// Poll well past what a single idle window would allow, but always
	// within the idle window of the previous request, so each request
	// slides the deadline forward before it can lapse.
	for i := 0; i < 5; i++ {
		clock.Advance(2 * time.Second)
		if code := getMe(t, r, sessionID); code != http.StatusOK {
			t.Fatalf("GET /auth/me on poll %d (steady activity within the idle window): status = %d, want 200", i, code)
		}
	}
}

// TestSessionMiddleware_HardCapExpiresSessionDespiteActivity is docs/
// security.md's "hard cap 12h" claim: even continuous activity that keeps
// sliding the idle deadline forward cannot keep a session alive past its
// hard cap from creation — the hard cap is an absolute ceiling, not just a
// longer idle timeout.
func TestSessionMiddleware_HardCapExpiresSessionDespiteActivity(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	idle := time.Hour       // generous idle window...
	hard := 3 * time.Second // ...but a tight hard cap from creation (second-granular; see above).
	_, r := newExpirySvc(t, clock, idle, hard)

	sessionID, _ := loginViaHandler(t, r, "someone", "pw", "pam")

	// Steady activity well inside the idle window (so idle-timeout alone
	// would never expire this session), advancing one second at a time
	// until crossing the 3s hard cap.
	for i := 1; i <= 5; i++ {
		clock.Advance(time.Second)
		code := getMe(t, r, sessionID)
		if i <= 3 {
			if code != http.StatusOK {
				t.Fatalf("GET /auth/me at t+%ds (still within the %s hard cap): status = %d, want 200", i, hard, code)
			}
			continue
		}
		if code != http.StatusUnauthorized {
			t.Errorf("GET /auth/me at t+%ds (past the %s hard cap despite steady activity): status = %d, want 401", i, hard, code)
		}
	}
}
