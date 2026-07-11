package fwlog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTailBytes_InitialTailCapsToNewestLines(t *testing.T) {
	data := []byte("l1\nl2\nl3\nl4\nl5\n")
	lines, next, reset := tailBytes(data, "", 3)
	if reset {
		t.Fatal("reset = true on an initial (cursor-less) read")
	}
	want := []string{"l3", "l4", "l5"}
	if !equalStrings(lines, want) {
		t.Fatalf("lines = %v, want %v", lines, want)
	}
	if next != "15" {
		t.Fatalf("nextCursor = %q, want %q (end of data, len=15)", next, "15")
	}
}

func TestTailBytes_FollowIncrementReturnsOnlyNewLines(t *testing.T) {
	data := []byte("l1\nl2\n")
	_, cursor, _ := tailBytes(data, "", 10)

	grown := append([]byte(nil), data...)
	grown = append(grown, []byte("l3\nl4\n")...)

	lines, next, reset := tailBytes(grown, cursor, 10)
	if reset {
		t.Fatal("reset = true for a valid, non-stale cursor")
	}
	want := []string{"l3", "l4"}
	if !equalStrings(lines, want) {
		t.Fatalf("lines = %v, want %v", lines, want)
	}
	if next != "12" {
		t.Fatalf("nextCursor = %q, want %q", next, "12")
	}
}

func TestTailBytes_HoldsBackIncompleteTrailingLine(t *testing.T) {
	data := []byte("l1\nl2\npartial-no-newline-yet")
	lines, next, _ := tailBytes(data, "", 10)
	want := []string{"l1", "l2"}
	if !equalStrings(lines, want) {
		t.Fatalf("lines = %v, want %v (partial trailing line withheld)", lines, want)
	}
	// Cursor stops at the end of "l2\n" (6 bytes), not consuming the
	// trailing partial content, so a later call with the completed line
	// picks it up in full.
	if next != "6" {
		t.Fatalf("nextCursor = %q, want %q", next, "6")
	}
}

func TestTailBytes_StaleCursorResets(t *testing.T) {
	data := []byte("l1\nl2\n")
	lines, _, reset := tailBytes(data, "9999", 10)
	if !reset {
		t.Fatal("reset = false for an out-of-range (rotated/truncated) cursor")
	}
	want := []string{"l1", "l2"}
	if !equalStrings(lines, want) {
		t.Fatalf("lines = %v, want %v (restarted from 0)", lines, want)
	}
}

func TestTailBytes_MalformedCursorResets(t *testing.T) {
	_, _, reset := tailBytes([]byte("l1\n"), "not-a-number", 10)
	if !reset {
		t.Fatal("reset = false for a non-numeric cursor")
	}
}

func TestTailBytes_FollowIncrementCapsAndAdvancesPartially(t *testing.T) {
	data := []byte("l1\nl2\nl3\nl4\nl5\n")
	lines, next, _ := tailBytes(data, "0", 2)
	want := []string{"l1", "l2"}
	if !equalStrings(lines, want) {
		t.Fatalf("lines = %v, want %v", lines, want)
	}
	// Only "l1\nl2\n" (6 bytes) consumed; the remainder must be retried,
	// not skipped, on the next call.
	if next != "6" {
		t.Fatalf("nextCursor = %q, want %q", next, "6")
	}
	more, next2, _ := tailBytes(data, next, 10)
	wantMore := []string{"l3", "l4", "l5"}
	if !equalStrings(more, wantMore) {
		t.Fatalf("second call lines = %v, want %v", more, wantMore)
	}
	if next2 != "15" {
		t.Fatalf("nextCursor2 = %q, want %q", next2, "15")
	}
}

func TestFileSource_TailAndFollow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pve-firewall.log")
	if err := os.WriteFile(path, []byte("100 4 tap100i0-IN 10/Jul/2026:12:00:01 +0000 ACCEPT: SRC=1.1.1.1 DST=2.2.2.2\n"), 0o644); err != nil {
		t.Fatalf("seeding fixture file: %v", err)
	}
	src := &FileSource{Path: path}
	ctx := context.Background()

	lines, cursor, _, err := src.Tail(ctx, "pve1", "", 100)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("lines = %v, want 1 entry", lines)
	}

	// Append more content, simulating a live-growing log, and follow.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("opening for append: %v", err)
	}
	if _, writeErr := f.WriteString("101 4 tap101i0-IN 10/Jul/2026:12:00:02 +0000 DROP: SRC=3.3.3.3 DST=4.4.4.4\n"); writeErr != nil {
		t.Fatalf("appending: %v", writeErr)
	}
	_ = f.Close()

	more, _, _, err := src.Tail(ctx, "pve1", cursor, 100)
	if err != nil {
		t.Fatalf("Tail (follow): %v", err)
	}
	if len(more) != 1 || more[0] == "" {
		t.Fatalf("follow lines = %v, want exactly the appended line", more)
	}
}

func TestFileSource_MissingFileIsErrNotFound(t *testing.T) {
	src := &FileSource{Path: filepath.Join(t.TempDir(), "does-not-exist.log")}
	_, _, _, err := src.Tail(context.Background(), "pve1", "", 10)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestMemorySource_SeedAppendTail(t *testing.T) {
	m := NewMemorySource()
	m.Seed("pve1", "l1\nl2\n")
	ctx := context.Background()

	lines, cursor, _, err := m.Tail(ctx, "pve1", "", 10)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if !equalStrings(lines, []string{"l1", "l2"}) {
		t.Fatalf("lines = %v", lines)
	}

	m.Append("pve1", "l3", "l4")
	more, _, _, err := m.Tail(ctx, "pve1", cursor, 10)
	if err != nil {
		t.Fatalf("Tail (follow): %v", err)
	}
	if !equalStrings(more, []string{"l3", "l4"}) {
		t.Fatalf("follow lines = %v", more)
	}
}

func TestMemorySource_UnknownNodeIsErrNotFound(t *testing.T) {
	m := NewMemorySource()
	_, _, _, err := m.Tail(context.Background(), "no-such-node", "", 10)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
