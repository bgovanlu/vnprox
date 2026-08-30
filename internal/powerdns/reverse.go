// SPDX-License-Identifier: Apache-2.0

package powerdns

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// Reverse-DNS naming, ported from PowerdnsPlugin.pm's get_reversedns_zone and
// add_ptr_record rather than from RFC 1035/3596, and verified against the
// same Perl modules PVE itself uses (Net::IP, NetAddr::IP) — the transcript
// is in planning/reports/evidence/pve-9.2.4-sdn-dns-surface.txt.
//
// This has to match PVE exactly in BOTH directions. When vnprox writes, a
// different zone name puts the PTR somewhere PVE will never look; when vnprox
// reads, a different zone name makes T-4109's PTR audit report every address
// as uncovered while the PTR sits happily in the zone next door.

// ReverseName returns the PTR record name for an address: the fully-reversed
// address with a trailing dot, which is Net::IP's reverse_ip and what
// add_ptr_record uses as the rrset name.
//
//	10.0.0.5    -> "5.0.0.10.in-addr.arpa."
//	2001:db8::1 -> "1.0.0. ... .8.b.d.0.1.0.0.2.ip6.arpa."  (all 32 nibbles)
func ReverseName(ip netip.Addr) (string, error) {
	if !ip.IsValid() {
		return "", fmt.Errorf("powerdns: %q is not an IP address", ip.String())
	}
	if ip.Is4In6() {
		ip = ip.Unmap()
	}
	if ip.Is4() {
		o := ip.As4()
		return fmt.Sprintf("%d.%d.%d.%d.in-addr.arpa.", o[3], o[2], o[1], o[0]), nil
	}
	return nibbleZone(ip.As16(), 128), nil
}

// ReverseZone returns the reverse zone a subnet's PTR records belong in:
// get_reversedns_zone, given the subnet's CIDR. reverseMaskV6 is the plugin
// instance's optional `reversemaskv6` override, which forces a different
// prefix length for the IPv6 reverse zone name (0 means "use the subnet's
// own"); it is ignored for IPv4, exactly as the plugin ignores it there.
//
// An empty return with a nil error is a real outcome, not an oversight: the
// plugin's IPv4 branch leaves $zone as "" for a public prefix longer than
// /24, and vnprox must not invent a zone name PVE would never use. Callers
// treat "" as "this subnet has no reverse zone vnprox can name".
func ReverseZone(cidr netip.Prefix, reverseMaskV6 int) (string, error) {
	if !cidr.IsValid() {
		return "", fmt.Errorf("powerdns: %q is not a CIDR", cidr.String())
	}
	addr := cidr.Addr()
	if addr.Is4In6() {
		addr = addr.Unmap()
		cidr = netip.PrefixFrom(addr, cidr.Bits()-96)
	}

	if addr.Is4() {
		return reverseZoneV4(addr, cidr.Bits()), nil
	}

	mask := cidr.Bits()
	if reverseMaskV6 > 0 {
		mask = reverseMaskV6
	}
	if mask%4 != 0 {
		// The plugin dies here: `die "reverse dns zone mask need to be a
		// multiple of 4"`. A nibble-boundary zone name cannot be built from
		// a prefix that does not land on one, and rounding would silently
		// produce a zone PVE never writes to.
		return "", fmt.Errorf("powerdns: reverse dns zone mask /%d is not a multiple of 4", mask)
	}
	if mask < 0 || mask > 128 {
		return "", fmt.Errorf("powerdns: reverse dns zone mask /%d is out of range", mask)
	}
	network := cidr.Masked().Addr()
	if reverseMaskV6 > 0 {
		// The override changes how much of the address names the zone, so
		// re-mask to it — otherwise a /64 subnet with reversemaskv6=48 would
		// keep bits 48-64 that the zone name does not include.
		p := netip.PrefixFrom(network, mask)
		if !p.IsValid() {
			return "", fmt.Errorf("powerdns: reversemaskv6 /%d does not apply to %s", mask, cidr)
		}
		network = p.Masked().Addr()
	}
	return nibbleZone(network.As16(), mask), nil
}

// reverseZoneV4 is get_reversedns_zone's IPv4 branch, quirk included.
//
// The RFC1918 special cases come first and are PowerDNS's own built-in
// private zones (`serve-rfc1918`), which is why 10/8 is named "10.in-addr.arpa."
// while a public /8 is not.
//
// The public branch reproduces a PVE bug rather than fixing it. The Perl
// reads `if ($mask <= 24) ... elsif ($mask <= 16) ... elsif ($mask <= 8)`,
// and since every mask <= 8 is also <= 24 the first arm always wins: a public
// /16 or /8 gets a /24-shaped three-label zone. Copying it is the whole point
// — vnprox has to read and write the zone PVE actually uses, and "correcting"
// it here would put vnprox's PTRs in a zone PVE never touches. Fixing it is
// upstream's call, not this client's.
func reverseZoneV4(addr netip.Addr, mask int) string {
	o := addr.As4()
	if isRFC1918(o) {
		switch o[0] {
		case 192:
			return "168.192.in-addr.arpa."
		case 172:
			return "16-31.172.in-addr.arpa."
		case 10:
			return "10.in-addr.arpa."
		}
	}
	switch {
	case mask <= 24:
		return fmt.Sprintf("%d.%d.%d.in-addr.arpa.", o[2], o[1], o[0])
	case mask <= 16:
		return fmt.Sprintf("%d.%d.in-addr.arpa.", o[1], o[0])
	case mask <= 8:
		return fmt.Sprintf("%d.in-addr.arpa.", o[0])
	default:
		// A public prefix longer than /24 has no delegated reverse zone the
		// plugin will name — it leaves $zone = "". Say so rather than guess.
		return ""
	}
}

// isRFC1918 mirrors NetAddr::IP's is_rfc1918 for the network address of a
// subnet: 10/8, 172.16/12, 192.168/16.
func isRFC1918(o [4]byte) bool {
	switch {
	case o[0] == 10:
		return true
	case o[0] == 172 && o[1] >= 16 && o[1] <= 31:
		return true
	case o[0] == 192 && o[1] == 168:
		return true
	default:
		return false
	}
}

// nibbleZone renders bits/4 nibbles of an IPv6 address in reverse order under
// ip6.arpa., which is Net::IP's reverse_ip for a network. bits must already
// be a multiple of 4 and within range; callers check.
func nibbleZone(a16 [16]byte, bits int) string {
	nibbles := bits / 4
	var b strings.Builder
	b.Grow(nibbles*2 + len("ip6.arpa."))
	for i := nibbles - 1; i >= 0; i-- {
		var n byte
		if i%2 == 0 {
			n = a16[i/2] >> 4
		} else {
			n = a16[i/2] & 0x0f
		}
		b.WriteString(strconv.FormatUint(uint64(n), 16))
		b.WriteByte('.')
	}
	b.WriteString("ip6.arpa.")
	return b.String()
}

// IsReverseZone reports whether a domain is a reverse zone (in-addr.arpa or
// ip6.arpa), regardless of trailing dot. The PTR audit uses it to tell a
// forward zone from a reverse one without re-deriving either.
func IsReverseZone(zone string) bool {
	z := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(zone), "."))
	return strings.HasSuffix(z, ".in-addr.arpa") || strings.HasSuffix(z, ".ip6.arpa") ||
		z == "in-addr.arpa" || z == "ip6.arpa"
}
