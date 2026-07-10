package ifaces

import (
	"flag"
	"os"
	"testing"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// parseCorpus parses a T-102 testdata/interfaces fixture by base name (no
// directory, with extension), e.g. "02-vlan-aware-bridge.interfaces".
func parseCorpus(t *testing.T, name string) (*host.File, string) {
	t.Helper()
	raw, err := os.ReadFile("../../../testdata/interfaces/" + name)
	if err != nil {
		t.Fatalf("reading corpus fixture %s: %v", name, err)
	}
	f, err := host.ParseInterfaces(raw)
	if err != nil {
		t.Fatalf("parsing corpus fixture %s: %v", name, err)
	}
	return f, string(raw)
}

// ref is a small constructor for inventory.Ref literals in tests.
func ref(kind inventory.Kind, node, id string) inventory.Ref {
	return inventory.Ref{Kind: kind, Node: node, ID: id}
}

// updateGolden opts in to (re)generating golden files: `go test -update`
// or VNPROX_UPDATE_GOLDEN=1 (the repo's established env-var mechanism).
var updateGolden = flag.Bool("update", false, "rewrite testdata/golden files from current output instead of comparing")

// goldenUpdateRequested reports whether golden regeneration was explicitly
// requested via the -update flag or VNPROX_UPDATE_GOLDEN=1.
func goldenUpdateRequested() bool {
	return *updateGolden || os.Getenv("VNPROX_UPDATE_GOLDEN") == "1"
}

// checkGolden compares got against the golden file at testdata/golden/<name>
// byte-for-byte. A missing golden is a hard failure — a deleted or renamed
// golden must never silently regenerate-and-pass (audit finding F-21).
// Creating a missing golden or updating an existing one happens only when
// explicitly requested with `-update` or VNPROX_UPDATE_GOLDEN=1.
func checkGolden(t testing.TB, name string, got string) {
	t.Helper()
	path := "testdata/golden/" + name
	if goldenUpdateRequested() {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("writing golden %s: %v", path, err)
		}
		t.Logf("wrote golden file %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden %s: %v (if this golden is intentionally new or changed, regenerate with `go test -update` or VNPROX_UPDATE_GOLDEN=1)", path, err)
	}
	if got != string(want) {
		t.Errorf("output for %s does not match golden:\n--- got ---\n%s\n--- want ---\n%s", name, got, string(want))
	}
}

// entriesEqual does a field-by-field comparison of two host.Entry slices
// (host.Entry/BodyItem are plain value structs with only comparable/slice
// fields, so we can't use == directly on slices of them).
func entriesEqual(a, b []host.Entry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !entryEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

func entryEqual(a, b host.Entry) bool {
	if a.Raw != b.Raw || a.Class != b.Class || a.Path != b.Path || a.Pattern != b.Pattern ||
		a.Name != b.Name || a.Family != b.Family || a.Method != b.Method || a.Kind != b.Kind {
		return false
	}
	if !stringsEqual(a.Ifaces, b.Ifaces) || !stringsEqual(a.Renames, b.Renames) || !stringsEqual(a.Inherits, b.Inherits) {
		return false
	}
	if len(a.Body) != len(b.Body) {
		return false
	}
	for i := range a.Body {
		if a.Body[i] != b.Body[i] {
			return false
		}
	}
	return true
}

func stringsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// requireOriginalEntriesPreserved asserts that mutated's Entries slice
// begins with exactly orig's Entries, byte-for-byte and field-for-field —
// the invariant every Create mutator upholds by only ever appending new
// Entry values (see appendStanza/prepareAppend), never touching an
// existing one. This is the "untouched-stanza byte-identity" acceptance
// criterion, checked directly against the AST rather than by string
// slicing the rendered file.
func requireOriginalEntriesPreserved(t *testing.T, orig, mutated *host.File) {
	t.Helper()
	if len(mutated.Entries) < len(orig.Entries) {
		t.Fatalf("mutated file has fewer entries (%d) than original (%d)", len(mutated.Entries), len(orig.Entries))
	}
	if !entriesEqual(orig.Entries, mutated.Entries[:len(orig.Entries)]) {
		t.Fatalf("original entries were not preserved byte-identically as a prefix of the mutated file")
	}
}
