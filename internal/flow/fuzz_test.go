package flow

import (
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// TestDefensiveParsing_RandomTruncation is AC2's fuzz-style defensive-
// parsing test: every fixture, truncated at ≥1000 random lengths (and, for
// good measure, with random byte flips), must never panic and never block
// (each decode call completes synchronously) — matching the CI FuzzParse
// convention's spirit (docs/development.md) for internal/host's
// interfaces(5) parser, applied here to this package's four wire decoders.
func TestDefensiveParsing_RandomTruncation(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "flows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading testdata/flows: %v", err)
	}

	type decoder struct {
		run  func(data []byte)
		name string
	}
	cache9 := NewTemplateCache(nil)
	cacheIPFIX := NewTemplateCache(nil)
	decoders := []decoder{
		{name: "netflow5", run: func(data []byte) { _, _ = DecodeNetFlow5(data, "pve1") }},
		{name: "netflow9", run: func(data []byte) { _, _, _ = DecodeNetFlow9(data, "pve1", "fuzz-exporter", cache9) }},
		{name: "ipfix", run: func(data []byte) { _, _, _ = DecodeIPFIX(data, "pve1", "fuzz-exporter", cacheIPFIX) }},
		{name: "sflow", run: func(data []byte) { _, _, _ = DecodeSFlow(data, "pve1", 1700000000) }},
	}

	rng := rand.New(rand.NewSource(1))
	const iterations = 1200

	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		orig, err := os.ReadFile(filepath.Join(dir, ent.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", ent.Name(), err)
		}
		for _, d := range decoders {
			t.Run(d.name+"/"+ent.Name(), func(t *testing.T) {
				for i := 0; i < iterations; i++ {
					mutated := mutateFixture(rng, orig)
					func() {
						defer func() {
							if r := recover(); r != nil {
								t.Fatalf("decoder %s panicked on mutated %s (iteration %d, len %d): %v", d.name, ent.Name(), i, len(mutated), r)
							}
						}()
						d.run(mutated)
					}()
				}
			})
		}
	}
}

// mutateFixture returns a corrupted copy of orig: either truncated to a
// random length, or truncated and then given a handful of random byte
// flips within the remaining bytes — the two corruption shapes a real
// on-the-wire UDP datagram loss/corruption can produce.
func mutateFixture(rng *rand.Rand, orig []byte) []byte {
	if len(orig) == 0 {
		return nil
	}
	n := rng.Intn(len(orig) + 1) // 0..len(orig) inclusive
	out := make([]byte, n)
	copy(out, orig[:n])

	if len(out) > 0 && rng.Intn(2) == 0 {
		flips := rng.Intn(4) + 1
		for i := 0; i < flips; i++ {
			idx := rng.Intn(len(out))
			out[idx] = byte(rng.Intn(256))
		}
	}
	return out
}
