package host

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// FuzzParse fuzzes ParseInterfaces against arbitrary byte input (T-102
// AC4). The only invariant asserted is the one the lossless AST exists to
// guarantee: whenever ParseInterfaces accepts an input (err == nil), Render
// must reproduce that input byte-for-byte. ParseInterfaces is expected to
// reject plenty of fuzzer-generated inputs with a *ParseError — that is not
// a failure, only a panic or a round-trip mismatch is.
func FuzzParse(f *testing.F) {
	entries, err := os.ReadDir("../../testdata/interfaces")
	if err != nil {
		f.Fatalf("reading corpus dir: %v", err)
	}
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join("../../testdata/interfaces", ent.Name()))
		if err != nil {
			f.Fatalf("reading seed %s: %v", ent.Name(), err)
		}
		f.Add(data)
	}
	// A handful of small hand-picked seeds to steer the mutator toward
	// the parser's structural edge cases (continuations, stanza
	// boundaries, reserved-keyword prefixes) faster than the corpus
	// files alone would.
	f.Add([]byte(""))
	f.Add([]byte("\\"))
	f.Add([]byte("iface\n"))
	f.Add([]byte("iface a b c\n\tx \\\n"))
	f.Add([]byte("allow-\n"))
	f.Add([]byte("source\n"))
	f.Add([]byte("mapping\n\tscript x\n"))
	f.Add([]byte("auto a\niface a inet static\n\taddr 1\n\n#c\niface b inet manual\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		file, err := ParseInterfaces(data)
		if err != nil {
			// Rejecting malformed input is expected and fine, as long
			// as it came back as our typed error, not a panic.
			var perr *ParseError
			if !errors.As(err, &perr) {
				t.Fatalf("ParseInterfaces returned a non-ParseError error: %v", err)
			}
			return
		}
		if got := file.Render(); got != string(data) {
			t.Fatalf("round trip mismatch:\ninput:  %q\nrender: %q", data, got)
		}
	})
}
