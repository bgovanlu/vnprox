package fwlog

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseLine_Table(t *testing.T) {
	tests := []struct {
		check  func(t *testing.T, e Entry)
		name   string
		line   string
		wantOK bool
	}{
		{
			name:   "guest tap accept with full kv",
			line:   "100 4 tap100i0-IN 10/Jul/2026:12:00:01 +0000 ACCEPT: IN=vmbr0 OUT=fwbr100i0 SRC=10.0.0.5 DST=10.0.0.100 LEN=60 PROTO=TCP SPT=51820 DPT=22",
			wantOK: true,
			check: func(t *testing.T, e Entry) {
				if !e.Guest || e.VMID != 100 || e.NicIndex != 0 || e.Direction != "in" {
					t.Fatalf("guest/vmid/nic/direction = %v/%d/%d/%s, want true/100/0/in", e.Guest, e.VMID, e.NicIndex, e.Direction)
				}
				if e.Action != "ACCEPT" {
					t.Fatalf("Action = %q, want ACCEPT", e.Action)
				}
				if e.Proto != "TCP" || e.Source != "10.0.0.5" || e.Dest != "10.0.0.100" || e.Dport != "22" {
					t.Fatalf("kv fields = %+v", e)
				}
				if !e.HasTimestamp || e.Timestamp.UTC().Format(time.RFC3339) != "2026-07-10T12:00:01Z" {
					t.Fatalf("Timestamp = %v (HasTimestamp=%v), want 2026-07-10T12:00:01Z", e.Timestamp, e.HasTimestamp)
				}
				if e.PolicyFallthrough {
					t.Fatal("PolicyFallthrough = true, want false")
				}
			},
		},
		{
			name:   "veth container chain, RFC3339 timestamp variant",
			line:   "101 4 veth101i1-OUT 2026-07-10T12:00:07+00:00 ACCEPT: IN=fwbr101i0 OUT=vmbr0 SRC=10.0.0.6 DST=1.2.3.4 PROTO=TCP DPT=8080",
			wantOK: true,
			check: func(t *testing.T, e Entry) {
				if !e.Guest || e.VMID != 101 || e.NicIndex != 1 || e.Direction != "out" {
					t.Fatalf("guest/vmid/nic/direction = %v/%d/%d/%s", e.Guest, e.VMID, e.NicIndex, e.Direction)
				}
				if !e.HasTimestamp {
					t.Fatal("HasTimestamp = false for a valid RFC3339 timestamp variant")
				}
			},
		},
		{
			name:   "default policy fallthrough",
			line:   "100 4 tap100i0-OUT 10/Jul/2026:12:00:03 +0000 policy DROP: IN=fwbr100i0 OUT=vmbr0 SRC=10.0.0.100 DST=8.8.8.8 PROTO=UDP",
			wantOK: true,
			check: func(t *testing.T, e Entry) {
				if !e.PolicyFallthrough || e.Action != "DROP" {
					t.Fatalf("PolicyFallthrough/Action = %v/%q, want true/DROP", e.PolicyFallthrough, e.Action)
				}
			},
		},
		{
			name:   "non-guest chain: direction inferred from suffix, not correlatable as a guest",
			line:   "100 4 PVEFW-HOST-IN 10/Jul/2026:12:00:08 +0000 DROP: IN=vmbr0 SRC=198.51.100.7 DST=10.0.0.1 PROTO=TCP DPT=22",
			wantOK: true,
			check: func(t *testing.T, e Entry) {
				if e.Guest {
					t.Fatal("Guest = true for a non tap/veth chain")
				}
				if e.Direction != "in" {
					t.Fatalf("Direction = %q, want in (from -IN suffix fallback)", e.Direction)
				}
			},
		},
		{
			name:   "message with no action word (KV starts immediately) is still parsed",
			line:   "200 4 tap200i0-IN 10/Jul/2026:12:05:04 +0000 IN=vmbr0 OUT=fwbr200i0 SRC=10.0.1.11 DST=10.0.1.200 PROTO=TCP DPT=22",
			wantOK: true,
			check: func(t *testing.T, e Entry) {
				if e.Action != "" {
					t.Fatalf("Action = %q, want empty (no action token present)", e.Action)
				}
				if e.Source != "10.0.1.11" || e.Proto != "TCP" {
					t.Fatalf("kv fields not parsed despite missing action: %+v", e)
				}
			},
		},
		{
			name:   "bad timestamp: still parsed, HasTimestamp false",
			line:   "100 4 tap100i0-IN not-a-timestamp ACCEPT: SRC=1.1.1.1 DST=2.2.2.2",
			wantOK: true,
			check: func(t *testing.T, e Entry) {
				if e.HasTimestamp {
					t.Fatal("HasTimestamp = true for an unparsable timestamp token")
				}
				if e.Action != "NOT-A-TIMESTAMP" {
					// Documented lenient fallback: msgStart stays at the
					// first token when no timestamp parses, so that token
					// is treated as the start of the message.
					t.Fatalf("Action = %q, want the leftover token (uppercased) treated as message start", e.Action)
				}
			},
		},
		{name: "empty line", line: "", wantOK: false},
		{name: "whitespace only", line: "   \t  ", wantOK: false},
		{name: "too few fields", line: "100 4", wantOK: false},
		{name: "non-integer vmid", line: "abc 4 tap100i0-IN 10/Jul/2026:12:00:01 +0000 ACCEPT:", wantOK: false},
		{name: "negative vmid", line: "-5 4 tap100i0-IN 10/Jul/2026:12:00:01 +0000 ACCEPT:", wantOK: false},
		{name: "log level out of range (8)", line: "100 8 tap100i0-IN 10/Jul/2026:12:00:01 +0000 ACCEPT:", wantOK: false},
		{name: "log level not an integer", line: "100 abc tap100i0-IN 10/Jul/2026:12:00:01 +0000 ACCEPT:", wantOK: false},
		{name: "json blob noise", line: `{"not":"a log line","just":"json garbage"}`, wantOK: false},
		{name: "single token", line: "999", wantOK: false},
		{name: "prose noise", line: "this is not a firewall log line at all, just noise", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, ok := ParseLine("pve1", tt.line)
			if ok != tt.wantOK {
				t.Fatalf("ParseLine(%q) ok = %v, want %v (entry=%+v)", tt.line, ok, tt.wantOK, e)
			}
			if ok && tt.check != nil {
				tt.check(t, e)
			}
		})
	}
}

// TestParseAll_FixtureCorpus covers AC1: the fixture log corpus parses,
// garbage lines are skipped with a counter (never a crash), and the
// Total/Parsed/Skipped accounting is internally consistent.
func TestParseAll_FixtureCorpus(t *testing.T) {
	for _, tc := range []struct {
		file        string
		wantParsed  int
		wantSkipped int
	}{
		{file: "../../testdata/firewall-logs/pve1.log", wantParsed: 6, wantSkipped: 6},
		{file: "../../testdata/firewall-logs/pve2.log", wantParsed: 4, wantSkipped: 4},
	} {
		t.Run(tc.file, func(t *testing.T) {
			f, err := os.Open(tc.file)
			if err != nil {
				t.Fatalf("opening fixture: %v", err)
			}
			defer func() { _ = f.Close() }()

			res := ParseAll("pve1", f)
			if res.Total != res.Parsed+res.Skipped {
				t.Fatalf("Total=%d != Parsed=%d + Skipped=%d", res.Total, res.Parsed, res.Skipped)
			}
			if res.Parsed != tc.wantParsed {
				t.Errorf("Parsed = %d, want %d", res.Parsed, tc.wantParsed)
			}
			if res.Skipped != tc.wantSkipped {
				t.Errorf("Skipped = %d, want %d", res.Skipped, tc.wantSkipped)
			}
			if len(res.Entries) != res.Parsed {
				t.Errorf("len(Entries) = %d, want Parsed = %d", len(res.Entries), res.Parsed)
			}
		})
	}
}

// TestParseAll_NeverCrashesOnGarbageStream is a lighter-weight, non-fuzz
// belt-and-braces check (see fuzz_test.go for the property-based version)
// that a variety of adversarial byte streams never panic ParseAll.
func TestParseAll_NeverCrashesOnGarbageStream(t *testing.T) {
	inputs := []string{
		"",
		"\x00\x01\x02binary garbage\xff\xfe",
		"a very very very very very very very very very very long line with no structure at all " + string(make([]byte, 10000)),
		"100 4 tap100i0-IN\n200 4\n\n\n\n300\n",
		"100 4 tap100i0-IN 10/Jul/2026:12:00:01 +0000\n",
	}
	for _, in := range inputs {
		res := ParseAll("pve1", strings.NewReader(in))
		if res.Total != res.Parsed+res.Skipped {
			t.Fatalf("Total/Parsed/Skipped inconsistent for input %q: %+v", in, res)
		}
	}
}
