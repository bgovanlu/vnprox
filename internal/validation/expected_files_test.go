// SPDX-License-Identifier: Apache-2.0

package validation

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRealExpectedFilesParse is a sanity check (not one of T-1801's five
// numbered acceptance criteria, but cheap and directly relevant to what
// T-1802 depends on): every real planning/validation/expected/<section>.md
// this card ships must actually parse as a well-formed expected-outcome
// table, and every section script must have a companion expected file.
func TestRealExpectedFilesParse(t *testing.T) {
	harnessDir := filepath.Join("..", "..", "planning", "validation", "harness")
	expectedDir := filepath.Join("..", "..", "planning", "validation", "expected")

	entries, err := os.ReadDir(harnessDir)
	if err != nil {
		t.Fatalf("reading %s: %v", harnessDir, err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".sh" {
			continue
		}
		section := e.Name()[:len(e.Name())-len(".sh")]
		t.Run(section, func(t *testing.T) {
			path := filepath.Join(expectedDir, section+".md")
			md, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("missing companion expected-outcome file %s: %v", path, err)
			}
			rows, err := ParseExpected(md)
			if err != nil {
				t.Fatalf("ParseExpected(%s): %v", path, err)
			}
			if len(rows) == 0 {
				t.Errorf("%s: parsed zero expected-outcome rows", path)
			}
		})
	}
}
