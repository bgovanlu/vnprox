package host

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// speedDuplex resolves an interface's link speed (Mbps) and duplex mode.
//
// Chosen approach (per T-102's ethtool deliverable: "netlink ethtool ioctl
// preferred; exec-based ethtool fallback acceptable — document which you
// chose and why"): this package uses the classic SIOCETHTOOL ioctl
// (ETHTOOL_GSET) via golang.org/x/sys/unix — see ethtool_linux.go — rather
// than either the newer ethtool netlink (genl) protocol or shelling out to
// the `ethtool` binary. Reasons:
//   - It requires no new third-party dependency: golang.org/x/sys is
//     already in the module graph (transitively, via modernc.org/sqlite),
//     and github.com/vishvananda/netlink does not implement ethtool at all
//     (verified: no ethtool*.go in its v1.3.1 source), so "netlink ethtool"
//     in the literal genl sense would require an additional dependency
//     (e.g. github.com/mdlayher/ethtool) that docs/development.md's
//     dependency list does not pre-approve.
//   - It needs no subprocess/exec.Command and no output parsing, unlike an
//     `ethtool eth0` shell-out, and — like reading netlink — works without
//     CAP_NET_ADMIN (verified in this sandbox: unprivileged SIOCETHTOOL
//     GSET calls succeed for a GET).
//
// ETHTOOL_GSET is deprecated in favor of ETHTOOL_GLINKSETTINGS on kernels
// that need to report link speeds ETHTOOL_GSET's 32-bit encoding cannot
// represent (>~4.2 Gbps in some historical encodings) or newer link mode
// bits; rather than implement GLINKSETTINGS's two-call variable-length
// buffer protocol for that edge case, this package falls back to reading
// /sys/class/net/<iface>/speed and .../duplex — which the kernel populates
// from the same link settings and does not share GSET's encoding limits —
// whenever the ioctl fails or is unavailable (including on non-Linux
// platforms, where ethtool_other.go's stub always defers to this path).
func speedDuplex(iface string) (speedMbps int, duplex string) {
	if speed, dpx, ok := platformEthtoolSpeedDuplex(iface); ok {
		return speed, dpx
	}
	return sysfsSpeedDuplex(iface)
}

// driverInfo resolves an interface's kernel driver name and bus address
// (e.g. a PCI address like "0000:01:00.0"), preferring the ETHTOOL_GDRVINFO
// ioctl (golang.org/x/sys/unix already exports a ready-made helper for it,
// IoctlGetEthtoolDrvinfo — no need to hand-roll the ifreq plumbing twice)
// and falling back to the /sys/class/net/<iface>/device{,/driver} symlinks,
// which resolve to the same information via a different kernel path and
// work identically on non-PCI (e.g. virtio) devices.
func driverInfo(iface string) (driver, busInfo string) {
	if d, b, ok := platformDriverInfo(iface); ok {
		return d, b
	}
	return sysfsDriverInfo(iface)
}

func sysfsDriverInfo(iface string) (driver, busInfo string) {
	base := filepath.Join(sysClassNetDir, iface)
	if target, err := os.Readlink(filepath.Join(base, "device", "driver")); err == nil {
		driver = filepath.Base(target)
	}
	if target, err := os.Readlink(filepath.Join(base, "device")); err == nil {
		busInfo = filepath.Base(target)
	}
	return driver, busInfo
}

// sysfsSpeedDuplex reads /sys/class/net/<iface>/speed and .../duplex.
// Both files return an error (or, for speed, occasionally the sentinel
// value -1) when the link is down or the driver does not support
// reporting, in which case the corresponding field is left unpopulated.
func sysfsSpeedDuplex(iface string) (speedMbps int, duplex string) {
	if data, err := os.ReadFile(filepath.Join(sysClassNetDir, iface, "speed")); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && v > 0 {
			speedMbps = v
		}
	}
	if data, err := os.ReadFile(filepath.Join(sysClassNetDir, iface, "duplex")); err == nil {
		d := strings.TrimSpace(string(data))
		if d == "full" || d == "half" {
			duplex = d
		}
	}
	return speedMbps, duplex
}
