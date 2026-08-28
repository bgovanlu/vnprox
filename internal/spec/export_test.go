// SPDX-License-Identifier: Apache-2.0

package spec_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/spec"
)

var fixtures = []struct {
	name string
	path string
}{
	{"single-node", fixtureSingleNode},
	{"three-node-vlan", fixtureThreeNode},
	{"evpn-lab", fixtureEVPNLab},
}

// AC1: Export against each fixture produces valid specVersion:1 YAML that
// parses back cleanly and matches a committed golden schema file.
func TestExport_GoldenSchema(t *testing.T) {
	for _, fx := range fixtures {
		t.Run(fx.name, func(t *testing.T) {
			g := buildFixtureGraph(t, fx.path)
			s := spec.Export(g.Snapshot())
			if s.SpecVersion != spec.Version {
				t.Fatalf("SpecVersion = %d, want %d", s.SpecVersion, spec.Version)
			}
			b, err := spec.Marshal(s)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			// Round-trips through Parse without error (valid schema).
			parsed, err := spec.Parse(b)
			if err != nil {
				t.Fatalf("Parse(Export): %v", err)
			}
			if parsed.SpecVersion != spec.Version {
				t.Fatalf("parsed SpecVersion = %d, want %d", parsed.SpecVersion, spec.Version)
			}
			assertGolden(t, fx.name, b)
		})
	}
}

// AC4: two exports of an unchanged cluster are byte-identical, even with Go
// map-iteration randomization in play (Export walks Snapshot.All(), groups
// entities into maps keyed by node, and must sort deterministically before
// marshaling).
func TestExport_ByteIdentical(t *testing.T) {
	for _, fx := range fixtures {
		t.Run(fx.name, func(t *testing.T) {
			g := buildFixtureGraph(t, fx.path)
			snap := g.Snapshot()
			var first []byte
			for i := 0; i < 25; i++ {
				b, err := spec.Marshal(spec.Export(snap))
				if err != nil {
					t.Fatalf("Marshal: %v", err)
				}
				if i == 0 {
					first = b
					continue
				}
				if string(b) != string(first) {
					t.Fatalf("export #%d differs from export #0:\n--- #0 ---\n%s\n--- #%d ---\n%s", i, first, i, b)
				}
			}
		})
	}
}
