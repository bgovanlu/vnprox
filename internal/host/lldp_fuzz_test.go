// SPDX-License-Identifier: Apache-2.0

package host

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzParseLLDP fuzzes ParseLLDP against arbitrary byte input (T-302 AC4:
// "malformed/hostile lldpctl JSON never panics"). The only invariant
// asserted is that ParseLLDP never panics; returning an error, nil, or a
// partial neighbor list are all acceptable outcomes for hostile input.
func FuzzParseLLDP(f *testing.F) {
	for _, dir := range []string{
		filepath.Join("testdata", "lldp"),
		filepath.Join("testdata", "lldp", "adversarial"),
	} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			f.Fatalf("reading corpus dir %s: %v", dir, err)
		}
		for _, ent := range entries {
			if ent.IsDir() {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, ent.Name()))
			if err != nil {
				f.Fatalf("reading seed %s: %v", ent.Name(), err)
			}
			f.Add(data)
		}
	}
	// Hand-picked seeds to steer the mutator toward structural edge cases
	// the corpus files alone might not reach quickly.
	f.Add([]byte(""))
	f.Add([]byte("{"))
	f.Add([]byte("["))
	f.Add([]byte("null"))
	f.Add([]byte(`{"lldp":`))
	f.Add([]byte(`{"lldp":[{"interface":[{}]}]}`))
	f.Add([]byte(`[{}]`))
	f.Add([]byte(`{"lldp":[{"interface":[{"chassis":{}}]}]}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		// The only assertion: no panic. ParseLLDP's own recover() should
		// already guarantee this, but the fuzz harness itself would still
		// fail the run on an unrecovered panic, so this is the real check.
		_, _ = ParseLLDP(data)
	})
}
