package host

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestParseInterfaces_RoundTripCorpus verifies that every file in
// testdata/interfaces/ parses without error and re-renders byte-for-byte
// identical to its source — the core lossless guarantee the AST exists to
// provide (T-102 AC1). The corpus spans: a simple single-bridge config,
// VLAN-aware bridges, bonds with VLANs, plain and bonded OVS stanzas,
// source/source-directory includes (absolute and relative), mapping
// stanzas, allow-*/rename/no-auto-down/no-scripts lines, multi-stanza
// dual-stack ifaces, "inherits" templates, backslash line continuations,
// and files with exotic-but-valid comments, mixed tab/space indentation,
// trailing whitespace, CRLF line endings, and a missing trailing newline.
func TestParseInterfaces_RoundTripCorpus(t *testing.T) {
	entries, err := os.ReadDir("../../testdata/interfaces")
	if err != nil {
		t.Fatalf("reading corpus dir: %v", err)
	}
	if len(entries) < 12 {
		t.Fatalf("corpus has %d files, want >= 12", len(entries))
	}

	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		name := ent.Name()
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("../../testdata/interfaces", name)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading %s: %v", path, err)
			}
			f, err := ParseInterfaces(data)
			if err != nil {
				t.Fatalf("ParseInterfaces(%s): %v", name, err)
			}
			got := f.Render()
			if got != string(data) {
				t.Errorf("round trip mismatch for %s:\n--- want ---\n%q\n--- got ---\n%q", name, string(data), got)
			}
		})
	}
}

// TestParseInterfaces_Malformed table-tests line-precise rejection of
// malformed input (T-102 AC2): a stanza missing its iface keyword (options
// appearing before any iface/mapping header), an unterminated backslash
// continuation, and unrecognized garbage lines.
func TestParseInterfaces_Malformed(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantLine int
	}{
		{
			name:     "option before any stanza",
			input:    "address 192.168.1.1/24\n",
			wantLine: 1,
		},
		{
			name:     "option after top-level directive but before iface",
			input:    "auto eno1\n\tmtu 1500\n",
			wantLine: 2,
		},
		{
			name:     "iface missing family and method",
			input:    "auto eno1\niface eno1\n",
			wantLine: 2,
		},
		{
			name:     "iface missing method",
			input:    "iface eno1 inet\n",
			wantLine: 1,
		},
		{
			name:     "unterminated continuation at eof with newline",
			input:    "iface eno1 inet static\n\taddress 1.2.3.4 \\\n",
			wantLine: 2,
		},
		{
			name:     "unterminated continuation at eof no newline",
			input:    "iface eno1 inet static\n\taddress 1.2.3.4 \\",
			wantLine: 2,
		},
		{
			name:     "garbage token outside any stanza after a comment",
			input:    "# just a header\nthis is not a keyword\n",
			wantLine: 2,
		},
		{
			// A blank line does not close an open iface stanza (only a
			// new reserved keyword line does — see interfaces_ast.go),
			// so the garbage line here must follow a keyword line
			// (auto) that actually closes the "lo" stanza first.
			name:     "garbage after a closed stanza",
			input:    "iface lo inet loopback\n\nauto eno1\nbogus line here\n",
			wantLine: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseInterfaces([]byte(tt.input))
			if err == nil {
				t.Fatalf("ParseInterfaces(%q): expected error, got nil", tt.input)
			}
			var perr *ParseError
			if !errors.As(err, &perr) {
				t.Fatalf("ParseInterfaces(%q): error %v is not *ParseError", tt.input, err)
			}
			if perr.Line != tt.wantLine {
				t.Errorf("ParseInterfaces(%q): error line = %d, want %d (msg: %s)", tt.input, perr.Line, tt.wantLine, perr.Msg)
			}
		})
	}
}

// TestParseInterfaces_Empty verifies the degenerate empty-file case
// round-trips too (zero entries, empty render).
func TestParseInterfaces_Empty(t *testing.T) {
	f, err := ParseInterfaces(nil)
	if err != nil {
		t.Fatalf("ParseInterfaces(nil): %v", err)
	}
	if len(f.Entries) != 0 {
		t.Errorf("Entries = %v, want empty", f.Entries)
	}
	if got := f.Render(); got != "" {
		t.Errorf("Render() = %q, want empty", got)
	}
}

// TestFile_Accessors exercises the read-side convenience accessors
// (Ifaces, Iface, AutoIfaces, Sources, Options, Get) against a
// representative file so consumers (and T-103/T-204) have working
// examples of the intended usage.
func TestFile_Accessors(t *testing.T) {
	data, err := os.ReadFile("../../testdata/interfaces/03-bond-with-vlans.interfaces")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	f, err := ParseInterfaces(data)
	if err != nil {
		t.Fatalf("ParseInterfaces: %v", err)
	}

	bond, ok := f.Iface("bond0")
	if !ok {
		t.Fatalf("bond0 iface not found")
	}
	if v, ok := bond.Get("bond-mode"); !ok || v != "802.3ad" {
		t.Errorf("bond0 bond-mode = %q, %v, want 802.3ad, true", v, ok)
	}
	if v, ok := bond.Get("bond-slaves"); !ok || v != "eno1 eno2" {
		t.Errorf("bond0 bond-slaves = %q, %v, want %q, true", v, ok, "eno1 eno2")
	}

	auto := f.AutoIfaces()
	found := false
	for _, name := range auto {
		if name == "bond0" {
			found = true
		}
	}
	if !found {
		t.Errorf("AutoIfaces() = %v, want to contain bond0", auto)
	}

	ifaces := f.Ifaces()
	if len(ifaces) == 0 {
		t.Errorf("Ifaces() returned none")
	}
}
