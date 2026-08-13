package compat

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// update mirrors internal/apicontract's own updateGolden flag (golden_test.go,
// manifest_test.go) — same name, same convention, separate flag instance
// because this is a different package:
//
//	go test ./internal/apicontract/compat/... -run TestMatrix_MatchesPublishedArtifact -update
//
// (also `make compat-matrix`.)
var update = flag.Bool("update", false, "update the published compatibility matrix artifact")

// publishedMatrixPath is the versioned, committed artifact a reader (or a
// downstream tool) can read without running anything —
// docs/compat-matrix.json's own "published matrix an operator can read at a
// glance" half lives in docs/compatibility.md; this is its machine-readable
// twin (AC1).
var publishedMatrixPath = filepath.Join("..", "..", "..", "docs", "compat-matrix.json")

// compatibilityDocPath is the human-readable doc UpdateGeneratedSection
// rewrites the generated table into (AC4: regenerated, not hand-maintained).
var compatibilityDocPath = filepath.Join("..", "..", "..", "docs", "compatibility.md")

// versionPlaceholder and timePlaceholder replace the two fields that would
// otherwise churn the committed artifact on every single run (the real
// release version is stamped in at release time — release.yml — exactly
// like docs/openapi.json's own openAPIVersionPlaceholder convention,
// cmd/vnproxd/openapi_test.go), and the generation timestamp, which is
// never meaningful to diff.
const versionPlaceholder = "unversioned"

var timePlaceholder = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)

func placeholderMatrix(m Matrix) Matrix {
	m.VnproxVersion = versionPlaceholder
	m.GeneratedAt = timePlaceholder
	return m
}

// TestMatrix_MatchesPublishedArtifact is this package's golden-fixture test
// for docs/compat-matrix.json, run every time as part of `go test ./...`
// (T-2103 AC1: "runs in CI"). It genuinely regenerates the matrix from
// scratch — Generate spins up a real compat-wrapped mock server per cell
// and drives real HTTP checks against it — rather than reading a fixture,
// so a break anywhere in the chain (pvemock's compat wrapper, this
// package's checks, or Generate's own cell iteration) fails this test, not
// just a hand-written one.
//
// It also writes the exact same Matrix, WITHOUT the placeholder
// substitution, to var/compat-matrix.json unconditionally — the literal
// "produces a machine-readable result per cell" artifact a CI run leaves
// behind, timestamped and versioned for real (VNPROX_COMPAT_VERSION, "dev"
// if unset), independent of whether -update was passed.
func TestMatrix_MatchesPublishedArtifact(t *testing.T) {
	m, err := Generate(versionPlaceholder)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(m.Cells) != len(Cells) {
		t.Fatalf("Generate produced %d cells, want %d", len(m.Cells), len(Cells))
	}

	writeCIArtifact(t, m)

	gotJSON, err := placeholderMatrix(m).JSON()
	if err != nil {
		t.Fatalf("Matrix.JSON: %v", err)
	}

	if *update {
		if writeErr := os.WriteFile(publishedMatrixPath, gotJSON, 0o644); writeErr != nil {
			t.Fatalf("writing %s: %v", publishedMatrixPath, writeErr)
		}
		if tableErr := UpdateGeneratedSection(compatibilityDocPath, m.MarkdownTable()); tableErr != nil {
			t.Fatalf("updating %s: %v", compatibilityDocPath, tableErr)
		}
		return
	}

	want, err := os.ReadFile(publishedMatrixPath)
	if err != nil {
		t.Fatalf("reading %s (run `make compat-matrix` to create it): %v", publishedMatrixPath, err)
	}
	if !bytes.Equal(want, gotJSON) {
		t.Errorf("%s is stale relative to a fresh Generate() run. Run `make compat-matrix` after confirming the change is intended.\n--- got ---\n%s\n--- want ---\n%s",
			publishedMatrixPath, gotJSON, want)
	}
}

func writeCIArtifact(t *testing.T, m Matrix) {
	t.Helper()
	varDir := filepath.Join("..", "..", "..", "var")
	if err := os.MkdirAll(varDir, 0o755); err != nil {
		t.Fatalf("creating %s: %v", varDir, err)
	}
	live := m
	if v := os.Getenv("VNPROX_COMPAT_VERSION"); v != "" {
		live.VnproxVersion = v
	} else {
		live.VnproxVersion = "dev"
	}
	liveJSON, err := live.JSON()
	if err != nil {
		t.Fatalf("Matrix.JSON (live artifact): %v", err)
	}
	if err := os.WriteFile(filepath.Join(varDir, "compat-matrix.json"), liveJSON, 0o644); err != nil {
		t.Fatalf("writing var/compat-matrix.json: %v", err)
	}
}

// TestSDNFabricZoneGate_IsCaughtPerVersion is this package's own copy of
// the T-2103 AC2 demonstration (internal/pvemock/compat_test.go's
// TestCompatServer_SDNFabricZoneGate proves the same thing one layer down,
// directly against NewCompatServer): every cell's sdn_fabric_zone_gate
// check must PASS, and "pass" means the mock's accept/reject decision
// matched what that PVE version is documented to support — which for the
// 8.2 cell means the zone create was correctly *rejected*. If
// pvemock.PVEVersionProfile.ValidateSDNZoneType's gate is ever weakened
// (see this repo's T-2103 report for the mutation run that proves it),
// this specific subtest is the one that reddens, not the whole suite.
func TestSDNFabricZoneGate_IsCaughtPerVersion(t *testing.T) {
	m, err := Generate("test")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	wantAccepted := map[string]bool{"8.2": false, "9.0": true, "9.2": true}
	for _, cell := range m.Cells {
		cell := cell
		t.Run("PVE "+cell.PVEVersion, func(t *testing.T) {
			want, ok := wantAccepted[cell.PVEVersion]
			if !ok {
				t.Fatalf("no expectation registered for PVE version %q — update this test's table", cell.PVEVersion)
			}
			var gate *CheckResult
			for i := range cell.Checks {
				if cell.Checks[i].Name == "sdn_fabric_zone_gate" {
					gate = &cell.Checks[i]
				}
			}
			if gate == nil {
				t.Fatalf("cell PVE %s carries no sdn_fabric_zone_gate check", cell.PVEVersion)
			}
			if !gate.Pass {
				t.Errorf("sdn_fabric_zone_gate on PVE %s did not behave as documented (want accepted=%v): %s",
					cell.PVEVersion, want, gate.Detail)
			}
			if cell.Validation != ValidationKindMock {
				t.Errorf("cell PVE %s Validation = %q, want %q — T-2103 AC3: this matrix never claims hardware validation", cell.PVEVersion, cell.Validation, ValidationKindMock)
			}
		})
	}
}

// TestMarkdownTable_ReflectsCellStatus is a pure-function test against a
// synthetic Matrix (no HTTP, no pvemock) so the rendering logic itself is
// exercised independently of Generate's network of moving parts.
func TestMarkdownTable_ReflectsCellStatus(t *testing.T) {
	m := Matrix{
		VnproxVersion: "v9.9.9",
		GeneratedAt:   timePlaceholder,
		Cells: []CellResult{
			{PVEVersion: "8.2", Fixture: "testdata/clusters/compat/pve-8.2.yaml", Validation: ValidationKindMock, Pass: true,
				Checks: []CheckResult{{Name: "auth_ticket", Pass: true}, {Name: "sdn_fabric_zone_gate", Pass: true}}},
			{PVEVersion: "9.0", Fixture: "testdata/clusters/compat/pve-9.0.yaml", Validation: ValidationKindMock, Pass: false,
				Checks: []CheckResult{{Name: "auth_ticket", Pass: true}, {Name: "sdn_fabric_zone_gate", Pass: false, Detail: "boom"}}},
		},
	}
	table := m.MarkdownTable()
	if !bytes.Contains([]byte(table), []byte("v9.9.9")) {
		t.Errorf("table does not mention vnprox version: %s", table)
	}
	if !bytes.Contains([]byte(table), []byte("| 8.2 | mock | pass |")) {
		t.Errorf("table's 8.2 row does not read as a clean pass:\n%s", table)
	}
	if !bytes.Contains([]byte(table), []byte("**FAIL**")) {
		t.Errorf("table does not mark the 9.0 row as failing:\n%s", table)
	}
	if !bytes.Contains([]byte(table), []byte("sdn_fabric_zone_gate:FAIL")) {
		t.Errorf("table does not name the failing check:\n%s", table)
	}
	for _, cell := range m.Cells {
		if !bytes.Contains([]byte(table), []byte(cell.Validation)) {
			t.Errorf("table omits the mock/hardware validation label for PVE %s (T-2103 AC3)", cell.PVEVersion)
		}
	}
}

// TestUpdateGeneratedSection exercises the marker-replacement logic in
// isolation against a temp file, including both marker-missing error paths.
func TestUpdateGeneratedSection(t *testing.T) {
	t.Run("replaces content between markers, preserves the rest", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "doc.md")
		original := "# Title\n\nSome prose.\n\n" + generatedBeginMarker + "\nstale table\n" + generatedEndMarker + "\n\nMore prose.\n"
		if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
			t.Fatalf("writing temp doc: %v", err)
		}
		if err := UpdateGeneratedSection(path, "fresh table"); err != nil {
			t.Fatalf("UpdateGeneratedSection: %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading back temp doc: %v", err)
		}
		gotStr := string(got)
		if bytes.Contains(got, []byte("stale table")) {
			t.Errorf("stale table content survived:\n%s", gotStr)
		}
		if !bytes.Contains(got, []byte("fresh table")) {
			t.Errorf("fresh table content missing:\n%s", gotStr)
		}
		if !bytes.Contains(got, []byte("# Title")) || !bytes.Contains(got, []byte("More prose.")) {
			t.Errorf("content outside the markers was not preserved:\n%s", gotStr)
		}
	})

	t.Run("missing begin marker is an error", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "doc.md")
		_ = os.WriteFile(path, []byte("# Title\n\nno markers here\n"), 0o644)
		if err := UpdateGeneratedSection(path, "table"); err == nil {
			t.Fatal("UpdateGeneratedSection with no begin marker: got nil error, want one")
		}
	})

	t.Run("missing end marker is an error", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "doc.md")
		_ = os.WriteFile(path, []byte("# Title\n\n"+generatedBeginMarker+"\nno end marker\n"), 0o644)
		if err := UpdateGeneratedSection(path, "table"); err == nil {
			t.Fatal("UpdateGeneratedSection with no end marker: got nil error, want one")
		}
	})
}

// TestGenerate_AllChecksNamedConsistently guards the "same check-name shape
// across every cell" property writeCIArtifact/the markdown renderer and a
// reader comparing rows all depend on implicitly.
func TestGenerate_AllChecksNamedConsistently(t *testing.T) {
	m, err := Generate("test")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var wantNames []string
	for i, cell := range m.Cells {
		var names []string
		for _, c := range cell.Checks {
			names = append(names, c.Name)
		}
		if i == 0 {
			wantNames = names
			continue
		}
		if len(names) != len(wantNames) {
			t.Fatalf("cell PVE %s has %d checks, first cell has %d: %v vs %v", cell.PVEVersion, len(names), len(wantNames), names, wantNames)
		}
		for j := range names {
			if names[j] != wantNames[j] {
				t.Errorf("cell PVE %s check[%d] = %q, want %q (same order as the first cell)", cell.PVEVersion, j, names[j], wantNames[j])
			}
		}
	}
}

// jsonRoundTrip is a small sanity check that Matrix survives a JSON
// round-trip byte-identically in shape (used only to catch an accidental
// unexported/unmarshalable field during development).
func TestMatrix_JSONRoundTrip(t *testing.T) {
	m := Matrix{VnproxVersion: "v1", GeneratedAt: timePlaceholder, Cells: []CellResult{
		{PVEVersion: "8.2", Fixture: "f", Validation: ValidationKindMock, Pass: true, Checks: []CheckResult{{Name: "x", Pass: true, Detail: "d"}}},
	}}
	raw, err := m.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var back Matrix
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back.VnproxVersion != m.VnproxVersion || len(back.Cells) != len(m.Cells) {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", back, m)
	}
}
