package host

import (
	"bufio"
	"bytes"
	"net"
	"strconv"
	"strings"
)

// DHCPLease is one dnsmasq-format DHCP lease-file entry (T-406,
// docs/features/sdn.md §5: "a live leases view (parsed per-node via peer
// API)"). PVE SDN's per-zone dnsmasq instance writes its lease file in the
// standard dnsmasq .leases format: one line per lease, whitespace
// separated, "<expiry-epoch> <mac> <ip> <hostname-or-*> <client-id-or-*>".
type DHCPLease struct {
	MAC       string
	IP        string
	Hostname  string // "" when dnsmasq recorded "*" (no hostname reported)
	ClientID  string // "" when dnsmasq recorded "*" (no client-id reported)
	ExpiresAt int64  // unix seconds
}

// ParseDHCPLeases defensively parses raw dnsmasq .leases file content
// (host.Reader.DHCPLeases' output — the concatenation of every zone's
// lease file on a node): one lease per whitespace-separated line, skipping
// any line that doesn't parse cleanly (wrong field count, unparsable
// expiry/MAC/IP) rather than erroring the whole read out — T-406
// acceptance criterion 3: "a lease-file corpus (including malformed
// lines) parses defensively, never crashes, skips bad lines with a
// counter". Returns the successfully parsed leases plus a count of
// skipped (malformed) lines, so a caller can surface "N lines were
// unreadable" without treating it as a read failure.
func ParseDHCPLeases(raw []byte) (leases []DHCPLease, skipped int) {
	// Defensive: this parser must never panic the caller, even against
	// adversarial/corrupted input this simple field-splitting logic did
	// not anticipate (T-406 AC3's "never crashes").
	defer func() {
		if r := recover(); r != nil {
			skipped += len(leases) + 1
			leases = nil
		}
	}()

	scanner := bufio.NewScanner(bytes.NewReader(raw))
	// dnsmasq lease lines are short and fixed-shape, but a corrupted or
	// concatenated-with-garbage file could in principle carry an
	// enormous "line" with no newline; cap well above any real lease
	// entry so a pathological input degrades to "skip this one huge
	// line", not an unbounded buffer grow inside bufio.Scanner.
	scanner.Buffer(make([]byte, 0, 4096), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		lease, ok := parseDHCPLeaseLine(line)
		if !ok {
			skipped++
			continue
		}
		leases = append(leases, lease)
	}
	return leases, skipped
}

// parseDHCPLeaseLine parses one dnsmasq lease line:
// "<expiry> <mac> <ip> <hostname|*> <client-id|*>". A duid-keyed IPv6
// line (dnsmasq's own less common "duid <duid> <iaid> <ip> <hostname>"
// shape) does not match this positional layout and is skipped, not
// misparsed — this package only needs the far more common IPv4-lease
// shape PVE SDN's zones actually produce.
func parseDHCPLeaseLine(line string) (DHCPLease, bool) {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return DHCPLease{}, false
	}
	expiry, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil || expiry < 0 {
		return DHCPLease{}, false
	}
	mac := strings.ToLower(fields[1])
	if _, err := net.ParseMAC(mac); err != nil {
		return DHCPLease{}, false
	}
	ip := fields[2]
	if net.ParseIP(ip) == nil {
		return DHCPLease{}, false
	}
	lease := DHCPLease{ExpiresAt: expiry, MAC: mac, IP: ip}
	if len(fields) > 3 && fields[3] != "*" {
		lease.Hostname = fields[3]
	}
	if len(fields) > 4 && fields[4] != "*" {
		lease.ClientID = fields[4]
	}
	return lease, true
}
