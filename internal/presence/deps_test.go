package presence_test

import (
	"go/build"
	"path/filepath"
	"strings"
	"testing"
)

// TestChangeEngineDoesNotImportPresence is T-2805 AC4's structural half.
//
// AC4 says a held lock on an entity in an approved changeset must not
// prevent applying it. The end-to-end proof of that is an HTTP-level test
// (internal/api's TestChangesets_HeldLockNeverBlocksApply, which holds a
// lock and then applies). This test proves something the end-to-end one
// cannot: that no FUTURE apply path can consult a lock either.
//
// internal/change owns stage → validate → diff → apply → confirm/rollback,
// the product's core safety guarantee. If it cannot see internal/presence,
// no code inside it — today's or tomorrow's — can turn an advisory lock into
// a second gate on that path. That is the same structural-boundary argument
// docs/architecture.md §11 makes for the plugin SDK's stage-only Stager and
// §13.1 for the MCP tool manifest: a property the build checks, not a
// convention a reviewer has to remember.
//
// It scans the real package's import list rather than a hand-kept file list,
// so a new file in internal/change is covered the moment it exists.
func TestChangeEngineDoesNotImportPresence(t *testing.T) {
	const (
		modulePath = "github.com/bgovanlu/vnprox"
		forbidden  = modulePath + "/internal/presence"
	)

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}

	for _, pkg := range []string{"internal/change", "internal/change/ifaces"} {
		dir := filepath.Join(root, pkg)
		p, importErr := build.ImportDir(dir, build.ImportComment)
		if importErr != nil {
			t.Fatalf("reading %s: %v", pkg, importErr)
		}
		// Imports covers the package's non-test files; TestImports/
		// XTestGoFiles are deliberately included too, since a test helper
		// that reached for a lock would be evidence the seam had been drawn
		// in the wrong place even if production code did not use it yet.
		all := append(append([]string{}, p.Imports...), p.TestImports...)
		all = append(all, p.XTestImports...)
		for _, imp := range all {
			if imp == forbidden || strings.HasPrefix(imp, forbidden+"/") {
				t.Errorf("%s imports %s.\n\n"+
					"T-2805's advisory locks must never sit in the apply path as a refusal: "+
					"\"a lock never prevents an emergency change; it prevents an accidental one\". "+
					"The change engine not being able to SEE the lock service is what makes that "+
					"structural rather than a convention. If a lock check really belongs in the "+
					"change engine, that is a decision for a task card, not an import.", pkg, forbidden)
			}
		}
	}

	// Guard against this test silently passing because the packages moved:
	// if internal/change stopped importing internal/store, the scan is
	// reading the wrong thing.
	p, err := build.ImportDir(filepath.Join(root, "internal/change"), build.ImportComment)
	if err != nil {
		t.Fatalf("re-reading internal/change: %v", err)
	}
	var sawStore bool
	for _, imp := range p.Imports {
		if imp == modulePath+"/internal/store" {
			sawStore = true
		}
	}
	if !sawStore {
		t.Fatal("internal/change does not import internal/store — this scan is not reading the package it thinks it is, " +
			"so its \"no presence import\" result is meaningless")
	}
}
