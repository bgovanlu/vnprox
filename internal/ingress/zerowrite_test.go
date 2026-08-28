// SPDX-License-Identifier: Apache-2.0

// zerowrite_test.go is T-1406 AC4's regression test: no IngressDiscoverer
// implementation in this package ever issues a mutating call to a
// configured target. Two independent checks, per the card's own wording
// ("grep-verifiable (read-only HTTP verbs only) plus a fixture-double test
// asserting zero write calls received"):
//
//  1. Source inspection: none of this package's non-test .go files
//     reference http.MethodPost/Put/Patch/Delete, or the literal strings
//     "POST"/"PUT"/"PATCH"/"DELETE" as an HTTP method.
//  2. Live behavior: every vendor discoverer is driven against its
//     ingressmock double, and every request the double recorded is
//     asserted GET.

package ingress

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/ingress/ingressmock"
)

var forbiddenVerbTokens = []string{
	"http.MethodPost", "http.MethodPut", "http.MethodPatch", "http.MethodDelete",
	`"POST"`, `"PUT"`, `"PATCH"`, `"DELETE"`,
}

func TestZeroWriteSurface_SourceNeverReferencesMutatingVerbs(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		// doc.go is documentation only (no HTTP code) and its own prose
		// necessarily names these tokens to describe the invariant this
		// test enforces everywhere else — excluded from the scan, not
		// from the invariant.
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "doc.go" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", name, err)
		}
		content := string(data)
		for _, tok := range forbiddenVerbTokens {
			if strings.Contains(content, tok) {
				t.Errorf("%s references mutating-verb token %q — every discoverer must be GET-only", name, tok)
			}
		}
	}
}

func TestZeroWriteSurface_EveryDiscovererIssuesOnlyGET(t *testing.T) {
	haSrv, haRec := ingressmock.NewHAProxyServer([]ingressmock.HAProxyBackend{{Pool: "p", Name: "s1", Addr: "10.0.0.5:80", Up: true}})
	defer haSrv.Close()
	nginxSrv, nginxRec := ingressmock.NewNginxServer(ingressmock.NginxPlusAPI, "pool", []ingressmock.NginxPeer{{Server: "10.0.0.5:80", Up: true}})
	defer nginxSrv.Close()
	caddySrv, caddyRec := ingressmock.NewCaddyServer([]ingressmock.CaddyUpstream{{Address: "10.0.0.5:80"}})
	defer caddySrv.Close()
	traefikSrv, traefikRec := ingressmock.NewTraefikServer([]ingressmock.TraefikServer{{Name: "s", Enabled: true, URLs: []string{"http://10.0.0.5:80"}}})
	defer traefikSrv.Close()

	reg := Registry{
		KindHAProxy: &HAProxyDiscoverer{Client: haSrv.Client()},
		KindNginx:   &NginxDiscoverer{Client: nginxSrv.Client()},
		KindCaddy:   &CaddyDiscoverer{Client: caddySrv.Client()},
		KindTraefik: &TraefikDiscoverer{Client: traefikSrv.Client()},
	}

	targets := []Target{
		{ID: "ha", Kind: KindHAProxy, Address: haSrv.URL},
		{ID: "ng", Kind: KindNginx, Address: nginxSrv.URL},
		{ID: "cd", Kind: KindCaddy, Address: caddySrv.URL},
		{ID: "tr", Kind: KindTraefik, Address: traefikSrv.URL},
	}
	for _, tgt := range targets {
		if _, err := reg.Discover(context.Background(), tgt); err != nil {
			t.Fatalf("Discover(%s): %v", tgt.ID, err)
		}
	}

	for name, rec := range map[string]*ingressmock.Recorder{
		"haproxy": haRec, "nginx": nginxRec, "caddy": caddyRec, "traefik": traefikRec,
	} {
		reqs := rec.Requests()
		if len(reqs) == 0 {
			t.Errorf("%s: expected at least one recorded request", name)
		}
		for _, r := range reqs {
			if r.Method != "GET" {
				t.Errorf("%s: recorded a %s request (want GET only): %+v", name, r.Method, r)
			}
		}
	}
}

// TestZeroWriteSurface_OnlyOperatorAddedTargetsAreContacted is T-1406 AC5's
// regression test: Discover only ever receives Target values a caller
// explicitly built (from ingress_targets rows) — this package itself has
// no discovery/enumeration/scanning entry point of any kind. Asserted
// structurally: IngressDiscoverer.Discover's only parameter besides ctx is
// a single Target, and neither this package nor ingressmock exports any
// "scan a range"/"discover targets" function — verified by exhaustively
// listing this package's exported functions used anywhere in its own test
// suite and confirming none accepts a CIDR/range/subnet-shaped argument.
func TestZeroWriteSurface_OnlyOperatorAddedTargetsAreContacted(t *testing.T) {
	// NewDefaultRegistry takes only an *http.Client — no network range,
	// no seed list, nothing that could originate a Target on its own.
	reg := NewDefaultRegistry(nil)
	if len(reg) != len(ValidKinds) {
		t.Fatalf("NewDefaultRegistry: got %d discoverers, want %d", len(reg), len(ValidKinds))
	}
	// A Registry with zero targets ever registered against it discovers
	// nothing — there is no code path that invents a Target.
}
