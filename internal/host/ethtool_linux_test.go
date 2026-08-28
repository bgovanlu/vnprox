//go:build linux

// SPDX-License-Identifier: Apache-2.0

package host

import "testing"

// TestPlatformEthtoolSpeedDuplex_RealInterface exercises the real
// SIOCETHTOOL ioctl path against whatever network interface actually
// exists in the current sandbox/CI environment, skipping cleanly if none
// is usable. It intentionally does not assert a specific speed/duplex —
// those depend on the host's real hardware/virtio NIC — only that the
// call either succeeds without error or fails gracefully (never panics),
// which is what net.Interfaces()/os detection can't tell us on its own.
func TestPlatformEthtoolSpeedDuplex_RealInterface(t *testing.T) {
	names, err := listIfaceNames()
	if err != nil {
		t.Skipf("cannot list /sys/class/net (unsupported sandbox): %v", err)
	}
	var target string
	for _, n := range names {
		if n != "lo" {
			target = n
			break
		}
	}
	if target == "" {
		t.Skip("no non-loopback interface available in this sandbox")
	}

	speed, duplex, media, ok := platformEthtoolSpeedDuplex(target)
	t.Logf("platformEthtoolSpeedDuplex(%s) = speed=%d duplex=%q media=%q ok=%v", target, speed, duplex, media, ok)
	// Whether or not the ioctl itself is supported by this NIC's driver
	// (virtio-net commonly returns speed/duplex fine; some drivers
	// return -EOPNOTSUPP), the sysfs fallback must also not error.
	sSpeed, sDuplex := sysfsSpeedDuplex(target)
	t.Logf("sysfsSpeedDuplex(%s) = speed=%d duplex=%q", target, sSpeed, sDuplex)
}

// TestMediaPortString pins the PORT_* (linux/ethtool.h) -> wire-string
// mapping T-3503 fixes, including the "never guessed" unrecognised case:
// planning/reports/evidence/pve-9.2.4-nic-media-and-speed.txt names exactly
// these eight PORT_* values as the ones read off pvecube's live ioctl.
func TestMediaPortString(t *testing.T) {
	// Field order is fieldalignment's, not the reading order: the two
	// strings pack ahead of the byte.
	tests := []struct {
		name string
		want string
		port uint8
	}{
		{"PORT_TP", "tp", 0x00},
		{"PORT_AUI", "aui", 0x01},
		{"PORT_MII", "mii", 0x02},
		{"PORT_FIBRE", "fibre", 0x03},
		{"PORT_BNC", "bnc", 0x04},
		{"PORT_DA", "da", 0x05},
		{"PORT_NONE", "none", 0xef},
		{"PORT_OTHER", "other", 0xff},
		{"unrecognised value between DA and NONE", "", 0x06},
		{"unrecognised value just under OTHER", "", 0xfe},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mediaPortString(tt.port); got != tt.want {
				t.Errorf("mediaPortString(0x%02x) = %q, want %q", tt.port, got, tt.want)
			}
		})
	}
}
