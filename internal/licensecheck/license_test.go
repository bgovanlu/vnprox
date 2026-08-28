// SPDX-License-Identifier: Apache-2.0

// Package licensecheck exists only to hold this test.
//
// T-2106: the repository shipped 617 commits and three release arcs with no
// licence at all, which nothing noticed because nothing was looking. These
// assertions are the "looking". They are cheap, they run in `make check`, and
// they fail loudly if the attribution files are deleted, emptied, or dropped
// from the package payload.
package licensecheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot walks up from this package to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 10; i++ {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not locate the module root")
	return ""
}

func read(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return string(b)
}

func TestLicenseFilesExistAndAreSubstantive(t *testing.T) {
	root := repoRoot(t)

	tests := []struct {
		file     string
		mustHave []string
		minBytes int
	}{
		{
			file:     "LICENSE",
			minBytes: 10_000,
			mustHave: []string{
				"Apache License",
				"Version 2.0, January 2004",
				// The appendix boilerplate must name a real holder, not the
				// placeholder the upstream text ships with.
				"Copyright 2026 Brian Govanlu",
			},
		},
		{
			file:     "NOTICE",
			minBytes: 400,
			mustHave: []string{
				"vnprox",
				"Copyright 2026 Brian Govanlu",
				"Apache License",
				// The attribution ask, and the Proxmox trademark/AGPL
				// boundary, are the two things this file exists to state.
				"THIRD-PARTY-LICENSES.md",
				"AGPL",
			},
		},
		{
			file:     "THIRD-PARTY-LICENSES.md",
			minBytes: 4_000,
			mustHave: []string{
				"elkjs",
				"EPL-2.0",
				"monaco-editor",
				"go-chi",
			},
		},
		{
			file:     "packaging/debian/copyright",
			minBytes: 800,
			mustHave: []string{
				"Format: https://www.debian.org/doc/packaging-manuals/copyright-format/1.0/",
				"Upstream-Name: vnprox",
				"License: Apache-2.0",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			body := read(t, root, tc.file)
			if len(body) < tc.minBytes {
				t.Errorf("%s is %d bytes, want at least %d — a stub is not a licence", tc.file, len(body), tc.minBytes)
			}
			for _, want := range tc.mustHave {
				if !strings.Contains(body, want) {
					t.Errorf("%s does not contain %q", tc.file, want)
				}
			}
			if strings.Contains(body, "[yyyy]") || strings.Contains(body, "[name of copyright owner]") {
				t.Errorf("%s still contains the unfilled upstream placeholder", tc.file)
			}
		})
	}
}

// The .deb must actually carry the attribution files. Apache-2.0 §4 requires
// the NOTICE to travel with a redistribution, and the bundled SPA contains
// EPL-2.0 code (elkjs) whose recipients must be able to find its licence.
// Checking the packaging recipe is the cheapest way to keep that true; the
// alternative is discovering it from a downstream complaint.
func TestPackagingShipsTheLicenseFiles(t *testing.T) {
	root := repoRoot(t)
	mk := read(t, root, "packaging/Makefile")

	for _, want := range []string{
		"usr/share/doc/vnprox/copyright",
		"usr/share/doc/vnprox/LICENSE",
		"usr/share/doc/vnprox/NOTICE",
		"usr/share/doc/vnprox/THIRD-PARTY-LICENSES.md",
	} {
		if !strings.Contains(mk, want) {
			t.Errorf("packaging/Makefile does not install %s", want)
		}
	}
}

// The generated third-party list must stay in step with what is actually
// declared as a runtime dependency. This does not re-run the generator (that
// needs network access for `npx`); it checks the invariant that matters —
// every package.json runtime dependency appears in the generated file.
func TestThirdPartyListCoversEveryRuntimeDependency(t *testing.T) {
	root := repoRoot(t)
	pkg := read(t, root, "web/package.json")
	list := read(t, root, "THIRD-PARTY-LICENSES.md")

	deps, ok := runtimeDependencies(pkg)
	if !ok {
		t.Fatal(`could not find a "dependencies" block in web/package.json — this test would otherwise pass over an empty set`)
	}
	// Anti-vacuity: the parse must find a plausible number, including a
	// package known to be there.
	if len(deps) < 10 {
		t.Fatalf("parsed only %d runtime dependencies, expected at least 10", len(deps))
	}
	var sawReact bool
	for _, d := range deps {
		if d == "react" {
			sawReact = true
		}
	}
	if !sawReact {
		t.Fatal("parse did not find react among the runtime dependencies; the parser is wrong")
	}

	var missing []string
	for _, d := range deps {
		if !strings.Contains(list, "`"+d+"`") {
			missing = append(missing, d)
		}
	}
	if len(missing) > 0 {
		t.Errorf("THIRD-PARTY-LICENSES.md is missing %v — run `make third-party`", missing)
	}
}

// runtimeDependencies extracts the keys of package.json's "dependencies"
// object. Hand-parsed rather than unmarshalled into a map so the test does not
// depend on field ordering or on adding a JSON dependency to this package.
func runtimeDependencies(pkg string) ([]string, bool) {
	start := strings.Index(pkg, `"dependencies"`)
	if start < 0 {
		return nil, false
	}
	open := strings.Index(pkg[start:], "{")
	if open < 0 {
		return nil, false
	}
	open += start
	depth := 0
	end := -1
	for i := open; i < len(pkg); i++ {
		switch pkg[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return nil, false
	}

	var out []string
	for _, line := range strings.Split(pkg[open:end], "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, `"`) {
			continue
		}
		rest := line[1:]
		q := strings.Index(rest, `"`)
		if q <= 0 {
			continue
		}
		out = append(out, rest[:q])
	}
	return out, true
}
