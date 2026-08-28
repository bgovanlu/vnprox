// SPDX-License-Identifier: Apache-2.0

package route

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// fibRouteJSON is the tolerant wire shape of one entry in `ip -j route
// show table all`'s (and `-6`'s) top-level JSON array — see
// planning/reports/evidence/pve-9.2.4-routing-2026-08-28.txt for the exact
// observed shape this mirrors field-for-field. flags is accepted but
// discarded: every row this task's evidence observed carried an empty
// flags array, and iproute2's flag vocabulary (RTNH_F_* names) is not
// something the route-explorer UI has a use for today.
type fibRouteJSON struct {
	Type     string `json:"type"`
	Dst      string `json:"dst"`
	Gateway  string `json:"gateway"`
	Dev      string `json:"dev"`
	Protocol string `json:"protocol"`
	Scope    string `json:"scope"`
	PrefSrc  string `json:"prefsrc"`
	Table    string `json:"table"`
	Pref     string `json:"pref"`
	Metric   int    `json:"metric"`
}

// ParseFIBRoutes parses `ip -j route show table all` (afi == AFIv4) or
// `ip -j -6 route show table all` (afi == AFIv6) output into FIBRoute
// values. Malformed/truncated/adversarial input never panics: a parse
// failure on the whole document is returned as an error (recovering any
// unexpected internal panic first, the same defensive convention
// internal/host's ParseLLDP/ParseBGPSummary use), but there is no
// per-entry skip-and-continue here the way those two parsers have —
// `ip -j` emits one flat, uniformly-shaped JSON array with no
// per-element variance to tolerate (unlike LLDP's nested-vs-flat or FRR's
// AFI-block ambiguity), so a malformed individual element is exactly as
// suspicious as a malformed document and is treated the same way: the
// whole parse fails rather than silently dropping a route from a safety
// tool whose entire job is completeness.
//
// Empty input (a route table nothing wrote to yet, though `ip route show`
// always has at least the loopback in practice) returns a nil slice, no
// error — "no routes" is not itself an error condition, the same
// convention ParseBGPSummary/ParseLLDP use for empty input.
func ParseFIBRoutes(raw []byte, afi AFI) (routes []FIBRoute, err error) {
	defer func() {
		if r := recover(); r != nil {
			routes, err = nil, fmt.Errorf("route: fib: parser panic recovered: %v", r)
		}
	}()

	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, nil
	}

	var entries []fibRouteJSON
	if err := json.Unmarshal(trimmed, &entries); err != nil {
		return nil, fmt.Errorf("route: fib: parsing %s route table: %w", afi, err)
	}

	out := make([]FIBRoute, 0, len(entries))
	for _, e := range entries {
		out = append(out, FIBRoute{
			AFI:      afi,
			Table:    normalizeTable(e.Table),
			Type:     normalizeRouteType(e.Type),
			Dst:      normalizeDst(e.Dst, afi),
			Gateway:  e.Gateway,
			Dev:      e.Dev,
			Protocol: e.Protocol,
			Scope:    e.Scope,
			PrefSrc:  e.PrefSrc,
			Pref:     e.Pref,
			Metric:   e.Metric,
		})
	}
	return out, nil
}

// normalizeTable defaults an absent `table` field to "main" — iproute2
// omits the key entirely for the implicit main table (254) rather than
// naming it, only naming "local"/"default"/a custom table (see the
// evidence transcript's "Observations on the JSON shape" note).
func normalizeTable(t string) string {
	if t == "" {
		return "main"
	}
	return t
}

// normalizeRouteType defaults an absent `type` field to "unicast" —
// iproute2 only emits `type` for a non-unicast pseudo-route
// (local/broadcast/anycast/multicast/...); an ordinary forwarding route
// carries no `type` key at all.
func normalizeRouteType(t string) string {
	if t == "" {
		return "unicast"
	}
	return t
}

// normalizeDst turns iproute2's `dst` field into a valid CIDR string
// regardless of which of its three observed forms it arrived in:
// "default" (the zero network, spelled as a keyword rather than a CIDR),
// a bare host address with no "/prefixlen" (every "local"-table
// local/broadcast/anycast entry in the evidence transcript is exactly
// this — iproute2 omits the redundant /32 or /128 for a single-address
// route), or an already-well-formed CIDR (every "main"/multicast entry).
func normalizeDst(dst string, afi AFI) string {
	if dst == "default" {
		if afi == AFIv6 {
			return "::/0"
		}
		return "0.0.0.0/0"
	}
	for _, c := range dst {
		if c == '/' {
			return dst // already CIDR-form
		}
	}
	if dst == "" {
		return dst
	}
	if afi == AFIv6 {
		return dst + "/128"
	}
	return dst + "/32"
}

// String implements fmt.Stringer for AFI, so it interpolates cleanly into
// error messages (e.g. ParseFIBRoutes' own wrap above) without an
// explicit string(afi) cast at every call site.
func (a AFI) String() string { return string(a) }
