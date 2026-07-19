// zerowrite_test.go is T-1503 AC4's regression test: Ceph is read-only
// forever — PVE's own Ceph tooling keeps sole ownership of Ceph
// configuration. Three independent checks (mirroring internal/k8s's own
// zerowrite_test.go, T-1501's identical invariant for its domain):
//
//  1. Source inspection: neither this package's own non-test .go files, nor
//     internal/pve/ceph.go specifically (the one file in the general-purpose
//     internal/pve client scoped to Ceph — that package legitimately has
//     write methods for other domains, e.g. SDN/firewall/guest config, so
//     only ceph.go itself is in scope here) reference a mutating HTTP verb
//     token.
//  2. Live behavior: pve.Client.CephConfig/CephOSDs, exercised against a
//     request-recording wrapper around pvemock, issue only GET requests
//     against Ceph routes (the client's own POST /access/ticket login is
//     expected and excluded — that's authentication, not a Ceph write).
//  3. internal/change/op.go (docs/data-model.md §3's op vocabulary — the
//     only place a changeset op type can be declared) contains no
//     "ceph."-prefixed OpType literal — grep-verifiable, so a future
//     change adding one can't slip past this test either.
package ceph_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
)

var forbiddenVerbTokens = []string{
	"http.MethodPost", "http.MethodPut", "http.MethodPatch", "http.MethodDelete",
	`"POST"`, `"PUT"`, `"PATCH"`, `"DELETE"`,
}

func scanDirForMutatingVerbs(t *testing.T, dir string, only func(name string) bool) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dir, err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "doc.go" {
			continue
		}
		if only != nil && !only(name) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", name, err)
		}
		content := string(data)
		for _, tok := range forbiddenVerbTokens {
			if strings.Contains(content, tok) {
				t.Errorf("%s/%s references mutating-verb token %q — Ceph access must be GET-only", dir, name, tok)
			}
		}
	}
}

func TestZeroWriteSurface_SourceNeverReferencesMutatingVerbs(t *testing.T) {
	// Every file in this package: internal/ceph has no write surface of any
	// kind, so nothing here should ever reference a mutating verb.
	scanDirForMutatingVerbs(t, ".", nil)
	// internal/pve is a general-purpose client with legitimate write
	// methods for other domains (SDN, firewall, guest config, ...) — only
	// ceph.go, the one file this task added, is in scope for this check.
	scanDirForMutatingVerbs(t, "../pve", func(name string) bool { return name == "ceph.go" })
}

// recordingHandler wraps srv, recording every non-auth request's method
// (path, method) before delegating — POST /access/ticket (login) is
// deliberately not recorded, since it's authentication, not a Ceph read or
// write.
type recordingHandler struct {
	inner   http.Handler
	methods []string
}

func (r *recordingHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path != "/api2/json/access/ticket" {
		r.methods = append(r.methods, req.Method)
	}
	r.inner.ServeHTTP(w, req)
}

// TestZeroWriteSurface_PVEClientMethodsIssueOnlyGET exercises both of
// internal/pve's Ceph methods against a request-recording wrapper around
// pvemock, confirming every request they issue is GET — the live-behavior
// half of AC4, mirroring internal/k8s's identical live-request assertion.
func TestZeroWriteSurface_PVEClientMethodsIssueOnlyGET(t *testing.T) {
	f, err := pvemock.LoadFixture(fixtureCephClean)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	rec := &recordingHandler{inner: pvemock.NewServer(f)}
	ts := httptest.NewServer(rec)
	defer ts.Close()

	c, err := pve.New(pve.Config{
		APIURL:   ts.URL,
		Auth:     pve.AuthTicket,
		Username: "root@pam",
		Password: "vnprox-mock",
	})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}

	ctx := context.Background()
	if _, err := c.CephConfig(ctx); err != nil {
		t.Fatalf("CephConfig: %v", err)
	}
	if _, err := c.CephOSDs(ctx, "pve1"); err != nil {
		t.Fatalf("CephOSDs: %v", err)
	}

	for _, m := range rec.methods {
		if m != http.MethodGet {
			t.Errorf("recorded a non-GET request: %s (full sequence: %v)", m, rec.methods)
		}
	}
	// Sanity: both Ceph calls above must have actually reached the mock —
	// if this is ever near-zero, the test is accidentally not exercising
	// anything.
	if len(rec.methods) != 2 {
		t.Fatalf("recorded %d requests (want exactly 2: CephConfig + CephOSDs) — %v", len(rec.methods), rec.methods)
	}
}

// TestZeroWriteSurface_NoCephOpTypeInChangeEngine is AC4's grep-verifiable
// half: internal/change/op.go (the sole place a changeset OpType literal
// can be declared, docs/data-model.md §3) contains no "ceph."-prefixed
// value — no changeset op type exists for Ceph, ever.
func TestZeroWriteSurface_NoCephOpTypeInChangeEngine(t *testing.T) {
	data, err := os.ReadFile("../change/op.go")
	if err != nil {
		t.Fatalf("ReadFile(../change/op.go): %v", err)
	}
	if strings.Contains(string(data), `"ceph.`) {
		t.Fatalf("internal/change/op.go declares a \"ceph.\"-prefixed OpType — Ceph must never have a changeset op")
	}
}
