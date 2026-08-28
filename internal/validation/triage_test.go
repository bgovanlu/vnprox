// SPDX-License-Identifier: Apache-2.0

package validation

import (
	"os"
	"testing"
)

// TestTriage_FlagsDeliberateDivergence is T-1801 acceptance criterion 2: a
// deliberately wrong expected-outcome entry must cause triage to flag a
// divergence rather than silently pass. testdata/fixture-blob.json and
// testdata/fixture-expected.md are a fixture built for exactly this test
// (see fixture-expected.md's own header comment) — row "b" claims
// verdict_inputs.http_status is 200 when the fixture blob actually
// contains 403.
func TestTriage_FlagsDeliberateDivergence(t *testing.T) {
	raw, err := os.ReadFile("testdata/fixture-blob.json")
	if err != nil {
		t.Fatalf("reading fixture blob: %v", err)
	}
	blob, err := ParseBlob(raw)
	if err != nil {
		t.Fatalf("ParseBlob: %v", err)
	}

	md, err := os.ReadFile("testdata/fixture-expected.md")
	if err != nil {
		t.Fatalf("reading fixture expected-outcome table: %v", err)
	}
	expected, err := ParseExpected(md)
	if err != nil {
		t.Fatalf("ParseExpected: %v", err)
	}
	if len(expected) != 4 {
		t.Fatalf("ParseExpected: got %d rows, want 4", len(expected))
	}

	results := Triage(blob, expected)
	if len(results) != 4 {
		t.Fatalf("Triage: got %d results, want 4", len(results))
	}

	byRowIndex := results

	// Row 0: item a, http_status equals 200 — a genuine match.
	if got := byRowIndex[0]; got.Status != StatusMatch {
		t.Errorf("row 0 (item a, http_status): got status %q, want %q (actual=%q)", got.Status, StatusMatch, got.Actual)
	}

	// Row 1: item b, deliberately wrong expected 200 vs actual 403 — must
	// be flagged, never silently pass.
	got := byRowIndex[1]
	if got.Status != StatusDivergence {
		t.Fatalf("row 1 (item b, deliberately wrong expectation): got status %q, want %q — a bad expected-outcome entry must not silently pass", got.Status, StatusDivergence)
	}
	if got.Actual != "403" {
		t.Errorf("row 1: got actual %q, want %q", got.Actual, "403")
	}
	if got.Detail == "" {
		t.Errorf("row 1: divergence Detail is empty, want the row's Meaning text explaining what diverged")
	}

	// Row 2: item a, raw contains "ok" — a genuine match via a different op.
	if got := byRowIndex[2]; got.Status != StatusMatch {
		t.Errorf("row 2 (item a, raw contains): got status %q, want %q", got.Status, StatusMatch)
	}

	// Row 3: item c does not exist in the blob at all.
	if got := byRowIndex[3]; got.Status != StatusItemMissing {
		t.Errorf("row 3 (item c, does not exist): got status %q, want %q", got.Status, StatusItemMissing)
	}

	if !Diverged(results) {
		t.Error("Diverged(results): got false, want true (row 1 is a real divergence)")
	}
}

func TestTriage_AllMatchIsNotDiverged(t *testing.T) {
	blob := &Blob{
		SchemaVersion: SupportedSchemaVersion, HarnessVersion: "1.0.0", Section: "s",
		GeneratedAt: "2026-07-30T12:00:00Z",
		Node:        NodeInfo{Hostname: "h", Identity: "h"},
		PVEVersion:  PVEVersion{Source: "unknown", Raw: "unknown"},
		Items: []Item{
			{ID: "x", Command: "true", Raw: "ok", ExitCode: 0, VerdictInputs: map[string]any{"n": float64(1)}},
		},
	}
	expected := []ExpectedRow{
		{ID: "x", Pointer: "exit_code", Op: "equals", Expected: "0"},
		{ID: "x", Pointer: "verdict_inputs.n", Op: "equals", Expected: "1"},
	}
	results := Triage(blob, expected)
	if Diverged(results) {
		t.Fatalf("Diverged(results): got true, want false; results=%+v", results)
	}
}

func TestParseExpected_RejectsUnknownOp(t *testing.T) {
	md := []byte("| id | pointer | op | expected | meaning |\n|---|---|---|---|---|\n| x | raw | matches-vibes | y | z |\n")
	if _, err := ParseExpected(md); err == nil {
		t.Fatal("ParseExpected: got nil error for an unknown op, want an error")
	}
}

func TestFormatValue_WholeNumberFloat(t *testing.T) {
	if got := formatValue(float64(200)); got != "200" {
		t.Errorf("formatValue(200.0) = %q, want %q", got, "200")
	}
	if got := formatValue(float64(200.5)); got != "200.5" {
		t.Errorf("formatValue(200.5) = %q, want %q", got, "200.5")
	}
	if got := formatValue(true); got != "true" {
		t.Errorf("formatValue(true) = %q, want %q", got, "true")
	}
}
