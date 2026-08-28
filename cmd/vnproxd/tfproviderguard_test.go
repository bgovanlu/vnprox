// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestCmdDoesNotImportTerraformProvider is T-4001's module-boundary guard:
// contrib/terraform-provider-vnprox is its own Go module (own go.mod), built
// on terraform-plugin-framework/terraform-plugin-go — a large dependency
// tree (a TUF-adjacent plugin protocol stack, gRPC, go-plugin's own
// subprocess-and-handshake machinery) that must never end up linked into
// vnproxd (the process that controls host networking) or vnproxctl. This is
// the same structural isolation commit 34c11588 gives sigstore-go, kept out
// of the daemon and scoped to vnproxctl alone
// (cmd/vnproxd/sigstoreguard_test.go, whose `go list -deps` pattern this
// test mirrors exactly) — except here BOTH cmd/vnproxd and cmd/vnproxctl
// must stay clear, since neither binary has any legitimate reason to link a
// Terraform provider's plugin-protocol server.
//
// Unlike sigstore-go (deliberately imported by cmd/vnproxctl's own hub
// verification path), there is no code anywhere in cmd/vnproxd or
// cmd/vnproxctl that imports contrib/terraform-provider-vnprox today, and
// there never should be: a separate Go module (this file's own doc comment;
// contrib/terraform-provider-vnprox/go.mod's distinct module path) cannot be
// imported by internal-package-visibility rules in the first place (its
// import path does not share this module's github.com/bgovanlu/vnprox
// prefix), so this test's job is proving that boundary stays a genuine
// module split — not merely that nobody happened to import it yet — the
// same "the build checks it, not a reviewer remembering a rule" property
// TestVnproxdDoesNotImportSigstore documents for its own boundary.
//
// This scans the REAL, TRANSITIVE build graph via `go list -deps` rather
// than a hand-kept import list, matching T-4001's acceptance criterion 2
// verbatim: "go list -deps ./cmd/... from the main module contains zero
// terraform-plugin-* packages."
func TestCmdDoesNotImportTerraformProvider(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps . (from cmd/vnproxd): %v\n%s", err, out)
	}
	var hits []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "terraform-plugin") {
			hits = append(hits, line)
		}
	}
	if len(hits) > 0 {
		t.Errorf("cmd/vnproxd transitively imports terraform-plugin-* packages, which must live only in contrib/terraform-provider-vnprox's own module (see this test's doc comment):\n%s", strings.Join(hits, "\n"))
	}

	if len(out) < 100 {
		t.Fatalf("go list -deps . (from cmd/vnproxd) produced suspiciously little output (%d bytes) — this scan is not reading a real build graph:\n%s", len(out), out)
	}
}

// TestVnproxctlDoesNotImportTerraformProvider is this test's other half —
// see TestCmdDoesNotImportTerraformProvider's doc comment. Run from
// cmd/vnproxd (via a relative "../vnproxctl" package argument, since Go
// test binaries don't shell out relative to an arbitrary directory) rather
// than as a same-named test in cmd/vnproxctl, so both halves of the AC2
// guarantee live next to sigstoreguard_test.go's own precedent in one
// place — a future reader auditing "what keeps the daemon/CLI free of the
// Terraform provider's dependency tree" finds both checks in the same file
// instead of needing to know a second file in a second package exists.
func TestVnproxctlDoesNotImportTerraformProvider(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "../vnproxctl").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps ../vnproxctl (from cmd/vnproxd): %v\n%s", err, out)
	}
	var hits []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "terraform-plugin") {
			hits = append(hits, line)
		}
	}
	if len(hits) > 0 {
		t.Errorf("cmd/vnproxctl transitively imports terraform-plugin-* packages, which must live only in contrib/terraform-provider-vnprox's own module (see TestCmdDoesNotImportTerraformProvider's doc comment):\n%s", strings.Join(hits, "\n"))
	}

	if len(out) < 100 {
		t.Fatalf("go list -deps ../vnproxctl (from cmd/vnproxd) produced suspiciously little output (%d bytes) — this scan is not reading a real build graph:\n%s", len(out), out)
	}
}
