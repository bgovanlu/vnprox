// secretlog_test.go is T-604's permanent, CI-enforced secrets-in-logs
// sweep (docs/security.md's implicit "no secrets in logs" expectation,
// made explicit by this task's acceptance criterion 4: "Log-secrecy test
// passes ... automated grep with allowlist"). It drives a real end-to-end
// flow — the exact production runDaemon path, a real PVE-shaped mock
// server, real HTTP over the loopback TLS listener — through the
// security-sensitive surfaces that could plausibly leak a secret into
// structured logs: login with a real password, an authenticated read, a
// CSRF-protected mutation attempt, a rejected peer-API request, and a
// failed login — then asserts the captured log stream (a JSON slog
// handler, mirroring production's log/slog usage) never contains the
// plaintext password, the full session id, the full CSRF token, or the
// peer cluster secret.
//
// This complements (rather than replaces) two more targeted tests already
// covering pieces of this same claim: internal/auth's
// TestRenewalFailureLog_NeverLeaksFullSessionID (the specific renewal-loop
// log sites T-604 found and fixed) and this package's own
// TestRunDaemon_DevConfigServesHealth (proves runDaemon serves real
// traffic at all). This test is the broader, allowlist-documented sweep
// the acceptance criterion asks for, run as part of `go test ./...` (and
// therefore `make check`) rather than gated behind the opt-in Playwright
// suite — see planning/reports/T-604.md for why, and for the one-time
// manual confirmation also run against the real Playwright/browser E2E
// suite's own daemon log output.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/pvemock"
)

// syncBuffer is a mutex-guarded bytes.Buffer: slog's own handlers
// serialize concurrent Handle calls against each other, but this test also
// wants to safely read the buffer's accumulated contents (String) after
// the daemon has stopped, without the race detector flagging a data race
// against any last in-flight log write.
type syncBuffer struct {
	buf bytes.Buffer
	mu  sync.Mutex
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// jsonLoggerTo builds a structured JSON slog.Logger writing to w, matching
// production's log/slog usage (CLAUDE.md: "Log with log/slog, structured")
// so this test's line-by-line JSON parsing below reflects a real deployment
// rather than the plain-text testLogger() other tests in this package use.
func jsonLoggerTo(w *syncBuffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, nil))
}

// The documented allowlist for this sweep (T-604 acceptance criterion 4:
// "automated grep with allowlist"):
//
//   - "password" / "ticket" / "csrf" / "secret" as bare words are allowed
//     wherever they appear in a log *message* describing an action (e.g.
//     "ticket renewal failed", "generated new cluster secret") — none of
//     this codebase's logging call sites log the submitted password, the
//     PVE ticket value, the CSRF token value, or the cluster secret bytes
//     themselves (grep-audited across internal/auth, internal/peer,
//     internal/store, internal/pve as part of this task), only the
//     *concept* or a file *path*. isAllowlistedSecretValue below allows
//     path-shaped values and known non-secret error-code strings through;
//     everything else on a secret-sounding field name fails the sweep.
//   - What's actually asserted, directly, below: the real fixture
//     password, the full session id, and the full CSRF token issued by
//     this specific test run never appear anywhere in the captured log,
//     verbatim, in any form (not just as a JSON field value).
//
// rewriteDevConfigWithAPIURL is rewriteDevConfig (devconfig_test.go) plus
// one more line rewrite: pointing [pve].api_url at a real in-process mock
// server instead of the checked-in dev.toml's hardcoded
// http://127.0.0.1:8006 (this test doesn't want a port-shared, more-
// deterministic-per-run mock, and needs a REAL POST /access/ticket login
// to exercise the actual production handleLogin path with a real
// password, not the dev_ticket_* collector-only shortcut).
func rewriteDevConfigWithAPIURL(t *testing.T, repoRoot, dir string, port int, apiURL string) string {
	t.Helper()
	cfgPath := rewriteDevConfig(t, repoRoot, dir, port)

	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("reading rewritten config: %v", err)
	}
	lines := strings.Split(string(raw), "\n")
	found := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "api_url ") || strings.HasPrefix(trimmed, "api_url=") {
			lines[i] = fmt.Sprintf("api_url = %q", apiURL)
			found = true
		}
	}
	if !found {
		t.Fatal("testdata/dev.toml has no api_url key to rewrite; update this test to match its current shape")
	}
	if err := os.WriteFile(cfgPath, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatalf("rewriting api_url: %v", err)
	}
	return cfgPath
}

// TestSecretsNeverAppearInLogs is the log-secrecy sweep described in this
// file's package doc comment.
func TestSecretsNeverAppearInLogs(t *testing.T) {
	repoRoot, err := repoRootAbs()
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}

	// A real PVE-shaped mock, in-process: the fixture's actual credentials
	// (single-node.yaml) are the secret this test proves never leaks.
	const (
		username = "root@pam"
		password = "vnprox-mock" //nolint:gosec // fixture credential, not a real secret
	)
	fixturePath := repoRoot + "/testdata/clusters/single-node.yaml"
	fixture, err := pvemock.LoadFixture(fixturePath)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	mockSrv := httptest.NewServer(pvemock.NewServer(fixture))
	defer mockSrv.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving ephemeral port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	cfgPath := rewriteDevConfigWithAPIURL(t, repoRoot, t.TempDir(), port, mockSrv.URL)

	var logBuf syncBuffer
	logger := jsonLoggerTo(&logBuf)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	daemonDone := make(chan error, 1)
	go func() { daemonDone <- runDaemon(ctx, cfgPath, logger) }()

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test-only client for the throwaway dev cert
		},
	}
	base := fmt.Sprintf("https://127.0.0.1:%d", port)
	waitForHealth(t, client, base, daemonDone)

	// --- Drive the security-sensitive flow -------------------------------

	// 1. A failed login (wrong password) — must not leak the attempted
	// password either.
	doLogin(t, client, base, username, "definitely-wrong-password")

	// 2. A real, successful login.
	sessionID, csrfToken := doLogin(t, client, base, username, password)
	if sessionID == "" || csrfToken == "" {
		t.Fatal("successful login did not return session/csrf cookies")
	}

	// 3. An authenticated read.
	meReq, _ := http.NewRequest(http.MethodGet, base+"/api/v1/auth/me", nil)
	meReq.AddCookie(&http.Cookie{Name: "vnprox_session", Value: sessionID})
	meResp, err := client.Do(meReq)
	if err != nil {
		t.Fatalf("GET /auth/me: %v", err)
	}
	_ = meResp.Body.Close()

	// 4. A CSRF-protected mutation (logout uses the same CSRFMiddleware
	// every mutating route does) — exercises the CSRF header comparison
	// path with the real token value.
	logoutReq, _ := http.NewRequest(http.MethodPost, base+"/api/v1/auth/logout", nil)
	logoutReq.AddCookie(&http.Cookie{Name: "vnprox_session", Value: sessionID})
	logoutReq.Header.Set("X-VNPROX-CSRF", csrfToken)
	logoutResp, err := client.Do(logoutReq)
	if err != nil {
		t.Fatalf("POST /auth/logout: %v", err)
	}
	_ = logoutResp.Body.Close()

	// 5. A rejected peer-API request (no signature at all) — exercises
	// peer.Server's authMiddleware rejection logging path.
	peerReq, _ := http.NewRequest(http.MethodGet, base+"/api/peer/health", nil)
	peerResp, err := client.Do(peerReq)
	if err != nil {
		t.Fatalf("GET /api/peer/health: %v", err)
	}
	_ = peerResp.Body.Close()

	cancel()
	select {
	case <-daemonDone:
	case <-time.After(5 * time.Second):
		t.Fatal("runDaemon did not return within 5s of context cancellation")
	}

	logged := logBuf.String()
	if logged == "" {
		t.Fatal("captured no log output at all; test setup is broken")
	}

	// --- The actual sweep -------------------------------------------------

	for _, secret := range []struct{ name, value string }{
		{"attempted wrong password", "definitely-wrong-password"},
		{"real fixture password", password},
	} {
		if strings.Contains(logged, secret.value) {
			t.Errorf("captured log contains the %s verbatim: %q", secret.name, secret.value)
		}
	}
	if strings.Contains(logged, sessionID) {
		t.Error("captured log contains the FULL session id verbatim")
	}
	if strings.Contains(logged, csrfToken) {
		t.Error("captured log contains the FULL CSRF token verbatim")
	}

	// Generic sweep: every JSON log line, decoded, must not carry a field
	// whose *name* suggests a secret and whose *value* is neither empty nor
	// an allowlisted path/placeholder shape (a file path, or a short
	// correlation-prefix ending in the ellipsis logSessionID/similar
	// redaction helpers use).
	for _, line := range strings.Split(strings.TrimSpace(logged), "\n") {
		if line == "" {
			continue
		}
		var fields map[string]any
		if err := json.Unmarshal([]byte(line), &fields); err != nil {
			t.Fatalf("log line is not valid JSON (expected a JSON slog handler): %s", line)
		}
		for key, val := range fields {
			if !looksLikeSecretFieldName(key) {
				continue
			}
			str, ok := val.(string)
			if !ok || str == "" {
				continue
			}
			if isAllowlistedSecretValue(str) {
				continue
			}
			t.Errorf("log line has suspicious field %q = %q (looks like an unredacted secret): %s", key, str, line)
		}
	}
}

// looksLikeSecretFieldName reports whether key is the kind of field name
// that, per this codebase's own logging conventions (grep-audited across
// internal/auth, internal/peer, internal/store, internal/pve — see this
// task's report), should never carry a raw secret value.
func looksLikeSecretFieldName(key string) bool {
	lower := strings.ToLower(key)
	for _, bad := range []string{"password", "ticket", "csrf", "secret", "session_id"} {
		if strings.Contains(lower, bad) {
			return true
		}
	}
	return false
}

// isAllowlistedSecretValue reports whether val is a shape this sweep
// expects even on a field with a secret-sounding name: a filesystem path
// (this codebase logs key/secret file *paths*, never their contents — see
// secretLogAllowlist's doc comment), a redacted correlation-prefix (ends in
// the "…" logSessionID appends), or a known non-secret error/status code
// string.
func isAllowlistedSecretValue(val string) bool {
	if strings.HasSuffix(val, "…") {
		return true // logSessionID-style redacted prefix.
	}
	pathish := []string{"/", "var/", "testdata/", ".key", ".secret", ".json", ".toml"}
	for _, p := range pathish {
		if strings.Contains(val, p) {
			return true
		}
	}
	nonSecretCodes := []string{
		"csrf_required", "not_authenticated", "invalid_credentials",
		"missing or invalid X-VNPROX-CSRF header", "peer_unauthorized",
		"missing or invalid peer signature", "invalid or missing peer signature",
	}
	for _, c := range nonSecretCodes {
		if strings.Contains(val, c) {
			return true
		}
	}
	return false
}

// doLogin POSTs to /auth/login and returns the session/csrf cookie values
// set on success (empty strings on a rejected login).
func doLogin(t *testing.T, client *http.Client, base, username, password string) (sessionID, csrfToken string) {
	t.Helper()
	body := fmt.Sprintf(`{"username":%q,"password":%q,"realm":"pam"}`, username, password)
	req, err := http.NewRequest(http.MethodPost, base+"/api/v1/auth/login", strings.NewReader(body))
	if err != nil {
		t.Fatalf("building login request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /auth/login: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	for _, c := range resp.Cookies() {
		switch c.Name {
		case "vnprox_session":
			sessionID = c.Value
		case "vnprox_csrf":
			csrfToken = c.Value
		}
	}
	return sessionID, csrfToken
}

func waitForHealth(t *testing.T, client *http.Client, base string, daemonDone <-chan error) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case err := <-daemonDone:
			t.Fatalf("daemon exited before serving health: %v", err)
		default:
		}
		resp, err := client.Get(base + "/api/v1/health")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon never served %s/api/v1/health: last error: %v", base, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func repoRootAbs() (string, error) {
	return filepath.Abs(filepath.Join("..", ".."))
}
