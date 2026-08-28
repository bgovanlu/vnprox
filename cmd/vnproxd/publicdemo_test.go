// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/publicdemo"
)

// T-2802 AC1, driven against the SHIPPED daemon.
//
// "Every mutating route returns 403 at the edge, asserted by driving the API
// directly rather than the UI, across the full route list from
// docs/openapi.json." Every clause of that is load-bearing:
//
//   - the full route list, READ from the committed document rather than
//     written out here, so a route added next month is covered by whoever
//     adds it and not by whoever remembers this file;
//   - driving the API directly, over TLS, against a daemon started the way
//     the unit file starts it, because the UI's refusal to offer a button is
//     not the property being asserted;
//   - at the edge, which is a stronger claim than "403", and is asserted by
//     internal/publicdemo's own X-Vnprox-Public-Demo-Refused marker: a
//     daemon that answered 403 for its own reasons would satisfy a naive
//     status check while proving nothing about the edge.
//
// internal/publicdemo/edge_test.go runs the same enumeration against the
// edge alone, where the control leg (the identical list with no edge) is
// free. Here the control legs are (a) the safe methods, which must be
// forwarded and answered, and (b) a second daemon started WITHOUT
// --public-demo, on which none of these routes is refused at the edge.

// --public-demo alone does not start a daemon.
//
// This is the guard that stops a read-only façade being put in front of a
// daemon holding real PVE credentials: the edge refuses writes, but the
// daemon behind it can still reach a real cluster, and "nothing real is
// behind this" is what --demo is for. Asserted at the flag layer, before any
// config is read or any port is bound.
func TestPublicDemo_RequiresDemoMode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := mainRun([]string{"--public-demo", "--config", filepath.Join(t.TempDir(), "nonexistent.toml")}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("--public-demo without --demo started a daemon")
	}
	if !strings.Contains(stderr.String(), "requires --demo") {
		t.Errorf("stderr = %q, want it to name the reason", stderr.String())
	}

	// The control leg: the same flag WITH --demo gets past this check. It
	// then fails on the missing config, which is a different failure and a
	// different message — without this, "exits non-zero" would be satisfied
	// by a binary that refuses everything.
	stdout.Reset()
	stderr.Reset()
	code = mainRun([]string{"--public-demo", "--demo", "--config", filepath.Join(t.TempDir(), "nonexistent.toml")}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("a demo daemon started against a config that does not exist")
	}
	if strings.Contains(stderr.String(), "requires --demo") {
		t.Errorf("--public-demo --demo was refused by the flag-pairing check: %q", stderr.String())
	}
}

// rewriteDemoConfig is rewriteDevConfig's sibling for testdata/demo.toml:
// same job (ephemeral port, temp paths, absolute cert paths), different
// file. Separate rather than parameterised because the two configs have
// different key sets — demo.toml has a [capture] root and no [pve] section
// at all — and a helper that silently skipped a missing key would be a
// helper that silently stopped rewriting one.
func rewriteDemoConfig(t testing.TB, repoRoot, dir string, port int) string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(repoRoot, "testdata", "demo.toml"))
	if err != nil {
		t.Fatalf("reading testdata/demo.toml: %v", err)
	}

	replacements := map[string]string{
		"listen":              fmt.Sprintf("127.0.0.1:%d", port),
		"tls_cert":            filepath.Join(repoRoot, "testdata", "certs", "dev-cert.pem"),
		"tls_key":             filepath.Join(repoRoot, "testdata", "certs", "dev-key.pem"),
		"db_path":             filepath.Join(dir, "vnprox.db"),
		"session_key_file":    filepath.Join(dir, "session.key"),
		"protected_path":      filepath.Join(dir, "protected.json"),
		"dev_interfaces_dir":  filepath.Join(dir, "dev-host"),
		"secret_path":         filepath.Join(dir, "cluster.secret"),
		"key_file":            filepath.Join(dir, "metrics.key"),
		"signing_key_file":    filepath.Join(dir, "blueprint-signing.key"),
		"trusted_signers_dir": filepath.Join(dir, "trusted-signers"),
		"root":                filepath.Join(dir, "captures"),
	}
	replaced := make(map[string]bool, len(replacements))

	lines := strings.Split(string(raw), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		for key, value := range replacements {
			if strings.HasPrefix(trimmed, key+" ") || strings.HasPrefix(trimmed, key+"=") {
				lines[i] = fmt.Sprintf("%s = %q", key, value)
				replaced[key] = true
			}
		}
	}
	for key := range replacements {
		if !replaced[key] {
			t.Fatalf("testdata/demo.toml has no %q key to rewrite; update this test to match its current shape", key)
		}
	}

	cfgPath := filepath.Join(dir, "demo.toml")
	if err := os.WriteFile(cfgPath, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatalf("writing the rewritten demo config: %v", err)
	}
	return cfgPath
}

// startDemoDaemon brings up `vnproxd --demo [--public-demo]` on an
// ephemeral port, through the production runDaemon path.
func startDemoDaemon(t *testing.T, public bool) string {
	t.Helper()

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving ephemeral port: %v", err)
	}
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("ephemeral listener is not TCP: %T", ln.Addr())
	}
	port := addr.Port
	_ = ln.Close()

	cfgPath := rewriteDemoConfig(t, repoRoot, t.TempDir(), port)

	ctx, cancel := context.WithCancel(context.Background())
	daemonDone := make(chan error, 1)
	go func() {
		daemonDone <- runDaemon(ctx, daemonOptions{ConfigPath: cfgPath, Demo: true, PublicDemo: public}, testLogger())
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-daemonDone:
		case <-time.After(10 * time.Second):
			t.Errorf("daemon did not shut down within 10s")
		}
	})

	base := fmt.Sprintf("https://127.0.0.1:%d", port)
	client := demoHTTPClient(t)
	deadline := time.Now().Add(30 * time.Second)
	for {
		select {
		case err := <-daemonDone:
			t.Fatalf("daemon exited before serving: %v", err)
		default:
		}
		resp, getErr := client.Get(base + "/api/v1/health")
		if getErr == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return base
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon did not serve /api/v1/health within 30s (last error: %v)", getErr)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// demoHTTPClient is one visitor's browser: its own cookie jar, so "another
// visitor" in these tests means another jar rather than another header.
func demoHTTPClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("building a cookie jar: %v", err)
	}
	return &http.Client{
		Timeout: 20 * time.Second,
		Jar:     jar,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test-only client for the throwaway dev cert
		},
		// A refused request is the subject; following a redirect to
		// somewhere that answers differently would hide it.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

type documentedRoute struct {
	method string
	path   string
}

// committedRoutes reads docs/openapi.json — the committed contract, the
// thing AC1 names — and flattens it. An entry it cannot classify fails the
// test rather than being skipped.
func committedRoutes(t *testing.T) []documentedRoute {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "openapi.json"))
	if err != nil {
		t.Fatalf("reading docs/openapi.json: %v", err)
	}
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if unmarshalErr := json.Unmarshal(raw, &doc); unmarshalErr != nil {
		t.Fatalf("parsing docs/openapi.json: %v", unmarshalErr)
	}

	var out []documentedRoute
	for path, item := range doc.Paths {
		for method := range item {
			switch method {
			case "get", "put", "post", "delete", "patch", "head", "options":
				out = append(out, documentedRoute{method: strings.ToUpper(method), path: path})
			case "parameters", "summary", "description":
			default:
				t.Errorf("docs/openapi.json path %q carries key %q, which this test cannot classify as an operation or not — "+
					"classify it; an unclassified route is how a mutating route ships unrefused", path, method)
			}
		}
	}
	if len(out) < 200 {
		t.Fatalf("only %d routes read out of docs/openapi.json; the document is not being enumerated", len(out))
	}
	return out
}

func concreteRoutePath(path string) string {
	out := path
	for strings.Contains(out, "{") {
		open := strings.Index(out, "{")
		closed := strings.Index(out[open:], "}")
		if closed < 0 {
			break
		}
		out = out[:open] + "demo-e2e" + out[open+closed+1:]
	}
	return out
}

func TestPublicDemo_EveryMutatingRouteIsRefusedAtTheEdge(t *testing.T) {
	if testing.Short() {
		t.Skip("brings up the full daemon")
	}
	routes := committedRoutes(t)
	base := startDemoDaemon(t, true)

	// One visitor cannot drive 400-odd routes: the per-visitor request cap
	// is the point of AC4 and applies here too. Rotating a jar every 80
	// requests keeps every request inside a real visitor's budget while
	// staying far below the visitor cap — and it is itself a small proof
	// that the caps are live on the shipped binary.
	client := demoHTTPClient(t)
	sent := 0
	rotate := func() {
		if sent%80 == 0 {
			client = demoHTTPClient(t)
		}
		sent++
	}

	mutating, safe, answered := 0, 0, 0
	for _, route := range routes {
		class, known := publicdemo.Classify(route.method)
		if !known {
			t.Errorf("%s %s: docs/openapi.json documents a method the edge does not classify; that must fail, not skip",
				route.method, route.path)
			continue
		}

		rotate()
		req, err := http.NewRequestWithContext(context.Background(), route.method,
			base+concreteRoutePath(route.path), strings.NewReader("{}"))
		if err != nil {
			t.Fatalf("%s %s: building the request: %v", route.method, route.path, err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Errorf("%s %s: %v", route.method, route.path, err)
			continue
		}
		_ = resp.Body.Close()

		if resp.Header.Get(publicdemo.HeaderPublicDemo) != "1" {
			t.Errorf("%s %s: no %s header; the edge is not in front of this route",
				route.method, route.path, publicdemo.HeaderPublicDemo)
		}

		if class == publicdemo.ClassMutating {
			mutating++
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("%s %s: status %d, want 403 — a mutating route was not refused at the edge",
					route.method, route.path, resp.StatusCode)
			}
			if got := resp.Header.Get(publicdemo.HeaderRefused); got != "public_demo_read_only" {
				t.Errorf("%s %s: %s = %q, want public_demo_read_only — a 403 that is not the edge's is not what AC1 asks for",
					route.method, route.path, publicdemo.HeaderRefused, got)
			}
			continue
		}

		safe++
		if got := resp.Header.Get(publicdemo.HeaderRefused); got != "" {
			t.Errorf("%s %s: the edge refused a safe method (%s)", route.method, route.path, got)
		}
		if resp.StatusCode == http.StatusOK {
			answered++
		}
	}

	if mutating < 50 {
		t.Fatalf("only %d mutating routes were driven; the enumeration is not doing its job", mutating)
	}
	// The control leg. Without it, "every mutating route is 403" is
	// satisfied by a daemon that refuses everything.
	if answered < 40 {
		t.Errorf("only %d of %d safe routes answered 200; a public demo that refuses reads too is not a demo, "+
			"and this assertion is what stops the 403s above from being vacuous", answered, safe)
	}
}

// The other control leg: the same routes, the same binary, the same config,
// WITHOUT --public-demo. Nothing here is refused at the edge, because there
// is no edge — which is what makes the test above an assertion about the
// edge rather than about demo mode.
func TestPublicDemo_WithoutTheFlagNothingIsRefusedAtTheEdge(t *testing.T) {
	if testing.Short() {
		t.Skip("brings up the full daemon")
	}
	routes := committedRoutes(t)
	base := startDemoDaemon(t, false)
	client := demoHTTPClient(t)

	checked := 0
	for _, route := range routes {
		if class, _ := publicdemo.Classify(route.method); class != publicdemo.ClassMutating {
			continue
		}
		req, err := http.NewRequestWithContext(context.Background(), route.method,
			base+concreteRoutePath(route.path), strings.NewReader("{}"))
		if err != nil {
			t.Fatalf("%s %s: %v", route.method, route.path, err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Errorf("%s %s: %v", route.method, route.path, err)
			continue
		}
		_ = resp.Body.Close()
		checked++
		if got := resp.Header.Get(publicdemo.HeaderRefused); got != "" {
			t.Errorf("%s %s was refused at the edge on a daemon started without --public-demo (%s)", route.method, route.path, got)
		}
		if resp.Header.Get(publicdemo.HeaderPublicDemo) != "" {
			t.Errorf("%s %s carried %s without --public-demo", route.method, route.path, publicdemo.HeaderPublicDemo)
		}
	}
	if checked < 50 {
		t.Fatalf("only %d mutating routes were driven on the control daemon", checked)
	}
}

// AC3 against the shipped daemon: two visitors, two real sessions.
//
// "Two real sessions" is proven by the daemon's own audit log — two
// successful logins it recorded itself — not by two cookie values this test
// made up. And their scratch state is mutually invisible, with the control
// leg (each visitor reading back their own) included, because "B cannot see
// A's layout" is trivially true of a surface that stores nothing.
func TestPublicDemo_SessionStateIsPerVisitor(t *testing.T) {
	if testing.Short() {
		t.Skip("brings up the full daemon")
	}
	base := startDemoDaemon(t, true)

	a, b := demoHTTPClient(t), demoHTTPClient(t)
	getJSON := func(client *http.Client, path string, into any) int {
		t.Helper()
		resp, err := client.Get(base + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		if into != nil && resp.StatusCode == http.StatusOK {
			if decodeErr := json.NewDecoder(resp.Body).Decode(into); decodeErr != nil {
				t.Fatalf("decoding GET %s: %v", path, decodeErr)
			}
		}
		return resp.StatusCode
	}

	var sessionA, sessionB publicdemo.VisitorSession
	if code := getJSON(a, publicdemo.VisitorSessionPath, &sessionA); code != http.StatusOK {
		t.Fatalf("visitor A session: status %d", code)
	}
	if code := getJSON(b, publicdemo.VisitorSessionPath, &sessionB); code != http.StatusOK {
		t.Fatalf("visitor B session: status %d", code)
	}
	if sessionA.Visitor == "" || sessionA.Visitor == sessionB.Visitor {
		t.Fatalf("visitor ids A=%q B=%q; each visitor must get their own", sessionA.Visitor, sessionB.Visitor)
	}

	// Each visitor reads something, which is what mints their daemon
	// session (the edge mints lazily, on first forwarded read).
	if code := getJSON(a, "/api/v1/topology", nil); code != http.StatusOK {
		t.Fatalf("visitor A reading the topology: status %d", code)
	}
	if code := getJSON(b, "/api/v1/topology", nil); code != http.StatusOK {
		t.Fatalf("visitor B reading the topology: status %d", code)
	}

	var audit struct {
		Items []struct {
			Username string `json:"username"`
			Action   string `json:"action"`
			Result   string `json:"result"`
		} `json:"items"`
	}
	if code := getJSON(a, "/api/v1/audit?limit=200", &audit); code != http.StatusOK {
		t.Fatalf("reading the audit log: status %d", code)
	}
	logins := 0
	for _, item := range audit.Items {
		if item.Action == "login" && item.Result == "success" {
			logins++
		}
	}
	if logins < 2 {
		t.Errorf("the daemon recorded %d successful logins for 2 visitors; the sessions are not per-visitor", logins)
	}

	// Layout state, the thing AC3 names.
	const layoutKey = publicdemo.VisitorStatePrefix + "topology"
	put := func(client *http.Client, path, body string) (int, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, base+path, strings.NewReader(body))
		if err != nil {
			t.Fatalf("building PUT %s: %v", path, err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("PUT %s: %v", path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		buf := make([]byte, 512)
		n, _ := resp.Body.Read(buf)
		return resp.StatusCode, string(buf[:n])
	}

	if code, body := put(a, layoutKey, `{"state":{"nodes":{"pve1":{"x":11,"y":22}}}}`); code != http.StatusOK {
		t.Fatalf("visitor A saving a layout: status %d, body %s", code, body)
	}
	if code := getJSON(b, layoutKey, nil); code != http.StatusNotFound {
		t.Errorf("visitor B could see visitor A's layout: status %d, want 404", code)
	}
	var readback publicdemo.VisitorState
	if code := getJSON(a, layoutKey, &readback); code != http.StatusOK {
		t.Fatalf("visitor A reading back their own layout: status %d", code)
	}
	if !strings.Contains(string(readback.State), `"x":11`) {
		t.Errorf("visitor A's layout came back as %s", readback.State)
	}

	// And the daemon's own /layouts route — the one a normal instance would
	// have used — is refused, which is why the surface above exists at all.
	code, _ := put(a, "/api/v1/layouts/topology", `{"layout":{}}`)
	if code != http.StatusForbidden {
		t.Errorf("PUT /api/v1/layouts/topology: status %d, want 403", code)
	}
}
