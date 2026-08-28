package telemetry

// docs_telemetry_page_test.go extends T-2503 AC6's drift guard (docs.go) to
// docs/telemetry.md (T-3812): the public transparency page docs.vnprox.com
// serves for a prospective adopter, as distinct from docs/security.md's
// audit-oriented section. Two documents now promise the same closed field
// list, and both are compared against the SAME struct with the SAME
// ParseDocTable/CompareDoc functions docs.go already uses — reusing the
// idiom rather than inventing a second one, so there remains exactly one
// thing to keep in sync (Payload), not two hand-written lists that could
// each drift independently of the code and of each other.

import (
	"os"
	"path/filepath"
	"testing"
)

// PublicDocRelPath is the transparency page's path, repo-relative.
const PublicDocRelPath = "docs/telemetry.md"

// TestPublicTelemetryPageMatchesPayload is docs/telemetry.md's half of the
// drift guard: adding a field to Payload without documenting it on the
// public page fails the build, exactly as TestDocSectionMatchesPayload
// (docs_test.go) already does for docs/security.md. Removing a field from
// Payload while the page still promises it fails too, in the same
// CompareDoc call.
func TestPublicTelemetryPageMatchesPayload(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("%v", err)
	}
	path := filepath.Join(root, PublicDocRelPath)
	raw, err := os.ReadFile(path) //nolint:gosec // repo-relative path this repo owns
	if err != nil {
		t.Fatalf("reading %s: %v", PublicDocRelPath, err)
	}

	rows, err := ParseDocTable(string(raw))
	if err != nil {
		t.Fatalf("parsing %s: %v", PublicDocRelPath, err)
	}

	fields := PayloadFields()
	if len(fields) == 0 {
		t.Fatal("reflection found no payload fields, so this comparison would pass against an empty document")
	}

	if err := CompareDoc(fields, rows, PublicDocRelPath); err != nil {
		t.Fatalf("%s and the telemetry payload struct disagree: %v", PublicDocRelPath, err)
	}
	t.Logf("%d fields, stated identically in %s and internal/telemetry.Payload", len(rows), PublicDocRelPath)
}
