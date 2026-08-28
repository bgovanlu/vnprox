// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestVnproxctlDoesImportSigstore is the sanity companion to
// cmd/vnproxd's TestVnproxdDoesNotImportSigstore: it proves that scan is
// actually distinguishing something, rather than passing because `go list
// -deps | grep sigstore` never matches anything in this build regardless of
// which binary is scanned (e.g. a typo'd grep pattern, or sigstore-go
// having been removed from the module graph entirely). vnproxctl is where
// T-3709's Sigstore verification code (internal/hubreg/sigstoreverify) now
// lives, via cmd/vnproxctl/hubcmd_sigstore.go, so this must be non-empty.
func TestVnproxctlDoesImportSigstore(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps ./cmd/vnproxctl: %v\n%s", err, out)
	}
	var hits int
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "sigstore") {
			hits++
		}
	}
	if hits == 0 {
		t.Fatal("cmd/vnproxctl's build graph contains no sigstore package — either the sigstore-in-vnproxctl feature regressed, or cmd/vnproxd's own \"does not import sigstore\" test is not meaningfully distinguishing anything")
	}
}
