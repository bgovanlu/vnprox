package host

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzParseBGPSummary fuzzes ParseBGPSummary against arbitrary byte input
// (T-404 AC4: "FRR JSON parse is fuzz-clean for 60s"). The only invariant
// asserted is that ParseBGPSummary never panics; returning an error, a
// zero BGPSummary, or a partial peer list are all acceptable outcomes for
// hostile input — same convention as FuzzParseLLDP.
func FuzzParseBGPSummary(f *testing.F) {
	for _, dir := range []string{
		filepath.Join("testdata", "frr", "bgp"),
		filepath.Join("testdata", "frr", "bgp", "adversarial"),
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
	f.Add([]byte(""))
	f.Add([]byte("{"))
	f.Add([]byte("["))
	f.Add([]byte("null"))
	f.Add([]byte(`{"peers":`))
	f.Add([]byte(`{"l2VpnEvpn":{"peers":{}}}`))
	f.Add([]byte(`{"l2VpnEvpn":{"peers":{"1.2.3.4":{}}}}`))
	f.Add([]byte(`{"peers":{"1.2.3.4":{"state":"Idle ()"}}}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = ParseBGPSummary(data)
	})
}

// FuzzParseEVPNVNI fuzzes ParseEVPNVNI against arbitrary byte input (T-404
// AC4). Same never-panic-only invariant as FuzzParseBGPSummary.
func FuzzParseEVPNVNI(f *testing.F) {
	for _, dir := range []string{
		filepath.Join("testdata", "frr", "evpn"),
		filepath.Join("testdata", "frr", "evpn", "adversarial"),
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
	f.Add([]byte(""))
	f.Add([]byte("{"))
	f.Add([]byte("["))
	f.Add([]byte("null"))
	f.Add([]byte(`{"10001":`))
	f.Add([]byte(`[{}]`))
	f.Add([]byte(`{"abc":{}}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = ParseEVPNVNI(data)
	})
}
