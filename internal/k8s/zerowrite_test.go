// zerowrite_test.go is T-1501 AC4's regression test: every method on
// internal/k8s.Client is GET-only — no code path in this package can issue
// a mutating call against a k8s API server. Two independent checks (the
// same pairing internal/ingress/zerowrite_test.go established for T-1406):
//
//  1. Source inspection: none of this package's non-test .go files
//     reference http.MethodPost/Put/Patch/Delete, or the literal strings
//     "POST"/"PUT"/"PATCH"/"DELETE" as an HTTP method.
//  2. Reflection + live behavior: every exported method on *Client is
//     enumerated via reflection and confirmed to be one of the four known
//     read-only accessors (closing off "a fifth method was added and
//     forgotten about here"), then every one of those four is actually
//     invoked against an instrumented k8smock server, asserting every
//     request it received was GET.
package k8s_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/k8s"
	"github.com/bgovanlu/vnprox/internal/k8smock"
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
		// doc.go's prose necessarily names these tokens to describe the
		// invariant this test enforces everywhere else — excluded from the
		// scan, not from the invariant (identical exclusion
		// internal/ingress/zerowrite_test.go makes for its own doc.go).
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
				t.Errorf("%s references mutating-verb token %q — Client must be GET-only", name, tok)
			}
		}
	}
}

// TestZeroWriteSurface_ClientExposesOnlyKnownReadMethods enumerates every
// exported method *k8s.Client declares via reflection and fails if it
// finds anything beyond the four documented read accessors — the
// "reflection ... method-inventory test" AC4 names, so a future edit that
// adds a fifth Client method can't silently slip past this file's other
// (necessarily hand-maintained) checks.
func TestZeroWriteSurface_ClientExposesOnlyKnownReadMethods(t *testing.T) {
	want := []string{"Nodes", "Pods", "Services", "KubeSystemDaemonSets"}
	sort.Strings(want)

	typ := reflect.TypeOf(&k8s.Client{})
	var got []string
	for i := 0; i < typ.NumMethod(); i++ {
		got = append(got, typ.Method(i).Name)
	}
	sort.Strings(got)

	if len(got) != len(want) {
		t.Fatalf("Client method set = %v, want exactly %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Client method set = %v, want exactly %v", got, want)
		}
	}
}

func TestZeroWriteSurface_EveryClientMethodIssuesOnlyGET(t *testing.T) {
	f := loadClusterFixture(t, "cluster-calico.yaml")
	srv, rec := k8smock.NewServer(f)
	defer srv.Close()

	c := &k8s.Client{HTTPClient: srv.Client(), BaseURL: srv.URL}
	ctx := context.Background()

	if _, err := c.Nodes(ctx); err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	if _, err := c.Pods(ctx); err != nil {
		t.Fatalf("Pods: %v", err)
	}
	if _, err := c.Services(ctx); err != nil {
		t.Fatalf("Services: %v", err)
	}
	if _, err := c.KubeSystemDaemonSets(ctx); err != nil {
		t.Fatalf("KubeSystemDaemonSets: %v", err)
	}

	reqs := rec.Requests()
	if len(reqs) != 4 {
		t.Fatalf("recorded %d requests, want 4", len(reqs))
	}
	for _, r := range reqs {
		if r.Method != "GET" {
			t.Errorf("recorded a %s request (want GET only): %+v", r.Method, r)
		}
	}
}
