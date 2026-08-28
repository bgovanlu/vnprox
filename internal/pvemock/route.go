// SPDX-License-Identifier: Apache-2.0

// route.go implements T-3903's fixture-backed route-explorer reads:
// FixtureHostReader.RouteTableV4/V6, RouteRulesV4/V6, FRRRIBV4/V6. These
// are new *exported methods on FixtureHostReader*, not additions to the
// HostReader interface above (or to internal/host.Reader) — internal/
// route.Fetcher is a small, separately declared seam
// (internal/route/service.go) that *FixtureHostReader already satisfies
// once these methods exist, the same "small interface, real type
// satisfies it" pattern docs/architecture.md §2 documents, applied here
// so this task adds no ripple to every other HostReader/Reader
// implementer in the tree.
//
// Per CLAUDE.md's "never model a PVE object from internal/pvemock, from
// docs, or from Proxmox release notes" — this file inverts that: it is
// pvemock being modeled *from* an observed transcript
// (planning/reports/evidence/pve-9.2.4-routing-2026-08-28.txt), not the
// other way around. A node's routing table is synthesized from its
// already-fixture-declared NetIface list (Address/Gateway) the exact same
// way the real Linux kernel derives it: every configured interface
// address gets a "connected" network route (main table) plus a "local"
// host route and (for a wide-enough prefix) a broadcast route (local
// table) — matching the evidence transcript's own observed shape
// field-for-field, not an invented one — and any interface with a
// configured Gateway gets the default route. This keeps the fixture
// honest without inventing a second, independent "routes:" fixture
// dialect that could drift from what NetIface already declares.

package pvemock

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
)

// fibRouteWire is the JSON wire shape `ip -j route show table all`
// produces, matched field-for-field against
// planning/reports/evidence/pve-9.2.4-routing-2026-08-28.txt so
// internal/route.ParseFIBRoutes (which this fixture output feeds in
// tests, via internal/host's FixtureReader-equivalent wiring for this
// package) parses real output and fixture output identically.
type fibRouteWire struct {
	Type     string   `json:"type,omitempty"`
	Dst      string   `json:"dst"`
	Gateway  string   `json:"gateway,omitempty"`
	Dev      string   `json:"dev"`
	Protocol string   `json:"protocol,omitempty"`
	Scope    string   `json:"scope,omitempty"`
	PrefSrc  string   `json:"prefsrc,omitempty"`
	Table    string   `json:"table,omitempty"`
	Pref     string   `json:"pref,omitempty"`
	Flags    []string `json:"flags"`
	Metric   int      `json:"metric,omitempty"`
}

type policyRuleWire struct {
	Src      string `json:"src"`
	Table    string `json:"table"`
	Priority int    `json:"priority"`
}

type ribNexthopWire struct {
	IP                string `json:"ip,omitempty"`
	InterfaceName     string `json:"interfaceName"`
	DirectlyConnected bool   `json:"directlyConnected,omitempty"`
	Active            bool   `json:"active"`
	FIB               bool   `json:"fib"`
	Weight            int    `json:"weight"`
}

type ribRouteWire struct {
	Prefix    string           `json:"prefix"`
	Protocol  string           `json:"protocol"`
	VrfName   string           `json:"vrfName"`
	Uptime    string           `json:"uptime"`
	Nexthops  []ribNexthopWire `json:"nexthops"`
	Distance  int              `json:"distance"`
	Metric    int              `json:"metric"`
	Selected  bool             `json:"selected,omitempty"`
	Installed bool             `json:"installed,omitempty"`
}

// RouteTableV4 implements internal/route.Fetcher (T-3903): node's
// fixture-synthesized `ip -j route show table all` output.
func (h *FixtureHostReader) RouteTableV4(_ context.Context, node string) ([]byte, error) {
	ns, ok := h.state.node(node)
	if !ok {
		return nil, fmt.Errorf("pvemock: host reader: %w: node %q", ErrNotFound, node)
	}
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	return json.Marshal(synthesizeFIB(ns.network, false))
}

// RouteTableV6 implements internal/route.Fetcher: node's
// fixture-synthesized `ip -j -6 route show table all` output.
func (h *FixtureHostReader) RouteTableV6(_ context.Context, node string) ([]byte, error) {
	ns, ok := h.state.node(node)
	if !ok {
		return nil, fmt.Errorf("pvemock: host reader: %w: node %q", ErrNotFound, node)
	}
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	return json.Marshal(synthesizeFIB(ns.network, true))
}

// RouteRulesV4 implements internal/route.Fetcher: the stock three-rule
// policy-routing set every node in this task's evidence transcript
// carries (no VRF-lite/policy routing is configured in any fixture
// cluster today — see this file's doc comment).
func (h *FixtureHostReader) RouteRulesV4(_ context.Context, node string) ([]byte, error) {
	if _, ok := h.state.node(node); !ok {
		return nil, fmt.Errorf("pvemock: host reader: %w: node %q", ErrNotFound, node)
	}
	return json.Marshal([]policyRuleWire{
		{Priority: 0, Src: "all", Table: "local"},
		{Priority: 32766, Src: "all", Table: "main"},
		{Priority: 32767, Src: "all", Table: "default"},
	})
}

// RouteRulesV6 implements internal/route.Fetcher: IPv6's stock two-rule
// set (no empty "default" table rule by upstream kernel convention — see
// the evidence transcript's `ip -j -6 rule show` output).
func (h *FixtureHostReader) RouteRulesV6(_ context.Context, node string) ([]byte, error) {
	if _, ok := h.state.node(node); !ok {
		return nil, fmt.Errorf("pvemock: host reader: %w: node %q", ErrNotFound, node)
	}
	return json.Marshal([]policyRuleWire{
		{Priority: 0, Src: "all", Table: "local"},
		{Priority: 32766, Src: "all", Table: "main"},
	})
}

// FRRRIBV4 implements internal/route.Fetcher: node's fixture-synthesized
// `show ip route json` output, or ErrFRRUnavailable when node's fixture
// declares no `frr:` block at all — same convention as
// FRRBGPSummary/FRREVPNVNI above.
func (h *FixtureHostReader) FRRRIBV4(_ context.Context, node string) ([]byte, error) {
	ns, ok := h.state.node(node)
	if !ok {
		return nil, fmt.Errorf("pvemock: host reader: %w: node %q", ErrNotFound, node)
	}
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	if ns.frr == nil {
		return nil, fmt.Errorf("pvemock: host reader: %w: node %q", ErrFRRUnavailable, node)
	}
	return json.Marshal(synthesizeRIB(ns.network, false))
}

// FRRRIBV6 implements internal/route.Fetcher: node's fixture-synthesized
// `show ipv6 route json` output, or ErrFRRUnavailable — same convention as
// FRRRIBV4.
func (h *FixtureHostReader) FRRRIBV6(_ context.Context, node string) ([]byte, error) {
	ns, ok := h.state.node(node)
	if !ok {
		return nil, fmt.Errorf("pvemock: host reader: %w: node %q", ErrNotFound, node)
	}
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	if ns.frr == nil {
		return nil, fmt.Errorf("pvemock: host reader: %w: node %q", ErrFRRUnavailable, node)
	}
	return json.Marshal(synthesizeRIB(ns.network, true))
}

// synthesizeFIB derives a node's kernel FIB from its fixture-declared
// interface list, mirroring exactly what the Linux kernel itself installs
// for a configured interface: a "connected" network route in the main
// table, a "local" host route (plus a "broadcast" route for a
// wide-enough v4 prefix) in the local table, and — for whichever
// interface(s) declare a Gateway — the default route. See this file's
// doc comment for why this is derived from NetIface rather than a second,
// independent fixture dialect.
func synthesizeFIB(ifaces []NetIface, v6 bool) []fibRouteWire {
	pref := "" // IPv4 routes never carry RFC4191 pref (see the evidence transcript)
	if v6 {
		pref = "medium"
	}
	var out []fibRouteWire

	// Loopback is always present, exactly as every real Linux host's
	// kernel FIB carries it regardless of fixture-declared interfaces.
	if v6 {
		out = append(out, fibRouteWire{Type: "local", Dst: "::1", Dev: "lo", Table: "local", Protocol: "kernel", Flags: []string{}, Pref: "medium"})
	} else {
		out = append(out, fibRouteWire{Type: "local", Dst: "127.0.0.0/8", Dev: "lo", Table: "local", Protocol: "kernel", Scope: "host", PrefSrc: "127.0.0.1", Flags: []string{}})
		out = append(out, fibRouteWire{Type: "local", Dst: "127.0.0.1", Dev: "lo", Table: "local", Protocol: "kernel", Scope: "host", PrefSrc: "127.0.0.1", Flags: []string{}})
	}

	var defaultGW, defaultDev string
	for _, iface := range ifaces {
		if iface.Gateway != "" && defaultGW == "" {
			gw, err := netip.ParseAddr(iface.Gateway)
			if err == nil && gw.Is6() == v6 {
				defaultGW, defaultDev = iface.Gateway, iface.Iface
			}
		}
		if iface.Address == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(iface.Address)
		if err != nil || prefix.Addr().Is6() != v6 {
			continue
		}
		network := prefix.Masked()
		out = append(out, fibRouteWire{
			Dst: network.String(), Dev: iface.Iface, Protocol: "kernel", Scope: "link",
			PrefSrc: prefix.Addr().String(), Table: "main", Flags: []string{}, Pref: pref,
		})
		out = append(out, fibRouteWire{
			Type: "local", Dst: prefix.Addr().String(), Dev: iface.Iface, Table: "local",
			Protocol: "kernel", Scope: "host", PrefSrc: prefix.Addr().String(), Flags: []string{},
		})
		if !v6 {
			if bcast, ok := broadcastV4(prefix); ok {
				out = append(out, fibRouteWire{
					Type: "broadcast", Dst: bcast.String(), Dev: iface.Iface, Table: "local",
					Protocol: "kernel", Scope: "link", PrefSrc: prefix.Addr().String(), Flags: []string{},
				})
			}
		} else {
			// Every interface auto-configures a link-local /64,
			// independent of any explicitly declared global address
			// (matches the evidence transcript: fe80::/64 per
			// interface regardless of what else is configured).
			out = append(out, fibRouteWire{
				Dst: "fe80::/64", Dev: iface.Iface, Protocol: "kernel", Table: "main",
				Metric: 256, Flags: []string{}, Pref: "medium",
			})
		}
	}

	if v6 {
		// Deduplicate the per-interface fe80::/64 loop's potential
		// double-count is impossible here (one iface = one route), but
		// keep output ordering stable for test determinism.
		sort.SliceStable(out, func(i, j int) bool { return out[i].Dev < out[j].Dev })
	}
	if defaultGW != "" {
		def := "0.0.0.0/0"
		if v6 {
			def = "::/0"
		}
		out = append([]fibRouteWire{{Dst: def, Gateway: defaultGW, Dev: defaultDev, Flags: []string{}}}, out...)
	}
	return out
}

// synthesizeRIB derives FRR's RIB from the same fixture-declared
// interface list synthesizeFIB uses — see this file's doc comment.
// vnprox's own evidence transcript found FRR's RIB mirroring the kernel's
// FIB exactly on a node with no BGP-learned routes yet (protocol
// connected/local/kernel, no bgp-origin entries) — this fixture models
// that observed baseline; a fixture that wants BGP-learned RIB entries
// too can extend FRRSpec (types.go) in a follow-up rather than this
// function inventing an unobserved shape now.
func synthesizeRIB(ifaces []NetIface, v6 bool) map[string][]ribRouteWire {
	out := map[string][]ribRouteWire{}
	var defaultGW, defaultDev string
	for _, iface := range ifaces {
		if iface.Gateway != "" && defaultGW == "" {
			gw, err := netip.ParseAddr(iface.Gateway)
			if err == nil && gw.Is6() == v6 {
				defaultGW, defaultDev = iface.Gateway, iface.Iface
			}
		}
		if iface.Address == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(iface.Address)
		if err != nil || prefix.Addr().Is6() != v6 {
			continue
		}
		network := prefix.Masked().String()
		out[network] = []ribRouteWire{{
			Prefix: network, Protocol: "connected", VrfName: "default",
			Selected: true, Installed: true,
			Nexthops: []ribNexthopWire{{InterfaceName: iface.Iface, DirectlyConnected: true, Active: true, FIB: true, Weight: 1}},
		}}
		host := prefix.Addr().String() + hostSuffix(v6)
		out[host] = []ribRouteWire{{
			Prefix: host, Protocol: "local", VrfName: "default",
			Selected: true, Installed: true,
			Nexthops: []ribNexthopWire{{InterfaceName: iface.Iface, DirectlyConnected: true, Active: true, FIB: true, Weight: 1}},
		}}
	}
	if defaultGW != "" {
		def := "0.0.0.0/0"
		if v6 {
			def = "::/0"
		}
		out[def] = []ribRouteWire{{
			Prefix: def, Protocol: "kernel", VrfName: "default", Selected: true, Installed: true,
			Nexthops: []ribNexthopWire{{IP: defaultGW, InterfaceName: defaultDev, Active: true, FIB: true, Weight: 1}},
		}}
	}
	return out
}

func hostSuffix(v6 bool) string {
	if v6 {
		return "/128"
	}
	return "/32"
}

// broadcastV4 computes the broadcast address of an IPv4 prefix, or
// ok=false for /31 and /32 (no broadcast concept — mirrors the kernel's
// own behavior of not installing a broadcast route for those).
func broadcastV4(p netip.Prefix) (netip.Addr, bool) {
	if p.Bits() >= 31 {
		return netip.Addr{}, false
	}
	b := p.Addr().As4()
	hostMask := ^uint32(0) >> uint(p.Bits())
	val := binary.BigEndian.Uint32(b[:]) | hostMask
	var out [4]byte
	binary.BigEndian.PutUint32(out[:], val)
	return netip.AddrFrom4(out), true
}
