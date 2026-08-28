// SPDX-License-Identifier: Apache-2.0

package capturemock

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/bgovanlu/vnprox/internal/capture"
)

// readPcap parses a classic-pcap file, returning the count of packet records
// — a minimal reader to validate the mock agent / corpus output is real,
// well-formed pcap (the format T-1302's decoder will consume).
func readPcap(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(data) < 24 {
		t.Fatalf("%s: too short for a pcap header", path)
	}
	if binary.LittleEndian.Uint32(data[0:4]) != pcapMagic {
		t.Fatalf("%s: bad pcap magic 0x%x", path, binary.LittleEndian.Uint32(data[0:4]))
	}
	off := 24
	count := 0
	for off+16 <= len(data) {
		inclLen := int(binary.LittleEndian.Uint32(data[off+8 : off+12]))
		off += 16
		if off+inclLen > len(data) {
			break // truncated tail (the truncated.pcap fixture)
		}
		off += inclLen
		count++
	}
	return count
}

func TestAgentWritesValidPcap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cap.pcap")
	ag := NewAgent()
	proc, err := ag.Start(context.Background(), capture.Spec{
		SessionID: "s1", FilePath: path,
		Caps: capture.Caps{MaxPackets: 1000, MaxBytes: 1 << 20, MaxDurationSec: 60},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	res := proc.Result()
	if res.Packets != int64(len(CorpusOrder)) {
		t.Errorf("packets = %d, want %d (one per corpus protocol)", res.Packets, len(CorpusOrder))
	}
	if got := readPcap(t, path); got != len(CorpusOrder) {
		t.Errorf("pcap has %d records, want %d", got, len(CorpusOrder))
	}
}

func TestAgentHonorsPacketCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cap.pcap")
	ag := NewAgent()
	proc, err := ag.Start(context.Background(), capture.Spec{
		SessionID: "s1", FilePath: path,
		Caps: capture.Caps{MaxPackets: 3, MaxBytes: 1 << 20, MaxDurationSec: 60},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	res := proc.Result()
	if res.Packets != 3 {
		t.Errorf("packets = %d, want 3 (packet cap)", res.Packets)
	}
	if res.Status != capture.StatusCompleted {
		t.Errorf("status = %s, want completed (a cap was hit)", res.Status)
	}
	if got := readPcap(t, path); got != 3 {
		t.Errorf("pcap has %d records, want 3", got)
	}
}

func TestAgentHonorsByteCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cap.pcap")
	ag := NewAgent()
	// A byte cap just above the header + one small frame, so only the first
	// frame or two fit.
	proc, err := ag.Start(context.Background(), capture.Spec{
		SessionID: "s1", FilePath: path,
		Caps: capture.Caps{MaxPackets: 1000, MaxBytes: 120, MaxDurationSec: 60},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	res := proc.Result()
	if res.Status != capture.StatusCompleted {
		t.Errorf("status = %s, want completed (byte cap hit)", res.Status)
	}
	if res.Bytes > 120 {
		t.Errorf("bytes = %d, must not exceed the 120-byte cap", res.Bytes)
	}
}

func TestGenerateCorpusProducesEveryProtocol(t *testing.T) {
	dir := t.TempDir()
	if err := GenerateCorpus(dir); err != nil {
		t.Fatalf("GenerateCorpus: %v", err)
	}
	for _, kind := range CorpusOrder {
		p := filepath.Join(dir, string(kind)+".pcap")
		if n := readPcap(t, p); n != 1 {
			t.Errorf("%s: %d records, want 1", p, n)
		}
	}
	if n := readPcap(t, filepath.Join(dir, "all-protocols.pcap")); n != len(CorpusOrder) {
		t.Errorf("all-protocols.pcap: %d records, want %d", n, len(CorpusOrder))
	}
	// The truncated fixture: a valid first record, then a header promising
	// more bytes than exist — the reader stops at the good record.
	if n := readPcap(t, filepath.Join(dir, "truncated.pcap")); n != 1 {
		t.Errorf("truncated.pcap: %d full records, want 1", n)
	}
}

func TestUDPFrameCarriesMarker(t *testing.T) {
	// Guards the AC7 payload-marker test's assumption: the udp frame really
	// does embed "vnprox-udp".
	if !bytes.Contains(buildFrame(FrameUDP), []byte("vnprox-udp")) {
		t.Fatal("udp frame missing expected payload marker")
	}
}
