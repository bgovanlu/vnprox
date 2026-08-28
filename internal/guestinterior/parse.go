// SPDX-License-Identifier: Apache-2.0

package guestinterior

import (
	"encoding/json"
	"net"
	"strconv"
	"strings"

	"github.com/bgovanlu/vnprox/internal/pve"
)

// FromAgentInterfaces converts the QEMU guest agent's own
// network-get-interfaces shape (pve.AgentIface, already decoded by
// internal/pve — no further JSON parsing needed here) into this package's
// Interface/Address lists. Loopback is excluded (matches internal/ipam's
// own agentObservations filtering) — it is never a claim worth surfacing
// in an interior inspector.
func FromAgentInterfaces(ifaces []pve.AgentIface) ([]Interface, []Address) {
	interfaces := make([]Interface, 0, len(ifaces))
	var addresses []Address
	for _, iface := range ifaces {
		if strings.EqualFold(iface.Name, "lo") {
			continue
		}
		interfaces = append(interfaces, Interface{Name: iface.Name, Mac: iface.HardwareAddr, Up: true})
		for _, addr := range iface.IPAddresses {
			if addr.IPAddress == "" {
				continue
			}
			family := addr.IPAddressType
			if family == "" {
				family = guessFamily(addr.IPAddress)
			}
			addresses = append(addresses, Address{
				Interface: iface.Name, IP: addr.IPAddress, Family: family, Prefix: addr.Prefix,
			})
		}
	}
	return interfaces, addresses
}

func guessFamily(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed != nil && parsed.To4() != nil {
		return "ipv4"
	}
	return "ipv6"
}

// ipAddrShowEntry is one interface object in `ip -j addr show`'s JSON
// array output — the lxc host-side path's address source (there is no
// guest-agent equivalent for a container).
type ipAddrShowEntry struct {
	IfName   string             `json:"ifname"`
	Address  string             `json:"address,omitempty"`
	Flags    []string           `json:"flags,omitempty"`
	AddrInfo []ipAddrShowAddrOn `json:"addr_info,omitempty"`
	Mtu      int                `json:"mtu,omitempty"`
}

type ipAddrShowAddrOn struct {
	Family    string `json:"family"` // inet|inet6
	Local     string `json:"local"`
	Prefixlen int    `json:"prefixlen,omitempty"`
}

// ParseIPAddrJSON parses `ip -j addr show`'s JSON array output (the lxc
// host-side path's interface/address source) into this package's
// Interface/Address lists. A malformed/empty payload parses defensively to
// an empty result rather than erroring — this is a diagnostic read, not a
// contract worth failing the whole response over one bad command output.
func ParseIPAddrJSON(raw []byte) ([]Interface, []Address) {
	var entries []ipAddrShowEntry
	if len(raw) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, nil
	}
	interfaces := make([]Interface, 0, len(entries))
	var addresses []Address
	for _, e := range entries {
		if strings.EqualFold(e.IfName, "lo") {
			continue
		}
		up := false
		for _, f := range e.Flags {
			if strings.EqualFold(f, "UP") {
				up = true
			}
		}
		interfaces = append(interfaces, Interface{Name: e.IfName, Mac: e.Address, MTU: e.Mtu, Up: up})
		for _, a := range e.AddrInfo {
			if a.Local == "" {
				continue
			}
			family := "ipv4"
			if a.Family == "inet6" {
				family = "ipv6"
			}
			addresses = append(addresses, Address{
				Interface: e.IfName, IP: a.Local, Family: family, Prefix: a.Prefixlen,
			})
		}
	}
	return interfaces, addresses
}

// ipRouteShowEntry is one route object in `ip -j route show`'s JSON array
// output.
type ipRouteShowEntry struct {
	Dst     string `json:"dst"`
	Gateway string `json:"gateway,omitempty"`
	Dev     string `json:"dev,omitempty"`
	Metric  int    `json:"metric,omitempty"`
}

// ParseIPRouteJSON parses `ip -j route show`'s JSON array output — the
// shared route source for both the qemu (execed inside the guest) and lxc
// (execed inside the container's netns via nsenter) paths. Defensive on a
// malformed/empty payload, matching ParseIPAddrJSON above.
func ParseIPRouteJSON(raw []byte) []Route {
	var entries []ipRouteShowEntry
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil
	}
	out := make([]Route, 0, len(entries))
	for _, e := range entries {
		dst := e.Dst
		if dst == "" {
			continue
		}
		out = append(out, Route{Destination: dst, Gateway: e.Gateway, Dev: e.Dev, Metric: e.Metric})
	}
	return out
}

// ParseResolvConf parses /etc/resolv.conf content (resolver(5) syntax:
// `nameserver <ip>` / `search <domain...>` lines, one directive per line,
// `#`/`;` comments) into a DNSConfig — the shared DNS source for both the
// qemu and lxc paths.
func ParseResolvConf(raw string) DNSConfig {
	var cfg DNSConfig
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "nameserver":
			cfg.Nameservers = append(cfg.Nameservers, fields[1])
		case "search":
			cfg.SearchDomains = append(cfg.SearchDomains, fields[1:]...)
		}
	}
	return cfg
}

// ParseSS parses `ss -H -tuln`'s plain-text output (no header line, `-H`)
// into a ListeningSocket list — the shared listening-socket source for
// both the qemu and lxc paths. Expected column layout:
//
//	Netid  State   Recv-Q  Send-Q  Local Address:Port  Peer Address:Port
//
// Only LISTEN(tcp)/UNCONN(udp) rows are surfaced (ss's own "listening"
// states for each protocol); any other state (e.g. an already-tracked
// ESTAB tcp row, which -tuln should never emit but a hand-run command
// might) is skipped rather than misreported as listening. A malformed
// line is skipped, not fatal to the rest.
func ParseSS(raw string) []ListeningSocket {
	var out []ListeningSocket
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		proto := strings.ToLower(fields[0])
		state := strings.ToUpper(fields[1])
		if proto == "tcp" && state != "LISTEN" {
			continue
		}
		if proto == "udp" && state != "UNCONN" {
			continue
		}
		if proto != "tcp" && proto != "udp" {
			continue
		}
		local := fields[4]
		addr, portStr := splitLastColon(local)
		port, err := strconv.Atoi(portStr)
		if err != nil {
			continue
		}
		out = append(out, ListeningSocket{Proto: proto, LocalAddr: addr, LocalPort: port})
	}
	return out
}

// splitLastColon splits "addr:port" on the last ':' — safe for a bracketed
// IPv6 local address (e.g. "[::]:22" or "*:53"), unlike a naive
// strings.Split(s, ":").
func splitLastColon(s string) (addr, port string) {
	idx := strings.LastIndexByte(s, ':')
	if idx < 0 {
		return s, ""
	}
	addr = strings.TrimSuffix(strings.TrimPrefix(s[:idx], "["), "]")
	return addr, s[idx+1:]
}
