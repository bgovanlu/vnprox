//go:build !linux

package host

// platformEthtoolSpeedDuplex has no implementation outside Linux (vnprox
// only ships for Linux); ok is always false so speedDuplex (ethtool.go)
// falls back to the (also-absent, so also empty) sysfs path, giving a
// harmless zero-value result rather than a compile failure on other
// platforms during development. mediaPort has no sysfs fallback anywhere
// (ioctl-only, see ethtool_linux.go), so it stays "" here unconditionally.
func platformEthtoolSpeedDuplex(_ string) (speedMbps int, duplex string, mediaPort string, ok bool) {
	return 0, "", "", false
}

// platformDriverInfo has no implementation outside Linux; see
// platformEthtoolSpeedDuplex above.
func platformDriverInfo(_ string) (driver, busInfo string, ok bool) {
	return "", "", false
}
