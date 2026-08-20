package host

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// speedDuplex resolves an interface's link speed (Mbps) and duplex mode.
//
// DEVIATION from the T-102 task card. planning/tasks/phase-1.md asks for
// "ethtool speed/duplex (netlink ethtool preferred, exec fallback)"; this
// package instead uses the classic SIOCETHTOOL ioctl (ETHTOOL_GSET) via
// golang.org/x/sys/unix — see ethtool_linux.go — with a sysfs fallback,
// implementing neither the card's preferred mechanism (the ethtool netlink
// genl protocol) nor its stated fallback (exec'ing the `ethtool` binary).
// The card did not pre-authorize this choice; the reasons for it are:
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
// mediaPort (the NIC's PORT_* media/connector type — "tp", "fibre", ...,
// see ethtool_linux.go's mediaPortString) rides along on the same ioctl
// call rather than a Go function of its own, because it comes from the
// same `struct ethtool_cmd` this function already fills for speed/duplex
// (planning/reports/evidence/pve-9.2.4-nic-media-and-speed.txt point 3).
// Unlike speed/duplex it is NOT gated by ok and has no sysfs fallback: the
// evidence transcript shows pvecube's down enp2s0/enp4s0 both still
// reporting "Port: Twisted Pair" with Speed/Duplex Unknown (point 2), so
// gating media on the same ok speed/duplex derives from would wrongly blank
// out a down link's port type. It is simply whatever
// platformEthtoolSpeedDuplex returned — "" on any platform/ioctl failure,
// the correct PORT_* mapping otherwise, independent of speed/duplex.
func speedDuplex(iface string) (speedMbps int, duplex string, mediaPort string) {
	speed, dpx, media, ok := platformEthtoolSpeedDuplex(iface)
	if ok {
		speedMbps, duplex = speed, dpx
	} else {
		speedMbps, duplex = sysfsSpeedDuplex(iface)
	}
	return speedMbps, duplex, media
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

// sysfsVFPCIAddr resolves one SR-IOV virtual function's PCI address via its
// PF's /sys/class/net/<iface>/device/virtfn<vfID> symlink (the kernel's own
// SR-IOV sysfs convention: each configured VF gets a virtfnN symlink
// pointing at that VF's own PCI device directory, named by its bus address
// — the same convention sysfsDriverInfo's "device" symlink read uses one
// level up, for the PF itself). Returns "" when the link doesn't exist
// (platform without sysfs, VF not yet configured, or a permission/race
// condition), never an error — this is a best-effort enrichment, not a
// required field (T-1506, needs-hardware-validation: not verified against
// real SR-IOV hardware).
func sysfsVFPCIAddr(iface string, vfID int) string {
	target, err := os.Readlink(filepath.Join(sysClassNetDir, iface, "device", "virtfn"+strconv.Itoa(vfID)))
	if err != nil {
		return ""
	}
	return filepath.Base(target)
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
