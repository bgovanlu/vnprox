// SPDX-License-Identifier: Apache-2.0

package explain

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// forbiddenImportSubstrings names import-path fragments that would let this
// package reach a network or a model backend. Substring match, not exact,
// so "net/http", "net/rpc", and a future "net/http/httputil" all get
// caught by "net/" without listing every stdlib net/* package by name.
//
//nolint:gochecknoglobals // test fixture
var forbiddenImportSubstrings = []string{
	"net/http",
	"net/rpc",
	"net/smtp",
	"google.golang.org/grpc",
	"os/exec", // could shell out to a network-capable binary
}

// TestNoNetworkCapableImports is the task card's "No network call, no
// model client, no API key config — verified by a test asserting the
// package imports nothing network-capable" requirement, checked
// statically (parse, don't run) so it cannot pass by accident on a
// platform where the forbidden call happens not to fire during the test
// run. Mirrors internal/findings/catalog_test.go's own go/parser-based
// source scan.
func TestNoNetworkCapableImports(t *testing.T) {
	dir := "."
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	fset := token.NewFileSet()
	filesScanned := 0
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		filesScanned++
		file, parseErr := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ImportsOnly)
		if parseErr != nil {
			t.Fatalf("parsing %s: %v", name, parseErr)
		}
		for _, imp := range file.Imports {
			checkImport(t, name, imp)
		}
	}
	if filesScanned == 0 {
		t.Fatal("scanned zero source files; the scan is broken and this test proves nothing")
	}
}

func checkImport(t *testing.T, file string, imp *ast.ImportSpec) {
	t.Helper()
	path, err := strconv.Unquote(imp.Path.Value)
	if err != nil {
		t.Fatalf("%s: unquoting import path %s: %v", file, imp.Path.Value, err)
	}
	for _, bad := range forbiddenImportSubstrings {
		if strings.Contains(path, bad) {
			t.Errorf("%s imports %q, which contains forbidden substring %q — this package must work on an air-gapped install with no network and no model backend", file, path, bad)
		}
	}
}
