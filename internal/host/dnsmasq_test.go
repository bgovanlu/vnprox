package host

import (
	"strings"
	"testing"
)

// TestParseDHCPLeases_ValidCorpus checks correct field extraction on
// well-formed lines, including the "*"-means-unset convention for
// hostname/client-id.
func TestParseDHCPLeases_ValidCorpus(t *testing.T) {
	raw := strings.Join([]string{
		"1735689600 aa:bb:cc:dd:ee:01 10.50.0.10 web1 01:aa:bb:cc:dd:ee:01",
		"1735689700 AA:BB:CC:DD:EE:02 10.50.0.11 * *",
		"1735689800 aa:bb:cc:dd:ee:03 10.50.0.12 web3 *",
	}, "\n")

	leases, skipped := ParseDHCPLeases([]byte(raw))
	if skipped != 0 {
		t.Fatalf("skipped = %d, want 0 for an all-valid corpus", skipped)
	}
	if len(leases) != 3 {
		t.Fatalf("leases = %d, want 3: %+v", len(leases), leases)
	}

	if leases[0].MAC != "aa:bb:cc:dd:ee:01" || leases[0].IP != "10.50.0.10" || leases[0].Hostname != "web1" || leases[0].ClientID != "01:aa:bb:cc:dd:ee:01" || leases[0].ExpiresAt != 1735689600 {
		t.Errorf("lease[0] = %+v", leases[0])
	}
	// Mixed-case MAC is normalized to lowercase.
	if leases[1].MAC != "aa:bb:cc:dd:ee:02" {
		t.Errorf("lease[1] mac = %q, want lowercased", leases[1].MAC)
	}
	if leases[1].Hostname != "" || leases[1].ClientID != "" {
		t.Errorf("lease[1] = %+v, want hostname/clientid empty for \"*\" fields", leases[1])
	}
	if leases[2].Hostname != "web3" || leases[2].ClientID != "" {
		t.Errorf("lease[2] = %+v, want hostname=web3 clientid empty", leases[2])
	}
}

// TestParseDHCPLeases_MalformedCorpus is T-406 acceptance criterion 3: a
// corpus including malformed lines never crashes the parser and every bad
// line is skipped (counted), not misparsed into garbage data.
func TestParseDHCPLeases_MalformedCorpus(t *testing.T) {
	raw := strings.Join([]string{
		"1735689600 aa:bb:cc:dd:ee:01 10.50.0.10 web1 *", // valid, kept
		"",                        // blank line, silently skipped, not counted
		"   ",                     // whitespace-only line
		"not-a-lease-line-at-all", // too few fields
		"nope-an-expiry aa:bb:cc:dd:ee:02 10.50.0.11 host *", // unparsable expiry
		"1735689600 not-a-mac 10.50.0.12 host *",             // unparsable mac
		"1735689600 aa:bb:cc:dd:ee:03 not-an-ip host *",      // unparsable ip
		"-5 aa:bb:cc:dd:ee:04 10.50.0.13 host *",             // negative expiry
		"1735689600 aa:bb:cc:dd:ee:05",                       // too few fields (only 2)
		"1735689900 aa:bb:cc:dd:ee:06 10.50.0.14 web6 *",     // valid, kept
	}, "\n")

	leases, skipped := ParseDHCPLeases([]byte(raw))
	if len(leases) != 2 {
		t.Fatalf("leases = %d, want 2 valid lines survived: %+v", len(leases), leases)
	}
	// 6 genuinely malformed non-blank lines above.
	if skipped != 6 {
		t.Fatalf("skipped = %d, want 6", skipped)
	}
	if leases[0].IP != "10.50.0.10" || leases[1].IP != "10.50.0.14" {
		t.Errorf("leases = %+v, want the two valid entries in order", leases)
	}
}

// TestParseDHCPLeases_NeverPanics fuzzes a handful of adversarial inputs
// directly (T-406 AC3's "never crashes" — see FuzzParseDHCPLeases below
// for the sustained fuzz run) to make sure the defensive recover() and
// bounded-buffer scanner actually protect against the obvious edge cases
// without needing a long fuzz run to catch them.
func TestParseDHCPLeases_NeverPanics(t *testing.T) {
	inputs := [][]byte{
		nil,
		{},
		[]byte("\x00\x01\x02\xff\xfe"),
		[]byte(strings.Repeat("a", 1<<21)), // one huge "line", no newline
		[]byte("1735689600\taa:bb:cc:dd:ee:01\t10.50.0.10\tweb1\t*\r\n"),
		[]byte("not json"),
		[]byte("１７３５　ａａ：ｂｂ　10.50.0.10"), // full-width unicode garbage
	}
	for i, in := range inputs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("input %d panicked: %v", i, r)
				}
			}()
			ParseDHCPLeases(in)
		}()
	}
}

// FuzzParseDHCPLeases is T-406 acceptance criterion 3's "never crashes"
// guarantee under sustained adversarial input, mirroring internal/host's
// existing FuzzParseBGPSummary/FuzzParseLLDP fuzz-freedom precedent (run
// manually: `go test -run='^$' -fuzz=FuzzParseDHCPLeases -fuzztime=60s
// ./internal/host/`).
func FuzzParseDHCPLeases(f *testing.F) {
	f.Add([]byte("1735689600 aa:bb:cc:dd:ee:01 10.50.0.10 web1 *"))
	f.Add([]byte(""))
	f.Add([]byte("garbage\nmore garbage\n"))
	f.Add([]byte("-1 zz:zz 999.999.999.999 * *"))
	f.Fuzz(func(t *testing.T, data []byte) {
		ParseDHCPLeases(data)
	})
}
