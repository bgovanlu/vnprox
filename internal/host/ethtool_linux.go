//go:build linux

package host

import (
	"bytes"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ethtoolCmd mirrors the kernel's `struct ethtool_cmd` (linux/ethtool.h),
// used with the legacy ETHTOOL_GSET/ETHTOOL_GLINKSETTINGS-predecessor
// ioctl command. Field order and sizes reproduce the C struct's natural
// packing exactly (44 bytes, no gaps) so it can be used directly as the
// ioctl's data buffer.
type ethtoolCmd struct {
	Cmd           uint32
	Supported     uint32
	Advertising   uint32
	Speed         uint16
	Duplex        uint8
	Port          uint8
	PhyAddress    uint8
	Transceiver   uint8
	Autoneg       uint8
	MdioSupport   uint8
	Maxtxpkt      uint32
	Maxrxpkt      uint32
	SpeedHi       uint16
	EthTPMdix     uint8
	EthTPMdixCtrl uint8
	LPAdvertising uint32
	Reserved      [2]uint32
}

const (
	ethtoolGSET = 0x00000001 // ETHTOOL_GSET

	duplexHalf    = 0x00
	duplexFull    = 0x01
	duplexUnknown = 0xff

	speedUnknownU16 = 0xffff
)

// ifreqData replicates the unexported layout golang.org/x/sys/unix uses
// internally for ifreq-with-pointer-data ioctls (see unix/ifreq_linux.go);
// that helper is not exported (as of golang.org/x/sys v0.44.0), so this
// package reconstructs the same struct shape to issue SIOCETHTOOL directly.
// Layout: 16-byte interface name + a pointer to the ethtool command
// struct, padded to match `struct ifreq`'s 24-byte union region on 64-bit
// platforms (linux/amd64, linux/arm64 — the two platforms T-102 targets).
//
// The field order below is load-bearing: it must mirror the kernel's
// `struct ifreq` (name first, at offset 0, matching ifr_name[IFNAMSIZ])
// byte-for-byte, since this struct is handed to the kernel directly via
// an ioctl syscall. Do not let a "sort fields to save memory" linter or
// autofix reorder it.
//
//nolint:govet // fieldalignment: layout is fixed by the kernel ABI, not negotiable
type ifreqData struct {
	name [unix.IFNAMSIZ]byte
	data unsafe.Pointer
	_    [16]byte
}

// platformEthtoolSpeedDuplex issues SIOCETHTOOL/ETHTOOL_GSET for iface and
// reports its link speed (Mbps) and duplex. ok is false whenever the ioctl
// itself could not be attempted or the driver reports unknown values,
// signaling the caller (speedDuplex, in ethtool.go) to fall back to
// sysfs.
func platformEthtoolSpeedDuplex(iface string) (speedMbps int, duplex string, ok bool) {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, 0)
	if err != nil {
		return 0, "", false
	}
	defer func() { _ = unix.Close(fd) }()

	var name [unix.IFNAMSIZ]byte
	if len(iface) >= unix.IFNAMSIZ {
		return 0, "", false
	}
	copy(name[:], iface)

	cmd := ethtoolCmd{Cmd: ethtoolGSET}
	ifr := ifreqData{name: name, data: unsafe.Pointer(&cmd)}

	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(unix.SIOCETHTOOL), uintptr(unsafe.Pointer(&ifr)))
	if errno != 0 {
		return 0, "", false
	}

	speed := uint32(cmd.Speed) | uint32(cmd.SpeedHi)<<16
	if cmd.Speed != speedUnknownU16 && speed > 0 {
		speedMbps = int(speed)
	}
	switch cmd.Duplex {
	case duplexFull:
		duplex = "full"
	case duplexHalf:
		duplex = "half"
	case duplexUnknown:
		// leave duplex == "" — nothing reported.
	}

	return speedMbps, duplex, speedMbps > 0 || duplex != ""
}

// platformDriverInfo issues SIOCETHTOOL/ETHTOOL_GDRVINFO for iface via
// golang.org/x/sys/unix's ready-made IoctlGetEthtoolDrvinfo helper
// (unlike GSET, x/sys/unix already exports the ifreq plumbing for this
// one).
func platformDriverInfo(iface string) (driver, busInfo string, ok bool) {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, 0)
	if err != nil {
		return "", "", false
	}
	defer func() { _ = unix.Close(fd) }()

	info, err := unix.IoctlGetEthtoolDrvinfo(fd, iface)
	if err != nil {
		return "", "", false
	}
	driver = nullTerminatedString(info.Driver[:])
	busInfo = nullTerminatedString(info.Bus_info[:])
	return driver, busInfo, driver != "" || busInfo != ""
}

func nullTerminatedString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}
