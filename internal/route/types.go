// SPDX-License-Identifier: Apache-2.0

package route

// AFI is an address family: IPv4 or IPv6. Kept as a distinct string type
// (rather than a bare bool) because it flows straight into API JSON and UI
// labels verbatim — a bool would need a translation layer at every one of
// those boundaries for no benefit.
type AFI string

const (
	AFIv4 AFI = "ipv4"
	AFIv6 AFI = "ipv6"
)

// FIBRoute is one kernel routing-table entry, as `ip -j route show table
// all` (and its `-6` counterpart) reports it — see ParseFIBRoutes and
// planning/reports/evidence/pve-9.2.4-routing-2026-08-28.txt for the exact
// observed JSON shape this is parsed from.
type FIBRoute struct {
	// Table is the routing table name: "main" (the default, implicit
	// table iproute2 omits from JSON — ParseFIBRoutes fills it in),
	// "local" (host-local/broadcast/anycast/multicast pseudo-routes,
	// consulted before "main" by the default `ip rule` set's priority-0
	// "lookup local" rule), "default", or a named/numbered custom
	// policy-routing table.
	Table string
	// Type is the route type: "unicast" (the default, implicit type
	// ParseFIBRoutes fills in when absent), "local", "broadcast",
	// "anycast", "multicast", "blackhole", "unreachable", "prohibit", or
	// "throw".
	Type string
	// Dst is the destination prefix in CIDR form, normalized: iproute2's
	// own "default" spelling for the zero network is expanded to
	// "0.0.0.0/0" (AFIv4) / "::/0" (AFIv6), and a bare host address with
	// no "/prefixlen" (as "local"-table entries report their own address)
	// is given the family's full-length prefix (/32 or /128) so every
	// entry is a valid CIDR a caller can net.ParseCIDR without a
	// family-specific special case.
	Dst string
	// Gateway is the next-hop IP for an indirect route ("" for a directly
	// connected/on-link route, which carries PrefSrc instead).
	Gateway string
	Dev     string
	// Protocol is the route's origin as the kernel records it: "kernel"
	// (auto-installed for a configured interface's own network or a
	// static default), "static" (an administrator/ifupdown2-configured
	// route), "boot", "dhcp", or a routing daemon's own protocol name
	// (FRR registers as "zebra" when it installs kernel routes it
	// learned from BGP/OSPF/etc — distinct from the *FRR RIB's* own
	// per-route Protocol field in RIBRoute, which uses FRR's own
	// vocabulary of "bgp"/"ospf"/"connected"/... instead).
	Protocol string
	Scope    string
	// PrefSrc is the source address the kernel would pick sending via
	// this route ("" when the route carries no explicit preferred
	// source, e.g. some local-table pseudo-routes).
	PrefSrc string
	// Pref is IPv6's RFC 4191 route preference ("low"/"medium"/"high"),
	// present only on AFIv6 entries; always "" for AFIv4 (v4 has no such
	// field — see the evidence transcript's field-presence note).
	Pref   string
	AFI    AFI
	Metric int
}

// PolicyRule is one `ip rule` policy-routing rule, from `ip -j rule show`
// (and its `-6` counterpart) — see ParsePolicyRules.
type PolicyRule struct {
	// Src is the rule's source-match selector, almost always "all" (no
	// source-specific policy routing configured) for the stock rule set
	// this task's evidence transcript observed; a real VRF-lite/policy
	// routing configuration can narrow this to a specific
	// address/prefix.
	Src string
	// Table is the routing table this rule selects when it matches:
	// "local", "main", "default", or a named/numbered custom table —
	// same vocabulary as FIBRoute.Table.
	Table    string
	AFI      AFI
	Priority int
}

// RIBRoute is one FRR RIB entry, from `vtysh -c "show ip route json"` (and
// "show ipv6 route json") — see ParseFRRRIB. Unlike FIBRoute (the kernel's
// own FIB, which holds exactly the routes actually installed), FRR's RIB
// can hold multiple candidate routes for the same prefix (e.g. both a
// locally-configured static route and a less-preferred BGP-learned one);
// Selected/Installed distinguish "this is the one FRR chose" from "FRR
// knows about this candidate but did not install it."
type RIBRoute struct {
	// VRF is the VRF this route belongs to ("default" when FRR/the node
	// runs no VRF-lite zones, which is every node in this task's evidence
	// transcript — see frrrib.go's tolerant-shape doc comment for how a
	// non-default VRF is still recognized when one exists).
	VRF string
	// Prefix is the destination CIDR, verbatim from FRR (already
	// CIDR-form in both observed shapes — no "default" special case like
	// FIBRoute.Dst needs, since vtysh's JSON always renders the zero
	// network as "0.0.0.0/0"/"::/0").
	Prefix string
	// Protocol is FRR's own route-origin vocabulary: "connected",
	// "local", "kernel", "static", "bgp", "ospf", "isis", ... — FRR's
	// terms, not the kernel FIB's (contrast FIBRoute.Protocol).
	Protocol string
	Uptime   string
	// AFI is a string type, so it belongs with the pointer-bearing fields
	// above the ints and bools: govet's fieldalignment measures bytes up to
	// the final pointer, and a string sitting below an int costs alignment
	// for nothing.
	AFI       AFI
	Nexthops  []RIBNextHop
	Distance  int
	Metric    int
	Selected  bool
	Installed bool
}

// RIBNextHop is one next hop of a RIBRoute.
type RIBNextHop struct {
	IP        string
	Interface string
	// DirectlyConnected is true for an on-link next hop with no gateway
	// IP (mirrors FRR's own "directlyConnected" JSON field).
	DirectlyConnected bool
	Active            bool
	// FIB reports whether this specific next hop is one of the ones FRR
	// actually installed into the kernel FIB (a route can have several
	// candidate next hops with only some of them FIB-installed, e.g.
	// ECMP with an unequal-cost fallback).
	FIB    bool
	Weight int
}
