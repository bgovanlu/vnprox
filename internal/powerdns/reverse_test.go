// SPDX-License-Identifier: Apache-2.0

package powerdns

import (
	"net/netip"
	"testing"
)

// The expected values in TestReverseName are not hand-derived: they are the
// output of Net::IP's reverse_ip, run on pvecube with the same modules the
// plugin uses, captured in
// planning/reports/evidence/pve-9.2.4-sdn-dns-surface.txt. That is the whole
// value of the table — a PTR name vnprox invents from the RFC would look just
// as correct and land in a zone PVE never writes to.
func TestReverseName_MatchesNetIPsReverseIP(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"10.0.0.5", "5.0.0.10.in-addr.arpa."},
		{"192.168.1.9", "9.1.168.192.in-addr.arpa."},
		{"172.16.5.4", "4.5.16.172.in-addr.arpa."},
		{"203.0.113.7", "7.113.0.203.in-addr.arpa."},
		{"2001:db8::1", "1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.8.b.d.0.1.0.0.2.ip6.arpa."},
		// An IPv4-mapped IPv6 address is an IPv4 address; naming it in
		// ip6.arpa would put the PTR in a zone nothing resolves through.
		{"::ffff:10.0.0.5", "5.0.0.10.in-addr.arpa."},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ReverseName(netip.MustParseAddr(tt.in))
			if err != nil {
				t.Fatalf("ReverseName: %v", err)
			}
			if got != tt.want {
				t.Errorf("ReverseName(%s) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestReverseZone_PrivateRangesUsePowerDNSsBuiltinZones(t *testing.T) {
	tests := []struct {
		cidr string
		want string
	}{
		{"10.0.0.0/24", "10.in-addr.arpa."},
		{"10.5.0.0/16", "10.in-addr.arpa."},
		{"192.168.1.0/24", "168.192.in-addr.arpa."},
		{"172.16.5.0/24", "16-31.172.in-addr.arpa."},
		{"172.31.0.0/16", "16-31.172.in-addr.arpa."},
	}
	for _, tt := range tests {
		t.Run(tt.cidr, func(t *testing.T) {
			got, err := ReverseZone(netip.MustParsePrefix(tt.cidr), 0)
			if err != nil {
				t.Fatalf("ReverseZone: %v", err)
			}
			if got != tt.want {
				t.Errorf("ReverseZone(%s) = %q, want %q", tt.cidr, got, tt.want)
			}
		})
	}
}

// 172.15 and 172.32 are outside RFC1918 and must NOT get the private zone —
// the boundary is where a too-loose check would quietly misfile records.
func TestReverseZone_RFC1918BoundariesAreNotPrivate(t *testing.T) {
	for cidr, want := range map[string]string{
		"172.15.0.0/24":  "0.15.172.in-addr.arpa.",
		"172.32.0.0/24":  "0.32.172.in-addr.arpa.",
		"192.169.1.0/24": "1.169.192.in-addr.arpa.",
	} {
		got, err := ReverseZone(netip.MustParsePrefix(cidr), 0)
		if err != nil {
			t.Fatalf("%s: %v", cidr, err)
		}
		if got != want {
			t.Errorf("ReverseZone(%s) = %q, want %q", cidr, got, want)
		}
	}
}

// The quirk, asserted deliberately. PVE's Perl is
// `if ($mask <= 24) ... elsif ($mask <= 16) ... elsif ($mask <= 8)`, so the
// first arm always wins and a public /16 gets a three-label /24-shaped zone.
// vnprox copies it because it has to read and write the zone PVE actually
// uses. If this test ever fails because someone "fixed" the arithmetic, the
// fix is the bug: vnprox's PTRs would move to a zone PVE never touches.
func TestReverseZone_CopiesPVEsPublicIPv4Quirk(t *testing.T) {
	got, err := ReverseZone(netip.MustParsePrefix("203.0.0.0/16"), 0)
	if err != nil {
		t.Fatalf("ReverseZone: %v", err)
	}
	if want := "0.0.203.in-addr.arpa."; got != want {
		t.Errorf("ReverseZone(203.0.0.0/16) = %q, want %q — PVE's own /24-shaped answer", got, want)
	}
}

// A public prefix longer than /24 leaves the plugin's $zone as "". Returning
// "" with no error says "no reverse zone vnprox can name", which is the truth;
// inventing one would be the same class of error as the invented routes this
// card exists to remove.
func TestReverseZone_PublicPrefixLongerThan24HasNoName(t *testing.T) {
	got, err := ReverseZone(netip.MustParsePrefix("203.0.113.0/25"), 0)
	if err != nil {
		t.Fatalf("ReverseZone: %v", err)
	}
	if got != "" {
		t.Errorf("ReverseZone(203.0.113.0/25) = %q, want \"\"", got)
	}
}

func TestReverseZone_IPv6MatchesNetIP(t *testing.T) {
	tests := []struct {
		cidr string
		want string
	}{
		{"2001:db8::/64", "0.0.0.0.0.0.0.0.8.b.d.0.1.0.0.2.ip6.arpa."},
		{"2001:db8:1:2::/48", "1.0.0.0.8.b.d.0.1.0.0.2.ip6.arpa."},
		{"fd00::/8", "d.f.ip6.arpa."},
	}
	for _, tt := range tests {
		t.Run(tt.cidr, func(t *testing.T) {
			got, err := ReverseZone(netip.MustParsePrefix(tt.cidr), 0)
			if err != nil {
				t.Fatalf("ReverseZone: %v", err)
			}
			if got != tt.want {
				t.Errorf("ReverseZone(%s) = %q, want %q", tt.cidr, got, tt.want)
			}
		})
	}
}

// reversemaskv6 forces a shorter zone name, and must also drop the bits the
// name no longer covers — otherwise a /64's fourth hextet would survive into
// a /48 zone name that cannot contain it.
func TestReverseZone_ReverseMaskV6Override(t *testing.T) {
	got, err := ReverseZone(netip.MustParsePrefix("2001:db8:1:2::/64"), 48)
	if err != nil {
		t.Fatalf("ReverseZone: %v", err)
	}
	if want := "1.0.0.0.8.b.d.0.1.0.0.2.ip6.arpa."; got != want {
		t.Errorf("ReverseZone with reversemaskv6=48 = %q, want %q", got, want)
	}
}

// The plugin dies on a non-nibble mask. Rounding would produce a zone name
// PVE never writes to, which is worse than refusing.
func TestReverseZone_RefusesANonNibbleIPv6Mask(t *testing.T) {
	if _, err := ReverseZone(netip.MustParsePrefix("2001:db8::/63"), 0); err == nil {
		t.Error("want an error for a /63 reverse zone, got none")
	}
	if _, err := ReverseZone(netip.MustParsePrefix("2001:db8::/64"), 50); err == nil {
		t.Error("want an error for reversemaskv6=50, got none")
	}
}

func TestIsReverseZone(t *testing.T) {
	for zone, want := range map[string]bool{
		"10.in-addr.arpa.":                  true,
		"0.0.0.0.8.b.d.0.1.0.0.2.ip6.arpa.": true,
		"in-addr.arpa":                      true,
		"ip6.arpa.":                         true,
		"example.com.":                      false,
		"arpa.":                             false,
		// A forward zone that merely contains the string must not match.
		"in-addr.arpa.example.com.": false,
	} {
		if got := IsReverseZone(zone); got != want {
			t.Errorf("IsReverseZone(%q) = %v, want %v", zone, got, want)
		}
	}
}
