// zerowrite_test.go is T-1206 AC4's regression test: PBS network awareness is
// read-only forever — PVE owns storage.cfg and the backup schedule; vnprox
// only reads PVE's own knowledge of them. Three independent checks:
//
//  1. Source inspection: neither this package's own non-test .go files, nor
//     internal/pve/storage.go specifically (the one file in the
//     general-purpose internal/pve client scoped to T-1206's storage/backup
//     reads — that package legitimately has write methods for other domains,
//     so only storage.go is in scope), reference a mutating HTTP verb token.
//  2. Live behavior: pve.Client.ListStorages/ListBackupJobs issue only GET
//     against the mock (the login POST is excluded — that's authentication).
//  3. internal/change/op.go (docs/data-model.md §3's op vocabulary — the only
//     place a changeset OpType literal can be declared) contains no
//     "pbs."-prefixed value, so a future change adding one can't slip past.
package pbs_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
		for _, tok := range forbiddenVerbTokens {
			if strings.Contains(string(data), tok) {
				t.Errorf("%s/%s references mutating-verb token %q — PBS access must be GET-only", dir, name, tok)
			}
		}
	}
}

func TestZeroWriteSurface_SourceNeverReferencesMutatingVerbs(t *testing.T) {
	scanDirForMutatingVerbs(t, ".", nil)
	scanDirForMutatingVerbs(t, "../pve", func(name string) bool { return name == "storage.go" })
}

// recordingHandler records every non-auth request's method before delegating.
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

func TestZeroWriteSurface_PVEClientMethodsIssueOnlyGET(t *testing.T) {
	f, err := pvemock.LoadFixture(fixtureBackupPaths)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	rec := &recordingHandler{inner: pvemock.NewServer(f)}
	ts := httptest.NewServer(rec)
	defer ts.Close()

	c := newTicketClient(t, ts.URL)
	ctx := context.Background()
	if _, err := c.ListStorages(ctx); err != nil {
		t.Fatalf("ListStorages: %v", err)
	}
	if _, err := c.ListBackupJobs(ctx); err != nil {
		t.Fatalf("ListBackupJobs: %v", err)
	}

	for _, m := range rec.methods {
		if m != http.MethodGet {
			t.Errorf("recorded a non-GET request: %s (full sequence: %v)", m, rec.methods)
		}
	}
	if len(rec.methods) != 2 {
		t.Fatalf("recorded %d requests (want exactly 2: ListStorages + ListBackupJobs) — %v", len(rec.methods), rec.methods)
	}
}

func TestZeroWriteSurface_NoPBSOpTypeInChangeEngine(t *testing.T) {
	data, err := os.ReadFile("../change/op.go")
	if err != nil {
		t.Fatalf("ReadFile(../change/op.go): %v", err)
	}
	if strings.Contains(string(data), `"pbs.`) {
		t.Fatalf("internal/change/op.go declares a \"pbs.\"-prefixed OpType — PBS must never have a changeset op")
	}
}
