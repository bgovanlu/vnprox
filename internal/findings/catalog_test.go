package findings

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// catalog_test.go is T-2706 acceptance criterion 6's first half: adding a
// check to the codebase must not be able to slip past the compliance
// mapping unnoticed. It parses every package that declares check-name
// constants and fails if any of them is absent from AllCheckNames.
//
// BREAK IT TO SEE IT FIRE: delete any entry from catalog.go's
// allCheckNames whose value is a `Check… = "…"` constant in one of
// guardedPackages, and this test names it.

// guardedPackages are the source directories parsed for check-name
// constants, relative to this package's directory.
//
//nolint:gochecknoglobals // test fixture
var guardedPackages = []string{
	".",
	"../capacity",
	"../certs",
	"../drift",
	"../gitsync",
	"../topology",
}

// catalogExclusions are constants matching the scanned shape that are
// deliberately NOT findings-stream check names. Each carries the reason;
// "we could not be bothered" is not one of them.
//
//nolint:gochecknoglobals // test fixture
var catalogExclusions = map[string]string{
	"vlan_cross_check_ok": "the clean-match sentinel; adapt_lldp.go drops it rather than adapting it into a Finding",
}

func TestAllCheckNames_IsUniqueAndSorted(t *testing.T) {
	got := AllCheckNames()
	if !slices.IsSorted(got) {
		t.Errorf("AllCheckNames() is not sorted: %v", got)
	}
	seen := map[string]bool{}
	for _, n := range got {
		if strings.TrimSpace(n) == "" {
			t.Error("AllCheckNames() contains an empty check name")
		}
		if seen[n] {
			t.Errorf("AllCheckNames() contains %q twice", n)
		}
		seen[n] = true
	}
	if len(got) == 0 {
		t.Fatal("AllCheckNames() is empty")
	}
}

func TestAllCheckNames_ReturnsACopy(t *testing.T) {
	a := AllCheckNames()
	if len(a) == 0 {
		t.Fatal("AllCheckNames() is empty")
	}
	a[0] = "clobbered"
	b := AllCheckNames()
	if b[0] == "clobbered" {
		t.Error("AllCheckNames() handed out the backing catalog; a caller can corrupt it")
	}
}

func TestAllCheckNames_CoversEveryDeclaredCheckConstant(t *testing.T) {
	catalog := map[string]bool{}
	for _, n := range AllCheckNames() {
		catalog[n] = true
	}

	found := 0
	for _, dir := range guardedPackages {
		for _, c := range checkConstantsIn(t, dir) {
			found++
			if reason, excluded := catalogExclusions[c.value]; excluded {
				if catalog[c.value] {
					t.Errorf("%s declares %s = %q, which is excluded from the catalog (%s), yet the catalog lists it",
						dir, c.name, c.value, reason)
				}
				continue
			}
			if !catalog[c.value] {
				t.Errorf("%s declares %s = %q but findings.AllCheckNames() does not list it; "+
					"a check no catalog knows about cannot be reported as unmapped by a compliance profile",
					dir, c.name, c.value)
			}
		}
	}
	// A guard that finds nothing to guard is not a guard.
	if found < 30 {
		t.Fatalf("the constant scan found only %d check constants across %v; the scan is probably broken", found, guardedPackages)
	}
}

type checkConstant struct {
	name  string
	value string
}

// checkConstantsIn parses dir's Go source (excluding tests) and returns
// every `const Check… = "literal"` / `VlanCheck… = "literal"` declaration.
func checkConstantsIn(t *testing.T, dir string) []checkConstant {
	t.Helper()
	dir = filepath.Clean(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	fset := token.NewFileSet()
	var out []checkConstant
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if parseErr != nil {
			t.Fatalf("parsing %s/%s: %v", dir, name, parseErr)
		}
		out = append(out, checkConstantsInFile(file)...)
	}
	return out
}

func checkConstantsInFile(file *ast.File) []checkConstant {
	var out []checkConstant
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, ident := range vs.Names {
				if !strings.HasPrefix(ident.Name, "Check") && !strings.HasPrefix(ident.Name, "VlanCheck") {
					continue
				}
				if i >= len(vs.Values) {
					continue
				}
				lit, isLit := vs.Values[i].(*ast.BasicLit)
				if !isLit || lit.Kind != token.STRING {
					continue
				}
				v, unquoteErr := strconv.Unquote(lit.Value)
				if unquoteErr != nil {
					continue
				}
				out = append(out, checkConstant{name: ident.Name, value: v})
			}
		}
	}
	return out
}
