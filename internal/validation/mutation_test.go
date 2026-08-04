package validation

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// mutatingVerbPattern matches the exact verb list T-1801's acceptance
// criterion 3 specifies: set, create, delete, ifreload, ifup, ifdown, as
// whole words (so e.g. "offset" or "resettle" don't false-positive, but
// "pvesh set" or a raw "ifreload" call do). Keep this in sync with the AC
// text — it is deliberately narrow and literal, not a general "looks like
// a write" heuristic.
var mutatingVerbPattern = regexp.MustCompile(`\b(set|create|delete|ifreload|ifup|ifdown)\b`)

// mutatesBannerPattern matches the literal MUTATES=1 banner every mutating
// harness script must declare near its top (planning/validation/harness/
// lib/common.sh's contract).
var mutatesBannerPattern = regexp.MustCompile(`(?m)^MUTATES=1\b`)

// harnessScripts returns every top-level planning/validation/harness/*.sh
// entry point — the files a human actually runs
// (`ssh node 'bash -s' < harness/<section>.sh`), per the deliverable's own
// naming. lib/ is a sourced-only helper library, never run directly, and
// is intentionally not part of this scan (see T-1801's report for why:
// scanning entry points, where a mutating verb is actually invoked, is
// what "hard to fool" means here — an author who adds `pvesh set` to a
// section script trips this test on that script, regardless of what
// helpers it sources).
func harnessScripts(t *testing.T) []string {
	t.Helper()
	dir := filepath.Join("..", "..", "planning", "validation", "harness")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var scripts []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".sh" {
			continue
		}
		scripts = append(scripts, filepath.Join(dir, e.Name()))
	}
	if len(scripts) == 0 {
		t.Fatalf("no harness/*.sh scripts found under %s", dir)
	}
	return scripts
}

// checkMutationBanner is the actual rule T-1801 acceptance criterion 3
// asserts: if a script's source contains any of the AC's mutating verbs as
// a whole word, it must also declare MUTATES=1. It returns a non-empty
// reason string when the rule is violated.
func checkMutationBanner(source []byte) (violation string) {
	verbs := mutatingVerbPattern.FindAllString(string(source), -1)
	if len(verbs) == 0 {
		return ""
	}
	if mutatesBannerPattern.Match(source) {
		return ""
	}
	return "contains mutating verb(s) " + uniqueJoin(verbs) + " but no MUTATES=1 banner"
}

func uniqueJoin(words []string) string {
	seen := map[string]bool{}
	var out string
	for _, w := range words {
		if seen[w] {
			continue
		}
		seen[w] = true
		if out != "" {
			out += ","
		}
		out += w
	}
	return out
}

// TestNoHarnessScriptMutatesWithoutBanner is T-1801 acceptance criterion 3.
func TestNoHarnessScriptMutatesWithoutBanner(t *testing.T) {
	for _, path := range harnessScripts(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading %s: %v", path, err)
			}
			if v := checkMutationBanner(source); v != "" {
				t.Errorf("%s: %s", path, v)
			}
		})
	}
}

// TestMutationBannerCheck_CatchesAViolation proves the check above isn't
// vacuously true (e.g. because none of the real scripts happen to trip
// it): a synthetic script using a banned verb with no banner must be
// flagged, and one that declares the banner must not be.
func TestMutationBannerCheck_CatchesAViolation(t *testing.T) {
	bad := []byte("#!/usr/bin/env bash\nMUTATES=0\npvesh set /nodes/pve1/network/vmbr0 -mtu 1500\n")
	if v := checkMutationBanner(bad); v == "" {
		t.Fatal("checkMutationBanner: a script calling `pvesh set` with MUTATES=0 (no MUTATES=1 banner) was not flagged")
	}

	good := []byte("#!/usr/bin/env bash\nMUTATES=1\npvesh set /nodes/pve1/network/vmbr0 -mtu 1500\n")
	if v := checkMutationBanner(good); v != "" {
		t.Fatalf("checkMutationBanner: a script with a MUTATES=1 banner was still flagged: %s", v)
	}

	readOnly := []byte("#!/usr/bin/env bash\nMUTATES=0\npvesh get /nodes/pve1/network\n")
	if v := checkMutationBanner(readOnly); v != "" {
		t.Fatalf("checkMutationBanner: a read-only script with no mutating verb was flagged: %s", v)
	}

	// "ifreload" specifically, called out in the AC text, and in a future-
	// author scenario where only the verb (not "set"/"create"/"delete")
	// is present.
	ifreload := []byte("#!/usr/bin/env bash\nMUTATES=0\nifreload -a\n")
	if v := checkMutationBanner(ifreload); v == "" {
		t.Fatal("checkMutationBanner: a script calling `ifreload` with no MUTATES=1 banner was not flagged")
	}
}
