package capturemock

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// GenerateCorpus materializes the pcap sample corpus into dir: one
// single-frame file per protocol (<kind>.pcap), one combined
// all-protocols.pcap covering every protocol in CorpusOrder, and a
// deliberately truncated file (truncated.pcap: a valid header plus a
// half-written record) for T-1302's "a corrupt/truncated sample decodes
// defensively" test. Timestamps are fixed so the generated files are
// byte-deterministic (stable golden diffs / reproducible commits).
func GenerateCorpus(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("capturemock: creating corpus dir %s: %w", dir, err)
	}
	base := time.Unix(1_700_000_000, 0).UTC()

	for i, kind := range CorpusOrder {
		path := filepath.Join(dir, string(kind)+".pcap")
		if err := writePcapFile(path, base.Add(time.Duration(i)*time.Second), []FrameKind{kind}); err != nil {
			return err
		}
	}
	if err := writePcapFile(filepath.Join(dir, "all-protocols.pcap"), base, CorpusOrder); err != nil {
		return err
	}
	return writeTruncated(filepath.Join(dir, "truncated.pcap"), base)
}

func writePcapFile(path string, base time.Time, kinds []FrameKind) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("capturemock: creating %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	bw := bufio.NewWriter(f)
	pw, err := newPcapWriter(bw)
	if err != nil {
		return err
	}
	for i, kind := range kinds {
		ts := base.Add(time.Duration(i) * time.Millisecond)
		if _, err := pw.writePacket(uint32(ts.Unix()), uint32(ts.Nanosecond()/1000), buildFrame(kind)); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// writeTruncated writes a valid global header + one complete frame + a
// second record header claiming a length longer than the bytes that follow
// (a mid-write truncation) — the defensive-decode fixture.
func writeTruncated(path string, base time.Time) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("capturemock: creating %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	bw := bufio.NewWriter(f)
	pw, err := newPcapWriter(bw)
	if err != nil {
		return err
	}
	ts := base
	if _, err := pw.writePacket(uint32(ts.Unix()), uint32(ts.Nanosecond()/1000), buildFrame(FrameICMP)); err != nil {
		return err
	}
	// A record header promising 200 bytes, followed by only 10 — a truncated
	// tail. writePacket can't express this (it always writes a full frame),
	// so emit the raw bytes directly.
	trunc := []byte{
		0x00, 0x00, 0x00, 0x00, // ts_sec
		0x00, 0x00, 0x00, 0x00, // ts_usec
		0xc8, 0x00, 0x00, 0x00, // incl_len = 200
		0xc8, 0x00, 0x00, 0x00, // orig_len = 200
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, // only 10 bytes
	}
	if _, err := bw.Write(trunc); err != nil {
		return err
	}
	return bw.Flush()
}
