// SPDX-License-Identifier: Apache-2.0

package host

import (
	"os"
	"testing"
)

func readConntrackTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("reading testdata/%s: %v", name, err)
	}
	return data
}

// TestParseConntrackTable_Golden is T-1305 acceptance criterion 1: a golden
// /proc/net/nf_conntrack-format sample covering TCP (two distinct states),
// UDP, ICMP, IPv6, and both SNAT and DNAT translations.
func TestParseConntrackTable_Golden(t *testing.T) {
	entries, skipped := ParseConntrackTable(readConntrackTestdata(t, "conntrack_golden.txt"))
	if skipped != 0 {
		t.Fatalf("skipped = %d, want 0", skipped)
	}

	want := []ConntrackEntry{
		{
			Proto: 6, SrcIP: "192.168.1.10", DstIP: "192.168.1.20", SrcPort: 54321, DstPort: 443,
			State: "ESTABLISHED", TimeoutSec: 431999,
		},
		{
			Proto: 6, SrcIP: "192.168.1.15", DstIP: "192.168.1.25", SrcPort: 55000, DstPort: 80,
			State: "TIME_WAIT", TimeoutSec: 30,
		},
		{
			Proto: 17, SrcIP: "192.168.1.11", DstIP: "192.168.1.30", SrcPort: 51000, DstPort: 53,
			State: "ASSURED", TimeoutSec: 29,
		},
		{
			Proto: 1, SrcIP: "192.168.1.50", DstIP: "192.168.1.60", SrcPort: 0, DstPort: 0,
			State: "", TimeoutSec: 29,
		},
		{
			// SNAT: internal 192.168.1.5 masqueraded to 203.0.113.10 — the
			// reply tuple's dst reveals the translated source.
			Proto: 6, SrcIP: "192.168.1.5", DstIP: "8.8.8.8", SrcPort: 44444, DstPort: 443,
			State: "ESTABLISHED", TimeoutSec: 431999,
			NatSrc: &NatAddr{IP: "203.0.113.10", Port: 44444},
		},
		{
			// DNAT: public 203.0.113.10:8080 port-forwarded to backend
			// 192.168.1.100:80 — the reply tuple's src reveals the real
			// destination.
			Proto: 6, SrcIP: "203.0.113.5", DstIP: "203.0.113.10", SrcPort: 51000, DstPort: 8080,
			State: "ESTABLISHED", TimeoutSec: 431999,
			NatDst: &NatAddr{IP: "192.168.1.100", Port: 80},
		},
		{
			Proto: 6, SrcIP: "fd00::1", DstIP: "fd00::2", SrcPort: 1234, DstPort: 22,
			State: "ESTABLISHED", TimeoutSec: 431999,
		},
	}

	if len(entries) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(entries), len(want), entries)
	}
	for i, e := range entries {
		w := want[i]
		if e.Proto != w.Proto || e.SrcIP != w.SrcIP || e.DstIP != w.DstIP || e.SrcPort != w.SrcPort ||
			e.DstPort != w.DstPort || e.State != w.State || e.TimeoutSec != w.TimeoutSec {
			t.Errorf("entry %d = %+v, want %+v", i, e, w)
		}
		if !natAddrEqual(e.NatSrc, w.NatSrc) {
			t.Errorf("entry %d NatSrc = %+v, want %+v", i, e.NatSrc, w.NatSrc)
		}
		if !natAddrEqual(e.NatDst, w.NatDst) {
			t.Errorf("entry %d NatDst = %+v, want %+v", i, e.NatDst, w.NatDst)
		}
	}
}

func natAddrEqual(a, b *NatAddr) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// TestParseConntrackTable_MalformedLinesSkipped is T-1305's defensive-parse
// regression: a garbage-token line, a too-short line, a blank line, a line
// missing its original-direction src, a non-integer timeout, and a
// non-integer sport are all skipped (never fail the whole read); the one
// well-formed trailing line still parses.
func TestParseConntrackTable_MalformedLinesSkipped(t *testing.T) {
	entries, skipped := ParseConntrackTable(readConntrackTestdata(t, "conntrack_malformed.txt"))
	if len(entries) != 1 {
		t.Fatalf("got %d parsed entries, want 1: %+v", len(entries), entries)
	}
	if skipped != 6 {
		t.Fatalf("skipped = %d, want 6", skipped)
	}
	e := entries[0]
	if e.Proto != 17 || e.SrcIP != "192.168.1.99" || e.DstIP != "192.168.1.100" || e.SrcPort != 1000 || e.DstPort != 2000 {
		t.Errorf("surviving entry = %+v, unexpected", e)
	}
}

func TestParseConntrackTable_EmptyInput(t *testing.T) {
	entries, skipped := ParseConntrackTable(nil)
	if entries != nil || skipped != 0 {
		t.Fatalf("empty input: entries=%+v skipped=%d, want nil/0", entries, skipped)
	}
}
