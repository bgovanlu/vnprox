// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/apidoc"
)

// updateOpenAPI regenerates docs/openapi.json from the running daemon.
//
//	go test ./cmd/vnproxd/... -run TestOpenAPI -update
//
// which is what `make openapi` runs. Mirrors internal/apicontract's own
// -update convention.
var updateOpenAPI = flag.Bool("update", false, "rewrite docs/openapi.json from the running daemon's document")

// openAPIVersionPlaceholder replaces the build-stamped version in the
// committed document. Without it every `git describe` bump would rewrite the
// file, and a contract document that changes on every commit is one nobody
// reviews the diff of — which is precisely the property this test exists to
// provide.
const openAPIVersionPlaceholder = "unversioned"

// startDevDaemon brings up the real daemon against the checked-in dev config
// on an ephemeral port and returns its base URL and a client that trusts the
// throwaway dev certificate.
//
// It is deliberately the *production* runDaemon path rather than a router
// assembled for the test: every mount function in internal/api returns early
// when its service is nil, so a hand-assembled router silently omits routes —
// and a route-completeness gate that walks a router missing routes passes by
// not looking.
func startDevDaemon(t *testing.T) (string, *http.Client) {
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

	cfgPath := rewriteDevConfig(t, repoRoot, t.TempDir(), port)

	ctx, cancel := context.WithCancel(context.Background())
	daemonDone := make(chan error, 1)
	go func() { daemonDone <- runDaemon(ctx, daemonOptions{ConfigPath: cfgPath}, testLogger()) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-daemonDone:
		case <-time.After(10 * time.Second):
			t.Errorf("daemon did not shut down within 10s")
		}
	})

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test-only client for the throwaway dev cert
		},
	}
	base := fmt.Sprintf("https://127.0.0.1:%d", port)

	deadline := time.Now().Add(20 * time.Second)
	for {
		select {
		case err := <-daemonDone:
			t.Fatalf("daemon exited before serving: %v", err)
		default:
		}
		resp, err := client.Get(base + "/api/v1/health")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return base, client
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon did not serve /api/v1/health within 20s (last error: %v)", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// fetchOpenAPI reads the document the live daemon generated from its own
// router, with the build-stamped version normalised.
func fetchOpenAPI(t *testing.T, base string, client *http.Client) (*apidoc.Document, []byte) {
	t.Helper()

	// No cookie jar, no Authorization header, no CSRF token: AC4's "reachable
	// without a session" is asserted by the absence, not by a comment.
	resp, err := client.Get(base + "/api/v1/openapi.json")
	if err != nil {
		t.Fatalf("GET /api/v1/openapi.json: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/openapi.json without a session: status %d, want 200 — the contract must be readable without credentials", resp.StatusCode)
	}

	var doc apidoc.Document
	if decodeErr := json.NewDecoder(resp.Body).Decode(&doc); decodeErr != nil {
		t.Fatalf("decoding the served document: %v", decodeErr)
	}
	doc.Info.Version = openAPIVersionPlaceholder

	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("re-encoding the served document: %v", err)
	}
	return &doc, append(body, '\n')
}

// docRoutes flattens a document back into METHOD+path keys.
func docRoutes(doc *apidoc.Document) map[string]bool {
	out := map[string]bool{}
	for path, item := range doc.Paths {
		for method, op := range map[string]*apidoc.Op{
			http.MethodGet:    item.Get,
			http.MethodPut:    item.Put,
			http.MethodPost:   item.Post,
			http.MethodDelete: item.Delete,
			http.MethodPatch:  item.Patch,
			http.MethodHead:   item.Head,
		} {
			if op != nil {
				out[apidoc.Key(method, path)] = true
			}
		}
	}
	return out
}

// TestOpenAPI_MatchesTheCommittedDocument is the frozen-contract half of
// T-2405: docs/openapi.json must equal what the production daemon serves.
//
// It compares the whole document byte-for-byte, not just the path set, so
// that a *field* removed from an operation — a security requirement dropped,
// a response code that stopped being possible — appears as a reviewable diff.
// That is the general form of T-2002-bug-01, where a field disappeared from a
// response and nothing anywhere noticed.
func TestOpenAPI_MatchesTheCommittedDocument(t *testing.T) {
	if testing.Short() {
		t.Skip("brings up the full daemon")
	}
	base, client := startDevDaemon(t)
	_, served := fetchOpenAPI(t, base, client)

	path := filepath.Join("..", "..", "docs", "openapi.json")
	if *updateOpenAPI {
		if err := os.WriteFile(path, served, 0o600); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
		t.Logf("wrote %s (%d bytes)", path, len(served))
		return
	}

	committed, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s (run `make openapi` to create it): %v", path, err)
	}
	if string(committed) == string(served) {
		return
	}

	// A whole-document diff is unreadable at this size, so lead with the
	// route-set difference, which is what has almost always changed.
	servedDoc, _ := fetchOpenAPI(t, base, client)
	var committedDoc apidoc.Document
	if err := json.Unmarshal(committed, &committedDoc); err != nil {
		t.Fatalf("parsing the committed %s: %v", path, err)
	}
	added, removed := routeSetDiff(docRoutes(&committedDoc), docRoutes(servedDoc))
	switch {
	case len(added) > 0 || len(removed) > 0:
		t.Errorf("the router's routes and docs/openapi.json disagree.\n"+
			"served but not documented (%d): %s\n"+
			"documented but not served (%d): %s\n"+
			"Run `make openapi` after confirming the change is intended.",
			len(added), strings.Join(added, ", "), len(removed), strings.Join(removed, ", "))
	default:
		t.Errorf("docs/openapi.json differs from the served document, though the route set is identical — "+
			"a description, security requirement or response set changed. "+
			"Run `make openapi` after confirming the change is intended. (%d bytes committed, %d served)",
			len(committed), len(served))
	}
}

func routeSetDiff(committed, served map[string]bool) (added, removed []string) {
	for key := range served {
		if !committed[key] {
			added = append(added, key)
		}
	}
	for key := range committed {
		if !served[key] {
			removed = append(removed, key)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

// TestOpenAPI_EveryRouteIsDescribed is the gate, run against the production
// router rather than a test one.
//
// Both directions, because they fail differently: an undescribed route ships
// an endpoint no integrator can discover, while a described-but-unserved
// route promises an endpoint that 404s.
func TestOpenAPI_EveryRouteIsDescribed(t *testing.T) {
	if testing.Short() {
		t.Skip("brings up the full daemon")
	}
	base, client := startDevDaemon(t)
	doc, _ := fetchOpenAPI(t, base, client)

	var undescribed []string
	for path, item := range doc.Paths {
		for method, op := range map[string]*apidoc.Op{
			http.MethodGet:    item.Get,
			http.MethodPut:    item.Put,
			http.MethodPost:   item.Post,
			http.MethodDelete: item.Delete,
			http.MethodPatch:  item.Patch,
			http.MethodHead:   item.Head,
		} {
			if op == nil {
				continue
			}
			if strings.HasPrefix(op.Summary, "Undescribed route") {
				undescribed = append(undescribed, apidoc.Key(method, path))
			}
		}
	}
	sort.Strings(undescribed)
	if len(undescribed) > 0 {
		t.Errorf("%d route(s) the daemon serves have no entry in internal/apidoc's Operations table:\n  %s\n"+
			"Add one line each. An endpoint that ships without a description is one no integrator can find.",
			len(undescribed), strings.Join(undescribed, "\n  "))
	}

	// The other direction. Operations describes routes across every
	// configuration, so an entry is only "unserved" if this configuration
	// mounts nothing at that path AND the document does not list it either.
	routes := docRoutes(doc)
	var unserved []string
	for key := range apidoc.Operations {
		if !routes[key] {
			unserved = append(unserved, key)
		}
	}
	sort.Strings(unserved)
	if len(unserved) > 0 {
		t.Errorf("%d entr(ies) in Operations describe routes the daemon does not serve:\n  %s\n"+
			"A documented route that 404s is worse than an undocumented one.",
			len(unserved), strings.Join(unserved, "\n  "))
	}
}

// TestOpenAPI_DescribedRoutesRequireCredentials is AC4's second half: the
// document is readable without a session, and the routes it describes are
// not.
//
// It drives every documented GET that has no path parameters and claims the
// session scheme, with no cookie at all, and requires a refusal. A route that
// answered 200 here would be an unauthenticated read of live network state.
func TestOpenAPI_DescribedRoutesRequireCredentials(t *testing.T) {
	if testing.Short() {
		t.Skip("brings up the full daemon")
	}
	base, client := startDevDaemon(t)
	doc, _ := fetchOpenAPI(t, base, client)

	checked := 0
	for path, item := range doc.Paths {
		op := item.Get
		if op == nil || len(op.Parameters) > 0 {
			continue
		}
		if !requiresScheme(op, "sessionCookie") {
			continue
		}
		resp, err := client.Get(base + path)
		if err != nil {
			t.Errorf("GET %s: %v", path, err)
			continue
		}
		_ = resp.Body.Close()
		checked++
		if resp.StatusCode == http.StatusOK {
			t.Errorf("GET %s answered 200 with no session cookie; the document says it requires one", path)
		}
	}
	if checked < 20 {
		t.Fatalf("only %d session-guarded parameterless GETs were driven; the document is not describing the real router", checked)
	}
}

func requiresScheme(op *apidoc.Op, scheme string) bool {
	for _, req := range op.Security {
		if _, ok := req[scheme]; ok {
			return true
		}
	}
	return false
}
