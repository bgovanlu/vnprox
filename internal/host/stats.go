// SPDX-License-Identifier: Apache-2.0

package host

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// sysClassNetDir is the sysfs root for network interfaces. Overridable in
// tests so statsFromSysfs can be exercised against a fake tree without
// root or a real NIC.
var sysClassNetDir = "/sys/class/net"

// readIfaceStats reads /sys/class/net/<iface>/statistics/* into an
// IfaceStats. Missing individual counter files are tolerated (left at
// zero) since not every driver/link kind exposes every counter (e.g. `lo`
// has no rx_dropped on some kernels); a missing statistics/ directory
// altogether is reported as an error.
func readIfaceStats(iface string) (IfaceStats, error) {
	dir := filepath.Join(sysClassNetDir, iface, "statistics")
	if _, err := os.Stat(dir); err != nil {
		return IfaceStats{}, fmt.Errorf("host: reading stats for %s: %w", iface, err)
	}

	var s IfaceStats
	fields := []struct {
		dst  *uint64
		file string
	}{
		{file: "rx_bytes", dst: &s.RxBytes},
		{file: "tx_bytes", dst: &s.TxBytes},
		{file: "rx_packets", dst: &s.RxPackets},
		{file: "tx_packets", dst: &s.TxPackets},
		{file: "rx_errors", dst: &s.RxErrors},
		{file: "tx_errors", dst: &s.TxErrors},
		{file: "rx_dropped", dst: &s.RxDropped},
		{file: "tx_dropped", dst: &s.TxDropped},
	}
	for _, f := range fields {
		v, err := readUint64File(filepath.Join(dir, f.file))
		if err == nil {
			*f.dst = v
		}
	}
	return s, nil
}

// listIfaceNames returns the names of every interface known to sysfs.
func listIfaceNames() ([]string, error) {
	entries, err := os.ReadDir(sysClassNetDir)
	if err != nil {
		return nil, fmt.Errorf("host: listing %s: %w", sysClassNetDir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names, nil
}

func readUint64File(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
}
