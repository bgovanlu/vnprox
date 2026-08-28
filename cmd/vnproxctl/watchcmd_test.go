// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

// newFakeVnproxdWS builds a plain (non-TLS) test server standing in for
// vnproxd's /api/v1 + /api/ws surface — plain HTTP rather than
// newFakeVnproxd's TLS variant, since what's under test here is the WS
// upgrade/dial path itself, not TLS handling (which remote_test.go's
// existing suite already covers for the /api/v1 half). --insecure defaults
// true regardless, so the TLS transport config is inert against a plain
// server.
func newFakeVnproxdWS(t *testing.T, mux *http.ServeMux) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func meHandler(automation bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"user": map[string]string{"username": "alice", "realm": "pve"},
			"caps": map[string]any{"": map[string]bool{
				"netRead": true, "automation": automation,
			}},
		})
	}
}

// TestRunWatch_RendersEventsNonTTY drives a real WS round trip: the daemon
// pushes two "events"-topic messages, `vnproxctl watch` renders both as
// plain (non-color, since *bytes.Buffer is never a terminal) lines and
// exits cleanly once --max-events is reached.
func TestRunWatch_RendersEventsNonTTY(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/auth/me", meHandler(true))
	mux.HandleFunc("/api/ws", func(w http.ResponseWriter, r *http.Request) {
		requireBearerToken(t, r, "tok")
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("websocket.Accept: %v", err)
			return
		}
		ctx := context.Background()
		_, _, _ = c.Read(ctx) // drain the {"subscribe":["events"]} message
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"event":"changeset.status","id":"cs1","status":"applied"}`))
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"event":"drift.changed","count":3}`))
		_, _, _ = c.Read(ctx) // wait for the client's close frame
		_ = c.Close(websocket.StatusNormalClosure, "")
	})
	srv := newFakeVnproxdWS(t, mux)

	var stdout, stderr bytes.Buffer
	code := run([]string{"watch", "--url", srv.URL + "/api/v1", "--token", "tok", "--max-events", "2"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want ExitSuccess (stderr: %s)", code, stderr.String())
	}

	out := stdout.String()
	if strings.Contains(out, "\x1b[") {
		t.Errorf("non-TTY output contains an ANSI escape code: %q", out)
	}
	if !strings.Contains(out, "changeset.status") || !strings.Contains(out, "id=cs1") || !strings.Contains(out, "status=applied") {
		t.Errorf("stdout missing changeset.status fields: %q", out)
	}
	if !strings.Contains(out, "drift.changed") || !strings.Contains(out, "count=3") {
		t.Errorf("stdout missing drift.changed fields: %q", out)
	}
}

// TestRunWatch_KindFilter proves --kind drops events whose "event" name
// isn't in the filter, without touching --max-events' count (a filtered
// event must never count toward the limit).
func TestRunWatch_KindFilter(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/auth/me", meHandler(true))
	mux.HandleFunc("/api/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("websocket.Accept: %v", err)
			return
		}
		ctx := context.Background()
		_, _, _ = c.Read(ctx)
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"event":"changeset.status","id":"cs1","status":"applying"}`))
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"event":"drift.changed","count":7}`))
		_, _, _ = c.Read(ctx)
		_ = c.Close(websocket.StatusNormalClosure, "")
	})
	srv := newFakeVnproxdWS(t, mux)

	var stdout, stderr bytes.Buffer
	code := run([]string{"watch", "--url", srv.URL + "/api/v1", "--token", "tok", "--kind", "drift.changed", "--max-events", "1"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want ExitSuccess (stderr: %s)", code, stderr.String())
	}
	out := stdout.String()
	if strings.Contains(out, "changeset.status") {
		t.Errorf("--kind=drift.changed should have filtered out changeset.status: %q", out)
	}
	if !strings.Contains(out, "drift.changed") || !strings.Contains(out, "count=7") {
		t.Errorf("stdout missing the allowed drift.changed event: %q", out)
	}
}

// TestRunWatch_ReconnectsAndShowsGap simulates a daemon restart: the first
// WS connection is dropped abruptly (no close handshake) after one event,
// and the second accepted connection delivers the second. The command must
// transparently reconnect (not exit/error) and the gap must be visible in
// the rendered output, not silently swallowed.
func TestRunWatch_ReconnectsAndShowsGap(t *testing.T) {
	var connNum int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/auth/me", meHandler(true))
	mux.HandleFunc("/api/ws", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&connNum, 1)
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("websocket.Accept: %v", err)
			return
		}
		ctx := context.Background()
		_, _, _ = c.Read(ctx)
		if n == 1 {
			_ = c.Write(ctx, websocket.MessageText, []byte(`{"event":"drift.changed","count":1}`))
			_ = c.CloseNow() // simulate a dropped connection, no close handshake
			return
		}
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"event":"drift.changed","count":2}`))
		_, _, _ = c.Read(ctx)
		_ = c.Close(websocket.StatusNormalClosure, "")
	})
	srv := newFakeVnproxdWS(t, mux)

	var stdout, stderr bytes.Buffer
	code := run([]string{"watch", "--url", srv.URL + "/api/v1", "--token", "tok", "--max-events", "2"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want ExitSuccess (stderr: %s)", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "count=1") || !strings.Contains(out, "count=2") {
		t.Fatalf("expected both events across the reconnect, got: %q", out)
	}
	if !strings.Contains(out, "connection lost") || !strings.Contains(out, "connection restored") {
		t.Errorf("the gap must be visible in the output, got: %q", out)
	}
	if atomic.LoadInt32(&connNum) < 2 {
		t.Errorf("expected at least 2 WS connections (a reconnect), got %d", connNum)
	}
}

// TestRunWatch_NDJSONShape pins -o json's newline-delimited shape: every
// line independently decodes as JSON, a "status" line announces the
// connection, and an "event" line passes the wire event's fields through
// verbatim under a "type":"event" tag.
func TestRunWatch_NDJSONShape(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/auth/me", meHandler(true))
	mux.HandleFunc("/api/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("websocket.Accept: %v", err)
			return
		}
		ctx := context.Background()
		_, _, _ = c.Read(ctx)
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"event":"drift.changed","count":5}`))
		_, _, _ = c.Read(ctx)
		_ = c.Close(websocket.StatusNormalClosure, "")
	})
	srv := newFakeVnproxdWS(t, mux)

	var stdout, stderr bytes.Buffer
	code := run([]string{"watch", "--url", srv.URL + "/api/v1", "--token", "tok", "--max-events", "1", "-o", "json"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want ExitSuccess (stderr: %s)", code, stderr.String())
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("want at least a status line and an event line, got %d lines: %q", len(lines), stdout.String())
	}
	sawStatus, sawEvent := false, false
	for _, line := range lines {
		var decoded map[string]any
		if err := json.Unmarshal([]byte(line), &decoded); err != nil {
			t.Fatalf("line %q is not valid JSON: %v", line, err)
		}
		switch decoded["type"] {
		case "status":
			sawStatus = true
		case "event":
			sawEvent = true
			if decoded["event"] != "drift.changed" {
				t.Errorf("event line = %+v, want event=drift.changed", decoded)
			}
			if decoded["count"] != float64(5) {
				t.Errorf("event line = %+v, want count=5", decoded)
			}
		default:
			t.Errorf("line %q has unexpected/missing \"type\"", line)
		}
	}
	if !sawStatus || !sawEvent {
		t.Errorf("want both a status and an event line, sawStatus=%v sawEvent=%v", sawStatus, sawEvent)
	}
}

// TestRunWatch_MissingAutomationScopeFailsFast is AC3: a token that lacks
// the "automation" scope must produce a clear error and non-zero exit
// without ever dialing /api/ws — caught at the GET /auth/me preflight,
// since the WS protocol itself is ack-less (a refused "events" subscribe
// is silently dropped, never rejected on the wire).
func TestRunWatch_MissingAutomationScopeFailsFast(t *testing.T) {
	dialed := false
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/auth/me", meHandler(false))
	mux.HandleFunc("/api/ws", func(w http.ResponseWriter, r *http.Request) {
		dialed = true
		c, err := websocket.Accept(w, r, nil)
		if err == nil {
			_ = c.Close(websocket.StatusNormalClosure, "")
		}
	})
	srv := newFakeVnproxdWS(t, mux)

	var stdout, stderr bytes.Buffer
	code := run([]string{"watch", "--url", srv.URL + "/api/v1", "--token", "tok"}, &stdout, &stderr)
	if code != ExitAuth {
		t.Fatalf("exit code = %d, want ExitAuth (stderr: %s)", code, stderr.String())
	}
	if dialed {
		t.Error("no /api/ws dial should have been attempted without the automation scope")
	}
	if !strings.Contains(stderr.String(), "automation") {
		t.Errorf("stderr should name the missing scope, got: %q", stderr.String())
	}
}

func TestRunWatch_NoTokenFailsFastAuthExitCode(t *testing.T) {
	dialed := false
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/auth/me", func(w http.ResponseWriter, r *http.Request) { dialed = true })
	mux.HandleFunc("/api/ws", func(w http.ResponseWriter, r *http.Request) { dialed = true })
	srv := newFakeVnproxdWS(t, mux)

	t.Setenv("VNPROX_TOKEN", "")
	var stdout, stderr bytes.Buffer
	code := run([]string{"watch", "--url", srv.URL + "/api/v1"}, &stdout, &stderr)
	if code != ExitAuth {
		t.Fatalf("exit code = %d, want ExitAuth", code)
	}
	if dialed {
		t.Error("no daemon call should have been attempted without a token")
	}
}

// --- unit tests for the pure helpers ---

func TestResolveWatchWSURL(t *testing.T) {
	cases := []struct {
		url, want string
	}{
		{"https://pve1:8007", "wss://pve1:8007/api/ws"},
		{"http://pve1:8007", "ws://pve1:8007/api/ws"},
		{"https://pve1:8007/api/v1", "wss://pve1:8007/api/ws"},
	}
	for _, c := range cases {
		fs := addRemoteFlagsForTest(c.url)
		got, err := resolveWatchWSURL(fs)
		if err != nil {
			t.Fatalf("resolveWatchWSURL(%q): %v", c.url, err)
		}
		if got != c.want {
			t.Errorf("resolveWatchWSURL(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

// addRemoteFlagsForTest builds a minimal *remoteFlags with only --url set,
// for resolveWatchWSURL's unit tests (which never touch --config/--token).
func addRemoteFlagsForTest(url string) *remoteFlags {
	u := url
	return &remoteFlags{url: &u}
}

func TestParseKindFilter(t *testing.T) {
	if f := parseKindFilter(""); f != nil {
		t.Errorf("parseKindFilter(\"\") = %+v, want nil (no filter)", f)
	}
	f := parseKindFilter("drift.changed, findings.changed ,")
	if !f["drift.changed"] || !f["findings.changed"] || len(f) != 2 {
		t.Errorf("parseKindFilter = %+v, want {drift.changed, findings.changed}", f)
	}
}

func TestKindAllowed(t *testing.T) {
	if !kindAllowed(nil, "anything.new") {
		t.Error("a nil filter must allow every event name, including one this build doesn't recognize (additive-only freeze)")
	}
	f := map[string]bool{"drift.changed": true}
	if !kindAllowed(f, "drift.changed") {
		t.Error("drift.changed should be allowed")
	}
	if kindAllowed(f, "audit.appended") {
		t.Error("audit.appended should be filtered out")
	}
}

func TestWatchBackoff_MonotonicAndCapped(t *testing.T) {
	prevMax := time.Duration(0)
	for attempt := 1; attempt <= 10; attempt++ {
		d := watchBackoff(attempt)
		if d <= 0 {
			t.Fatalf("watchBackoff(%d) = %v, want > 0", attempt, d)
		}
		if d > watchMaxBackoff {
			t.Fatalf("watchBackoff(%d) = %v, want <= %v", attempt, d, watchMaxBackoff)
		}
		_ = prevMax
	}
}

// TestEventRenderer_ColorVsPlain proves the TTY/non-TTY rendering
// difference is exactly ANSI escapes — same fields, same order — by
// exercising eventRenderer directly rather than needing a real pty.
func TestEventRenderer_ColorVsPlain(t *testing.T) {
	evt := map[string]any{"event": "changeset.status", "id": "cs1", "status": "applied"}

	var colorBuf bytes.Buffer
	rc := newEventRenderer(&colorBuf, true, false)
	rc.event(evt)
	if !strings.Contains(colorBuf.String(), "\x1b[") {
		t.Errorf("color renderer output has no ANSI escape: %q", colorBuf.String())
	}

	var plainBuf bytes.Buffer
	rp := newEventRenderer(&plainBuf, false, false)
	rp.event(evt)
	if strings.Contains(plainBuf.String(), "\x1b[") {
		t.Errorf("plain renderer output has an ANSI escape: %q", plainBuf.String())
	}
	if !strings.Contains(plainBuf.String(), "id=cs1") || !strings.Contains(plainBuf.String(), "status=applied") {
		t.Errorf("plain renderer missing fields: %q", plainBuf.String())
	}
}

func TestIsTerminalWriter_BufferIsNeverATerminal(t *testing.T) {
	var buf bytes.Buffer
	if isTerminalWriter(&buf) {
		t.Error("a *bytes.Buffer must never be reported as a terminal")
	}
}
