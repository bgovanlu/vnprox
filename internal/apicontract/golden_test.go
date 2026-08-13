package apicontract

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
)

// updateGolden regenerates testdata/golden/*.json from the real handlers'
// actual current responses instead of asserting against them:
//
//	go test ./internal/apicontract/... -update
//
// This is how the fixtures are produced in the first place (T-1106's card:
// golden fixtures must be "generated from/verified against the real
// handlers, not hand-written independently") — the same pattern
// internal/spec/testhelpers_test.go's own -update flag uses for its golden
// spec YAML.
var updateGolden = flag.Bool("update", false, "update apicontract golden fixtures from live handler responses")

func goldenPath(name string) string {
	return filepath.Join("testdata", "golden", name+".json")
}

// assertGolden marshals got (already normalized by the caller — volatile
// fields like ids/timestamps zeroed or redacted) as indented JSON and
// compares it against the checked-in golden file, or rewrites it under
// -update. A deliberate handler schema break changes got's shape/values
// and fails this comparison, which is this suite's whole purpose.
func assertGolden(t *testing.T, name string, got any) {
	t.Helper()
	gotJSON, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatalf("marshaling golden comparison value for %s: %v", name, err)
	}
	gotJSON = append(gotJSON, '\n')

	path := goldenPath(name)
	if *updateGolden {
		if mkErr := os.MkdirAll(filepath.Dir(path), 0o755); mkErr != nil {
			t.Fatalf("mkdir testdata/golden: %v", mkErr)
		}
		if writeErr := os.WriteFile(path, gotJSON, 0o644); writeErr != nil {
			t.Fatalf("writing golden %s: %v", path, writeErr)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden %s (run `go test ./internal/apicontract/... -update` to create it): %v", path, err)
	}
	if !bytes.Equal(want, gotJSON) {
		t.Errorf("response for %s does not match golden %s (a handler schema change? re-run with -update after confirming the new shape is intentional).\n--- got ---\n%s\n--- want ---\n%s",
			name, path, gotJSON, want)
	}
}

// redactedChangeset copies a changesetResponse with every run-to-run
// volatile field (id, timestamps, confirm deadline) replaced by a fixed
// placeholder, so the golden file asserts on everything that's actually
// part of the documented contract (status, ops, findings, touchesMgmtPath)
// without flaking on a fresh ULID or wall-clock timestamp each run.
func redactedChangeset(c changesetResponse) changesetResponse {
	c.ID = "<id>"
	c.CreatedAt = 0
	c.UpdatedAt = 0
	if c.ConfirmDeadline != nil {
		zero := int64(0)
		c.ConfirmDeadline = &zero
	}
	// T-2003: every op now carries a server-assigned, stable-but-random id
	// (change.Op.ID, review.go's assignOpIDs) — as volatile run-to-run as the
	// changeset's own id above, and redacted the same way so the golden
	// fixture asserts on the documented op shape (op/target/params) without
	// flaking on a fresh ULID each run.
	if len(c.Ops) > 0 {
		redacted := make([]change.Op, len(c.Ops))
		copy(redacted, c.Ops)
		for i := range redacted {
			redacted[i].ID = "<op-id>"
		}
		c.Ops = redacted
	}
	// T-2101: touchesMgmtPath (internal/change/mgmttouch.go) is computed
	// from internal/topology.ResolveMgmtPaths, which needs a real LLDP-
	// identified uplink to resolve anything at all — this package's
	// in-process harness (harness_test.go's collect.Config) never wires an
	// LLDP source, so it is always empty there and the flag is always false
	// for every in-process golden fixture, regardless of the ops involved.
	// A real vnproxd (external conformance mode,
	// conformance_external_test.go) DOES collect LLDP and can genuinely
	// resolve vmbr0 onto a node's management path, correctly reporting
	// true where the in-process golden says false. That is real topology
	// data the minimal in-process harness structurally cannot produce, not
	// a handler regression — confirmed empirically by running this exact
	// suite against a real `go run ./cmd/pvemock` + `go run ./cmd/vnproxd
	// --config testdata/dev.toml` pair, the only place this field
	// diverged. Redacted here, in external mode only, so the golden
	// comparison keeps asserting on the documented shape (status, ops,
	// findings) without conflating "does this handler still work" with
	// "does this specific target's LLDP fixture happen to match
	// apicontract's own, deliberately LLDP-less one."
	if externalModeActive() {
		c.TouchesMgmtPath = false
	}
	return c
}
