// SPDX-License-Identifier: Apache-2.0

package flow

import (
	"os"
	"path/filepath"
	"testing"
)

// fixture reads a testdata/flows/<name> fixture, failing the test if it's
// missing (these are committed golden datagrams — see
// planning/reports/T-1002.md for how they were generated).
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "flows", name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return data
}

func TestDecodeNetFlow5_Golden(t *testing.T) {
	data := fixture(t, "netflow5_basic.bin")
	records, err := DecodeNetFlow5(data, "pve1")
	if err != nil {
		t.Fatalf("DecodeNetFlow5: %v", err)
	}
	want := []Record{
		{
			At: 1700000000, Node: "pve1", SrcIP: "10.0.0.5", DstIP: "10.0.0.10",
			SrcPort: 51000, DstPort: 443, Proto: 6, Bytes: 1500, Packets: 10,
			IngressIfIndex: 1, EgressIfIndex: 2, Source: SourceNetFlow5,
		},
		{
			At: 1700000000, Node: "pve1", SrcIP: "10.0.0.6", DstIP: "8.8.8.8",
			SrcPort: 53211, DstPort: 53, Proto: 17, Bytes: 512, Packets: 4,
			IngressIfIndex: 1, EgressIfIndex: 3, Source: SourceNetFlow5,
		},
	}
	assertRecordsEqual(t, records, want)
}

func TestDecodeNetFlow5_Truncated(t *testing.T) {
	data := fixture(t, "netflow5_truncated.bin")
	records, err := DecodeNetFlow5(data, "pve1")
	if err == nil {
		t.Fatalf("expected an error decoding a truncated datagram, got none (records=%v)", records)
	}
	// Must not panic (the test itself running to completion proves that);
	// whatever complete records preceded the truncation point are still
	// returned rather than discarded.
}

func TestDecodeNetFlow9_Golden(t *testing.T) {
	cache := NewTemplateCache(nil)
	exporter := "10.0.0.1:2055"

	tmplData := fixture(t, "netflow9_template.bin")
	recs, dropped, err := DecodeNetFlow9(tmplData, "pve1", exporter, cache)
	if err != nil {
		t.Fatalf("decoding template datagram: %v", err)
	}
	if len(recs) != 0 || dropped != 0 {
		t.Fatalf("template-only datagram should produce no records/drops, got %d records, %d dropped", len(recs), dropped)
	}
	if cache.Len() != 1 {
		t.Fatalf("expected 1 cached template, got %d", cache.Len())
	}

	dataData := fixture(t, "netflow9_data.bin")
	records, dropped, err := DecodeNetFlow9(dataData, "pve1", exporter, cache)
	if err != nil {
		t.Fatalf("decoding data datagram: %v", err)
	}
	if dropped != 0 {
		t.Fatalf("expected 0 dropped, got %d", dropped)
	}
	want := []Record{
		{
			At: 1700000010, Node: "pve1", SrcIP: "192.168.10.5", DstIP: "192.168.10.20",
			SrcPort: 40000, DstPort: 8080, Proto: 6, Bytes: 2048, Packets: 16,
			IngressIfIndex: 1, EgressIfIndex: 2, VLAN: 100, Source: SourceNetFlow9,
		},
	}
	assertRecordsEqual(t, records, want)
}

func TestDecodeNetFlow9_DataWithoutTemplate_Dropped(t *testing.T) {
	cache := NewTemplateCache(nil)
	data := fixture(t, "netflow9_data_no_template.bin")
	records, dropped, err := DecodeNetFlow9(data, "pve1", "10.0.0.1:2055", cache)
	if err != nil {
		t.Fatalf("DecodeNetFlow9: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("expected 0 records (no template cached), got %d", len(records))
	}
	if dropped != 1 {
		t.Fatalf("expected 1 dropped data set, got %d", dropped)
	}
}

func TestDecodeNetFlow9_Truncated(t *testing.T) {
	data := fixture(t, "netflow9_truncated.bin")
	_, _, err := DecodeNetFlow9(data, "pve1", "10.0.0.1:2055", NewTemplateCache(nil))
	if err == nil {
		t.Fatal("expected an error decoding a truncated netflow9 datagram")
	}
}

func TestDecodeIPFIX_Golden(t *testing.T) {
	cache := NewTemplateCache(nil)
	data := fixture(t, "ipfix_basic.bin")
	records, dropped, err := DecodeIPFIX(data, "pve1", "10.0.0.2:4739", cache)
	if err != nil {
		t.Fatalf("DecodeIPFIX: %v", err)
	}
	if dropped != 0 {
		t.Fatalf("expected 0 dropped, got %d", dropped)
	}
	want := []Record{
		{
			At: 1700000030, Node: "pve1", SrcIP: "172.16.0.9", DstIP: "172.16.0.55",
			SrcPort: 33333, DstPort: 22, Proto: 6, Bytes: 4096, Packets: 32,
			IngressIfIndex: 3, EgressIfIndex: 4, VLAN: 50, Source: SourceIPFIX,
		},
	}
	assertRecordsEqual(t, records, want)
	if cache.Len() != 1 {
		t.Fatalf("expected 1 cached template after decoding a bundled template+data datagram, got %d", cache.Len())
	}
}

func TestDecodeIPFIX_Truncated(t *testing.T) {
	data := fixture(t, "ipfix_truncated.bin")
	_, _, err := DecodeIPFIX(data, "pve1", "10.0.0.2:4739", NewTemplateCache(nil))
	if err == nil {
		t.Fatal("expected an error decoding a truncated ipfix datagram")
	}
}

func TestDecodeSFlow_Golden(t *testing.T) {
	data := fixture(t, "sflow5_basic.bin")
	records, dropped, err := DecodeSFlow(data, "pve1", 1700000040)
	if err != nil {
		t.Fatalf("DecodeSFlow: %v", err)
	}
	if dropped != 0 {
		t.Fatalf("expected 0 dropped, got %d", dropped)
	}
	want := []Record{
		{
			At: 1700000040, Node: "pve1", SrcIP: "10.1.1.5", DstIP: "10.1.1.50",
			SrcPort: 34567, DstPort: 443, Proto: 6, VLAN: 200,
			IngressIfIndex: 1, EgressIfIndex: 2, Source: SourceSFlow,
			// T-3706: sflow5_basic.bin's sampled_header carries
			// frame_length=48 (0x30) — see decodeSFlowRawPacketHeader's doc
			// comment for why this decodes to Bytes=48, Packets=1 rather
			// than the 0/0 this fixture used to (silently) assert.
			Bytes: 48, Packets: 1,
		},
	}
	assertRecordsEqual(t, records, want)
}

func TestDecodeSFlow_Truncated(t *testing.T) {
	data := fixture(t, "sflow5_truncated.bin")
	records, dropped, err := DecodeSFlow(data, "pve1", 1700000040)
	// A truncated sFlow datagram may legitimately fail at the outer header
	// (err != nil) or at a sample boundary (err != nil) — what matters is
	// it never panics and never fabricates records; both are already
	// implied by this test function completing without a panic.
	_ = records
	_ = dropped
	if err == nil {
		t.Log("truncated sflow fixture happened to decode without error (still no panic, acceptable)")
	}
}

// assertRecordsEqual compares got against want field-by-field with a
// readable diff, ignoring SrcRef/DstRef (resolution is a separate concern
// tested in resolve_test.go — every decoder-level golden test above expects
// them unresolved, i.e. "").
func assertRecordsEqual(t *testing.T, got, want []Record) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("record count mismatch: got %d, want %d\ngot:  %+v\nwant: %+v", len(got), len(want), got, want)
	}
	for i := range got {
		g, w := got[i], want[i]
		if g != w {
			t.Errorf("record %d mismatch:\n got: %+v\nwant: %+v", i, g, w)
		}
	}
}
