package apicontract

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
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
	return c
}
