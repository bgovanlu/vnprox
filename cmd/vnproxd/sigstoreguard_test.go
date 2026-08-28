// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestVnproxdDoesNotImportSigstore is T-3709's structural half of the
// vnproxd/vnproxctl split: full sigstore-go verification lives only in
// vnproxctl (cmd/vnproxctl, internal/hubreg/sigstoreverify — see that
// package's doc comment for the full rationale). The daemon keeps its
// existing, unchanged Ed25519 index-verification path
// (internal/hubreg.Gate/Verify) and must never pull sigstore-go's ~330
// transitive modules (a TUF client, a Certificate Transparency verifier,
// gRPC, OpenTelemetry) into the process that controls host networking —
// the abandoned `sigstore-in-daemon` branch (commit 562de983) grew
// vnproxd from 38.3 MB to 54.3 MB and the module graph from 64 to ~400
// doing exactly that.
//
// This scans vnproxd's REAL, TRANSITIVE build graph via `go list -deps`
// rather than a hand-kept import list, so a future dependency added
// anywhere in the daemon's own dependency tree that happens to pull in
// sigstore-go transitively fails this test immediately — the same "the
// build checks it, not a reviewer remembering a rule" property
// internal/presence/deps_test.go's TestChangeEngineDoesNotImportPresence
// and internal/mcp/registry_test.go's registry-enumeration tests use for
// their own structural boundaries. Those two use go/build.ImportDir
// (direct imports only) and reflection over a hardcoded allowlist,
// respectively; this one needs the full transitive graph, which only
// `go list -deps` computes, so it shells out rather than reusing either
// pattern verbatim.
func TestVnproxdDoesNotImportSigstore(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps ./cmd/vnproxd: %v\n%s", err, out)
	}
	var hits []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "sigstore") {
			hits = append(hits, line)
		}
	}
	if len(hits) > 0 {
		t.Errorf("cmd/vnproxd transitively imports sigstore-go packages, which must live only in cmd/vnproxctl (see this test's doc comment):\n%s", strings.Join(hits, "\n"))
	}

	// Guard against this test silently passing because the daemon's build
	// list emptied out entirely (e.g. a build-tag mistake in the command
	// above) — a sanity check that the scan is reading a real, populated
	// dependency graph.
	if len(out) < 100 {
		t.Fatalf("go list -deps ./cmd/vnproxd produced suspiciously little output (%d bytes) — this scan is not reading a real build graph:\n%s", len(out), out)
	}
}
