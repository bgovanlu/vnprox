package fwlog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FuzzParseAll fuzzes ParseAll against arbitrary byte input (mirroring
// internal/host's FuzzParse — see that package's interfaces_fuzz_test.go).
// The invariant asserted is AC1's own contract: parsing arbitrary/garbage
// content must never panic, and the Total/Parsed/Skipped accounting must
// always be internally consistent (every line is counted exactly once, as
// either parsed or skipped).
func FuzzParseAll(f *testing.F) {
	entries, err := os.ReadDir("../../testdata/firewall-logs")
	if err == nil {
		for _, ent := range entries {
			if ent.IsDir() {
				continue
			}
			data, rerr := os.ReadFile(filepath.Join("../../testdata/firewall-logs", ent.Name()))
			if rerr == nil {
				f.Add(data)
			}
		}
	}
	f.Add([]byte(""))
	f.Add([]byte("\n"))
	f.Add([]byte("\x00\x01\xff\xfe"))
	f.Add([]byte("100 4 tap100i0-IN 10/Jul/2026:12:00:01 +0000 ACCEPT: SRC=1.1.1.1 DST=2.2.2.2\n"))
	f.Add([]byte("100 4 tap100i0-OUT 10/Jul/2026:12:00:01 +0000 policy DROP: IN=vmbr0\n"))
	f.Add([]byte("-1 -1 -1 -1 -1\n"))
	f.Add([]byte("99999999999999999999999999999 4 tap1i0-IN 10/Jul/2026:12:00:01 +0000 ACCEPT:\n"))
	f.Add([]byte("100 4 tap1i0-IN =====\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		res := ParseAll("fuzznode", strings.NewReader(string(data)))
		if res.Total != res.Parsed+res.Skipped {
			t.Fatalf("Total=%d != Parsed=%d + Skipped=%d for input %q", res.Total, res.Parsed, res.Skipped, data)
		}
		if len(res.Entries) != res.Parsed {
			t.Fatalf("len(Entries)=%d != Parsed=%d", len(res.Entries), res.Parsed)
		}
		// ParseLine itself must agree with ParseAll's per-line verdict —
		// exercised directly too, since ParseAll's line-splitting could in
		// principle diverge from ParseLine's own leniency.
		for _, e := range res.Entries {
			if _, ok := ParseLine("fuzznode", e.Raw); !ok {
				t.Fatalf("ParseAll produced an Entry whose Raw line ParseLine itself rejects: %q", e.Raw)
			}
		}
	})
}
