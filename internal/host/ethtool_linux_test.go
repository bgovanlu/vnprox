//go:build linux

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

	speed, duplex, ok := platformEthtoolSpeedDuplex(target)
	t.Logf("platformEthtoolSpeedDuplex(%s) = speed=%d duplex=%q ok=%v", target, speed, duplex, ok)
	// Whether or not the ioctl itself is supported by this NIC's driver
	// (virtio-net commonly returns speed/duplex fine; some drivers
	// return -EOPNOTSUPP), the sysfs fallback must also not error.
	sSpeed, sDuplex := sysfsSpeedDuplex(target)
	t.Logf("sysfsSpeedDuplex(%s) = speed=%d duplex=%q", target, sSpeed, sDuplex)
}
