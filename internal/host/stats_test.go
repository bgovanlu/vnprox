package host

import (
	"os"
	"path/filepath"
	"testing"
)

func writeStatFile(t *testing.T, dir, name, val string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(val+"\n"), 0o644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

func TestReadIfaceStats(t *testing.T) {
	root := t.TempDir()
	orig := sysClassNetDir
	sysClassNetDir = root
	t.Cleanup(func() { sysClassNetDir = orig })

	statDir := filepath.Join(root, "eno1", "statistics")
	if err := os.MkdirAll(statDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeStatFile(t, statDir, "rx_bytes", "1048576000")
	writeStatFile(t, statDir, "tx_bytes", "524288000")
	writeStatFile(t, statDir, "rx_packets", "900000")
	writeStatFile(t, statDir, "tx_packets", "450000")
	writeStatFile(t, statDir, "rx_errors", "3")
	writeStatFile(t, statDir, "tx_errors", "0")
	// Deliberately omit rx_dropped/tx_dropped, as some drivers do not
	// expose every counter file.

	stats, err := readIfaceStats("eno1")
	if err != nil {
		t.Fatalf("readIfaceStats: %v", err)
	}
	want := IfaceStats{
		RxBytes: 1048576000, TxBytes: 524288000,
		RxPackets: 900000, TxPackets: 450000,
		RxErrors: 3, TxErrors: 0,
		RxDropped: 0, TxDropped: 0,
	}
	if stats != want {
		t.Errorf("readIfaceStats(eno1) = %+v, want %+v", stats, want)
	}
}

func TestReadIfaceStats_MissingDir(t *testing.T) {
	root := t.TempDir()
	orig := sysClassNetDir
	sysClassNetDir = root
	t.Cleanup(func() { sysClassNetDir = orig })

	if _, err := readIfaceStats("nope"); err == nil {
		t.Fatalf("expected error for missing statistics dir")
	}
}

func TestListIfaceNames(t *testing.T) {
	root := t.TempDir()
	orig := sysClassNetDir
	sysClassNetDir = root
	t.Cleanup(func() { sysClassNetDir = orig })

	for _, n := range []string{"lo", "eno1", "vmbr0"} {
		if err := os.MkdirAll(filepath.Join(root, n), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", n, err)
		}
	}
	names, err := listIfaceNames()
	if err != nil {
		t.Fatalf("listIfaceNames: %v", err)
	}
	if len(names) != 3 {
		t.Errorf("listIfaceNames() = %v, want 3 entries", names)
	}
}
