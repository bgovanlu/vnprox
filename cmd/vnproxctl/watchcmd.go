// SPDX-License-Identifier: Apache-2.0

// watchcmd.go implements `vnproxctl watch` (T-4010): a live terminal view
// over the existing WS `"events"` topic (internal/topology/hub.go, T-1104,
// frozen at D10 — see docs/adr/0010-platform-api-freeze-at-v3-0.md). This
// command is a CONSUMER of that frozen envelope only: it decodes whatever
// `{"event": "<name>", ...payload}` object arrives, filters and renders it,
// and never adds a field, a topic, or an event name. Auth follows the exact
// same T-1104 bearer-token convention every other `remote`/`apply` command
// uses (see remoteclient.go's package doc comment) — `--token`/
// `VNPROX_TOKEN`, never a PVE username/password.
//
// The `"events"` topic additionally requires the connecting identity's
// token to carry the `automation` capability (docs/api.md's WebSocket
// section: a subscribe naming "events" from a connection that lacks it is
// silently dropped — an ack-less, fail-closed protocol with no rejection
// signal on the wire). Since a bearer token's granted scopes land verbatim
// in `GET /auth/me`'s `caps` response (internal/auth/middleware.go's
// authenticateBearer: `caps := map[string]Capabilities{"":
// CapabilitiesFromScopes(scopes)}`), this command preflights that route
// before ever dialing the socket, so a token missing the scope gets a
// clear, immediate, non-zero-exit error instead of a connection that opens
// successfully and then silently receives nothing forever.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"
	"nhooyr.io/websocket"

	"github.com/bgovanlu/vnprox/internal/config"
)

// watchCapabilities mirrors the subset of internal/auth.Capabilities'
// documented `GET /auth/me` JSON shape this command needs (docs/api.md's
// Auth section) — just the one flag that gates the "events" topic, not the
// whole capability vocabulary, so this file has no dependency on
// internal/auth itself (matching remote.go/speccmd.go's existing convention
// of re-declaring narrow wire DTOs rather than importing daemon-side
// packages into the CLI binary).
type watchCapabilities struct {
	Automation bool `json:"automation"`
}

// watchMeResponse mirrors GET /auth/me's `{user, caps: {<node>:
// Capabilities}}` shape, narrowed to the one field this command reads. A
// bearer token's capabilities are cluster-wide (docs/api.md's Tokens
// section: "a single cluster-wide entry, no per-node granularity"), always
// keyed "" — see internal/auth/middleware.go's authenticateBearer.
type watchMeResponse struct {
	Caps map[string]watchCapabilities `json:"caps"`
}

// watchEventKindsHint lists the event names the "events" topic is
// documented to carry today (docs/architecture.md §13.3 / docs/api.md's
// WebSocket section: "changeset.status, drift.changed, findings.changed,
// audit.appended"), for --help text only. --kind does not validate against
// this list (see parseKindFilter) — the frozen envelope is additive-only,
// so a future event name must work as a filter value the moment a server
// starts sending it, without requiring a new vnproxctl release first.
const watchEventKindsHint = "changeset.status, drift.changed, findings.changed, audit.appended"

func runWatch(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl watch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rf := addRemoteFlags(fs)
	kindFlag := fs.String("kind", "", "comma-separated event names to show (known values today: "+watchEventKindsHint+"); default: all")
	maxEvents := fs.Int("max-events", 0, "exit after this many rendered events (0 = run until interrupted)")
	noColor := fs.Bool("no-color", false, "disable ANSI colors even when stdout is a terminal")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	jsonOut, code, ok := parseOutputFlagOrUsage(rf, "vnproxctl watch", stderr)
	if !ok {
		return code
	}
	kinds := parseKindFilter(*kindFlag)

	client, code := buildRemoteClient(rf, "vnproxctl watch", stderr)
	if client == nil {
		return code
	}

	preflightCtx, preflightCancel := context.WithTimeout(context.Background(), *rf.timeout)
	var me watchMeResponse
	status, apiErr, err := client.doJSON(preflightCtx, "GET", "/auth/me", nil, &me)
	preflightCancel()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl watch: %v\n", err)
		return exitForErr(err)
	}
	if apiErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl watch: %s: %s\n", apiErr.Code, apiErr.Message)
		return exitForAPIError(status)
	}
	if !me.Caps[""].Automation {
		_, _ = fmt.Fprintln(stderr, "vnproxctl watch: this token lacks the \"automation\" scope the \"events\" topic requires "+
			"(docs/api.md's Tokens & Webhooks section) — mint a token with automation in its --scopes and pass it via --token/"+remoteTokenEnvVar)
		return ExitAuth
	}

	wsURL, err := resolveWatchWSURL(rf)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl watch: %v\n", err)
		return ExitUsage
	}

	httpClient := &http.Client{
		Timeout:   *rf.timeout,
		Transport: client.http.Transport,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	tty := isTerminalWriter(stdout) && !*noColor
	renderer := newEventRenderer(stdout, tty, jsonOut)

	return watchLoop(ctx, wsURL, client.token, httpClient, renderer, kinds, *maxEvents, stderr)
}

// fdWriter is satisfied by *os.File; used to detect a real terminal without
// widening this command's stdout parameter beyond io.Writer (main.go passes
// os.Stdout in production; tests pass a *bytes.Buffer, which does not
// satisfy this interface and therefore always takes the non-TTY path —
// exactly the deterministic behavior a test needs without a real pty).
type fdWriter interface {
	Fd() uintptr
}

// isTerminalWriter reports whether w is connected to a terminal.
func isTerminalWriter(w io.Writer) bool {
	f, ok := w.(fdWriter)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// resolveWatchWSURL derives the /api/ws URL from the same --url/--config
// precedence buildRemoteClient uses for the /api/v1 base — /api/ws is a
// top-level route, not under /api/v1 (docs/api.md's WebSocket section), so
// this cannot simply reuse remoteClient.baseURL.
func resolveWatchWSURL(rf *remoteFlags) (string, error) {
	if raw := *rf.url; raw != "" {
		u, err := url.Parse(raw)
		if err != nil {
			return "", fmt.Errorf("invalid --url %q: %w", raw, err)
		}
		switch u.Scheme {
		case "https", "wss", "":
			u.Scheme = "wss"
		case "http", "ws":
			u.Scheme = "ws"
		default:
			return "", fmt.Errorf("invalid --url %q: unsupported scheme %q", raw, u.Scheme)
		}
		u.Path = "/api/ws"
		u.RawQuery = ""
		u.Fragment = ""
		return u.String(), nil
	}

	cfg, err := config.Load(*rf.configPath, discardLogger())
	if err != nil {
		return "", fmt.Errorf("loading %s: %w", *rf.configPath, err)
	}
	host, port, err := net.SplitHostPort(cfg.Server.Listen)
	if err != nil {
		return "", fmt.Errorf("parsing server.listen %q: %w", cfg.Server.Listen, err)
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return fmt.Sprintf("wss://%s/api/ws", net.JoinHostPort(host, port)), nil
}

// parseKindFilter splits --kind's comma-separated value into a set; an
// empty flag means "no filter" (nil, distinguished from an empty-but-set
// map so kindAllowed's nil check is unambiguous).
func parseKindFilter(raw string) map[string]bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	out := map[string]bool{}
	for _, k := range strings.Split(raw, ",") {
		k = strings.TrimSpace(k)
		if k != "" {
			out[k] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func kindAllowed(kinds map[string]bool, event string) bool {
	if kinds == nil {
		return true
	}
	return kinds[event]
}

// subscribeMessage is the one documented client->server WS message
// (docs/api.md's WebSocket section): `{"subscribe": [...]}`.
var subscribeEventsMessage = []byte(`{"subscribe":["events"]}`)

// watchLoop owns the dial/subscribe/read/reconnect cycle. It returns
// ExitSuccess when ctx is cancelled (Ctrl-C/SIGTERM — a clean, expected
// stop) or when maxEvents is reached, and only ever returns non-zero for a
// problem the auth preflight in runWatch could not have already caught
// (there is none today, but the seam is kept so a future non-recoverable
// class has somewhere to report through instead of retrying forever).
func watchLoop(ctx context.Context, wsURL, token string, httpClient *http.Client, r *eventRenderer, kinds map[string]bool, maxEvents int, stderr io.Writer) int {
	attempt := 0
	everConnected := false
	rendered := 0
	// gapSince is non-zero for as long as the stream is down (dial failed
	// or an established connection dropped); it is what lets the eventual
	// "reconnected" line report how long the gap actually lasted, rather
	// than just announcing that one occurred — the acceptance bar CLAUDE.md
	// and this card's own text set ("the gap made visible") means the
	// operator can tell whether they missed ten seconds of events or ten
	// minutes.
	var gapSince time.Time

	for {
		if ctx.Err() != nil {
			r.stopped()
			return ExitSuccess
		}

		dialCtx, dialCancel := context.WithTimeout(ctx, 15*time.Second)
		conn, _, dialErr := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{
			HTTPClient: httpClient,
			HTTPHeader: http.Header{"Authorization": {"Bearer " + token}},
		})
		dialCancel()

		if dialErr != nil {
			if ctx.Err() != nil {
				r.stopped()
				return ExitSuccess
			}
			if gapSince.IsZero() {
				gapSince = time.Now()
			}
			attempt++
			r.reconnecting(attempt, dialErr)
			wait := watchBackoff(attempt)
			select {
			case <-ctx.Done():
				r.stopped()
				return ExitSuccess
			case <-time.After(wait):
			}
			continue
		}

		if everConnected {
			gap := time.Duration(0)
			if !gapSince.IsZero() {
				gap = time.Since(gapSince)
			}
			r.reconnected(attempt, gap)
		} else {
			r.connected()
			everConnected = true
		}
		attempt = 0
		// gapSince is intentionally left as-is here rather than reset to
		// zero: every path that starts a new gap (dial failure, subscribe
		// failure, read failure below) unconditionally overwrites it with
		// time.Now() before it is next read, so there is no observable
		// state left over from the gap that just ended.

		subCtx, subCancel := context.WithTimeout(ctx, 10*time.Second)
		writeErr := conn.Write(subCtx, websocket.MessageText, subscribeEventsMessage)
		subCancel()
		if writeErr != nil {
			_ = conn.CloseNow()
			if ctx.Err() != nil {
				r.stopped()
				return ExitSuccess
			}
			gapSince = time.Now()
			attempt++
			r.reconnecting(attempt, writeErr)
			continue
		}

		for {
			_, data, readErr := conn.Read(ctx)
			if readErr != nil {
				_ = conn.CloseNow()
				if ctx.Err() != nil {
					r.stopped()
					return ExitSuccess
				}
				gapSince = time.Now()
				r.disconnected(readErr)
				break
			}

			var evt map[string]any
			if err := json.Unmarshal(data, &evt); err != nil {
				_, _ = fmt.Fprintf(stderr, "vnproxctl watch: ignoring malformed event: %v\n", err)
				continue
			}
			name, _ := evt["event"].(string)
			if !kindAllowed(kinds, name) {
				continue
			}
			r.event(evt)
			rendered++
			if maxEvents > 0 && rendered >= maxEvents {
				_ = conn.Close(websocket.StatusNormalClosure, "vnproxctl watch: --max-events reached")
				return ExitSuccess
			}
		}
	}
}

// watchBackoff mirrors web/src/api/ws.ts's reconnect backoff (500ms floor,
// 15s ceiling, doubling per attempt, ±50% jitter) — the CLI reconnects on
// the same schedule the web UI's own WS client already uses, per the task
// card's "matching the web client's own reconnect behavior" instruction.
const (
	watchMinBackoff = 500 * time.Millisecond
	watchMaxBackoff = 15 * time.Second
)

func watchBackoff(attempt int) time.Duration {
	d := watchMinBackoff
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= watchMaxBackoff {
			d = watchMaxBackoff
			break
		}
	}
	jittered := time.Duration(float64(d) * (0.5 + rand.Float64()*0.5))
	if jittered <= 0 {
		return watchMinBackoff
	}
	return jittered
}

// sortedKeys is a small shared helper for the renderer's deterministic
// key=value field ordering (see watchevents.go).
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
