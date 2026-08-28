// SPDX-License-Identifier: Apache-2.0

package ipam_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoExternalSyncChangesetOp is T-1203 AC5: no ipam.external_sync.* (or any)
// changeset op type exists, and the sync write path never touches
// internal/change. This is enforced two ways, both structural (not behavioral),
// so a future edit that tried to route external-IPAM writes through the change
// engine would fail the build's test gate:
//
//  1. internal/change's op-type registry contains no "external_sync" op string.
//  2. internal/ipam (which owns the sync write path, sync.go) never imports
//     internal/change — external-IPAM writes are outside the change engine by
//     construction, so the dependency simply must not exist.
func TestNoExternalSyncChangesetOp(t *testing.T) {
	repoRoot := repoRootFromHere(t)

	// (1) No op-type constant string mentions external_sync anywhere in the
	// change engine's op definitions.
	opSrc, err := os.ReadFile(filepath.Join(repoRoot, "internal", "change", "op.go"))
	if err != nil {
		t.Fatalf("reading internal/change/op.go: %v", err)
	}
	if strings.Contains(string(opSrc), "external_sync") {
		t.Errorf("internal/change/op.go mentions external_sync — no external-IPAM changeset op may exist (T-1203 AC5)")
	}

	// (2) No non-test file in internal/ipam imports internal/change.
	ipamDir := filepath.Join(repoRoot, "internal", "ipam")
	entries, err := os.ReadDir(ipamDir)
	if err != nil {
		t.Fatalf("reading internal/ipam: %v", err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(ipamDir, name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, imp := range f.Imports {
			if strings.Contains(imp.Path.Value, "internal/change") {
				t.Errorf("%s imports internal/change — the external-IPAM sync path must never touch the change engine (T-1203 AC5)", name)
			}
		}
	}
}

// repoRootFromHere walks up from the working directory (internal/ipam) to the
// module root (the directory holding go.mod).
func repoRootFromHere(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate go.mod above the test's working directory")
		}
		dir = parent
	}
}
