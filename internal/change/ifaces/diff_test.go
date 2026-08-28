// SPDX-License-Identifier: Apache-2.0

package ifaces

import "testing"

func TestUnifiedDiff_Identical(t *testing.T) {
	if d := UnifiedDiff("a", "a", "same\n", "same\n"); d != "" {
		t.Errorf("identical content: got non-empty diff %q", d)
	}
}

func TestUnifiedDiff_SimpleChange(t *testing.T) {
	old := "line1\nline2\nline3\n"
	new := "line1\nCHANGED\nline3\n"
	d := UnifiedDiff("old.txt", "new.txt", old, new)
	want := "--- old.txt\n+++ new.txt\n@@ -1,3 +1,3 @@\n line1\n-line2\n+CHANGED\n line3\n"
	if d != want {
		t.Errorf("diff mismatch:\ngot:\n%s\nwant:\n%s", d, want)
	}
}

func TestUnifiedDiff_Append(t *testing.T) {
	old := "a\nb\n"
	new := "a\nb\nc\n"
	d := UnifiedDiff("f", "f", old, new)
	want := "--- f\n+++ f\n@@ -1,2 +1,3 @@\n a\n b\n+c\n"
	if d != want {
		t.Errorf("diff mismatch:\ngot:\n%s\nwant:\n%s", d, want)
	}
}

func TestUnifiedDiff_MultipleHunks(t *testing.T) {
	// Two changes far enough apart (more than 2*diffContext lines of
	// unchanged content between them) must render as two separate hunks.
	var old, new string
	for i := 0; i < 20; i++ {
		old += "ctx\n"
		new += "ctx\n"
	}
	oldLines := splitLines(old)
	newLines := splitLines(new)
	oldLines[0] = "FIRST-OLD\n"
	newLines[0] = "FIRST-NEW\n"
	oldLines[19] = "LAST-OLD\n"
	newLines[19] = "LAST-NEW\n"
	old = joinAll(oldLines)
	new = joinAll(newLines)

	d := UnifiedDiff("f", "f", old, new)
	hunkCount := countOccurrences(d, "@@")
	if hunkCount != 4 { // two "@@ ... @@" markers per hunk
		t.Errorf("expected 2 hunks (4 '@@' markers), got %d markers in:\n%s", hunkCount, d)
	}
}

func joinAll(lines []string) string {
	out := ""
	for _, l := range lines {
		out += l
	}
	return out
}

func countOccurrences(s, sub string) int {
	n := 0
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			n++
		}
	}
	return n
}

func TestUnifiedDiff_NoTrailingNewline(t *testing.T) {
	old := "a\nb"
	new := "a\nc"
	d := UnifiedDiff("f", "f", old, new)
	if d == "" {
		t.Fatal("expected a non-empty diff")
	}
	// Each side's final, terminator-less line must be followed by the
	// unified-diff no-newline marker (GNU diff behavior; without it GNU
	// patch rejects the hunk — audit finding F-06).
	want := "--- f\n+++ f\n@@ -1,2 +1,2 @@\n a\n-b\n\\ No newline at end of file\n+c\n\\ No newline at end of file\n"
	if d != want {
		t.Errorf("diff mismatch:\ngot:\n%q\nwant:\n%q", d, want)
	}
}

// TestUnifiedDiff_NewlineAddedAtEOF: old lacks the trailing newline, new
// has it — the marker must appear only on the old side.
func TestUnifiedDiff_NewlineAddedAtEOF(t *testing.T) {
	d := UnifiedDiff("f", "f", "a\nb", "a\nb\n")
	want := "--- f\n+++ f\n@@ -1,2 +1,2 @@\n a\n-b\n\\ No newline at end of file\n+b\n"
	if d != want {
		t.Errorf("diff mismatch:\ngot:\n%q\nwant:\n%q", d, want)
	}
}

// TestUnifiedDiff_NoNewlineOnContextLine: both sides end with the same
// terminator-less line rendered as context — one marker, after the
// context line, per GNU diff.
func TestUnifiedDiff_NoNewlineOnContextLine(t *testing.T) {
	d := UnifiedDiff("f", "f", "a\nz", "b\nz")
	want := "--- f\n+++ f\n@@ -1,2 +1,2 @@\n-a\n+b\n z\n\\ No newline at end of file\n"
	if d != want {
		t.Errorf("diff mismatch:\ngot:\n%q\nwant:\n%q", d, want)
	}
}
