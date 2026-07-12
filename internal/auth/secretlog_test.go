package auth_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/auth"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// TestRenewalFailureLog_NeverLeaksFullSessionID is T-604's secrets-in-logs
// sweep for the session id specifically: the id is the bearer credential in
// the vnprox_session cookie (docs/security.md), so any log line containing
// it verbatim would let a log reader (a materially weaker privilege than
// host shell access) hijack that session directly. This drives the exact
// real failure path that used to log the raw id — a renewal failure
// (renewal.go's renewAndRefreshOne, on the "auth: ticket renewal failed,
// invalidating session" line) — through a captured slog.Logger and asserts
// the full id never appears in the captured output, only its short,
// unusable-for-replay prefix (see logSessionID's internal unit test,
// secretlog_internal_test.go, for the truncation contract itself).
func TestRenewalFailureLog_NeverLeaksFullSessionID(t *testing.T) {
	sessions, audit, _ := newTestStore(t)

	id := &controllableIdentity{
		ticket: "tkt-1", csrf: "csrf-1",
		perms: pve.Permissions{"/": {"Sys.Audit": true}},
	}
	factory := func(_, _, _, _ string) (auth.PVEIdentity, error) { return id, nil }

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))

	svc, err := auth.NewService(auth.Config{
		Sessions:                 sessions,
		Audit:                    audit,
		NewIdentity:              factory,
		TicketRenewCheckInterval: 5 * time.Millisecond,
		Logger:                   logger,
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
	if len(sessionID) < 32 {
		t.Fatalf("test session id unexpectedly short (%d chars); the leak this test guards against needs a real-shaped id", len(sessionID))
	}

	// Force every future renewal to fail, so the renewal loop hits the
	// logging call site this test targets.
	id.mu.Lock()
	id.renewErr = errors.New("simulated: PVE ticket could not be renewed")
	id.mu.Unlock()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(logBuf.String(), "ticket renewal failed") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	logged := logBuf.String()
	if !strings.Contains(logged, "ticket renewal failed") {
		t.Fatalf("renewal failure was never logged; test setup is broken. log so far: %s", logged)
	}
	if strings.Contains(logged, sessionID) {
		t.Fatalf("captured log contains the FULL session id verbatim — a log reader could replay it as a stolen session cookie:\n%s", logged)
	}
	// The truncated correlation-handle prefix is expected to still appear
	// (that's the whole point of logSessionID) — its absence would mean the
	// logging call site was silently dropped rather than redacted.
	if !strings.Contains(logged, sessionID[:8]) {
		t.Errorf("expected the redacted id prefix %q to still appear in the log for correlation, got:\n%s", sessionID[:8], logged)
	}

	// Also exercise the request-scoped idle/hard-timeout expiry error path
	// (middleware.go's SessionMiddleware), which logs "session_id" on a
	// failed Update after sliding the expiry — hit indirectly by simply
	// making a bunch of authenticated requests; if it fires it must be
	// redacted exactly like the renewal path.
	for range 5 {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sessionID})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
	}
	if strings.Contains(logBuf.String(), sessionID) {
		t.Fatalf("captured log contains the FULL session id verbatim after authenticated requests:\n%s", logBuf.String())
	}
}
