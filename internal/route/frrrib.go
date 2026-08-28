// SPDX-License-Identifier: Apache-2.0

package route

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// ribRouteJSON is the tolerant wire shape of one entry in `vtysh -c "show
// ip route json"` (and "show ipv6 route json")'s per-prefix array — see
// planning/reports/evidence/pve-9.2.4-routing-2026-08-28.txt.
type ribRouteJSON struct {
	Prefix    string           `json:"prefix"`
	Protocol  string           `json:"protocol"`
	VrfName   string           `json:"vrfName"`
	Uptime    string           `json:"uptime"`
	Nexthops  []ribNexthopJSON `json:"nexthops"`
	Distance  int              `json:"distance"`
	Metric    int              `json:"metric"`
	Selected  boolOrAbsent     `json:"selected"`
	Installed boolOrAbsent     `json:"installed"`
}

type ribNexthopJSON struct {
	IP                string `json:"ip"`
	InterfaceName     string `json:"interfaceName"`
	DirectlyConnected bool   `json:"directlyConnected"`
	Active            bool   `json:"active"`
	FIB               bool   `json:"fib"`
	Weight            int    `json:"weight"`
}

// boolOrAbsent is a plain bool for JSON purposes; FRR simply omits
// selected/installed for a non-selected/non-installed candidate route
// rather than emitting `false` (see the evidence transcript's fe80::/64
// entries: two of the three connected candidates carry neither key at
// all) — encoding/json already treats an absent bool field as its zero
// value (false), so this is a named type purely for the doc comment
// above to point at, not a custom UnmarshalJSON.
type boolOrAbsent = bool

// ParseFRRRIB parses FRR's RIB JSON for one address family (afi ==
// AFIv4 for `show ip route json`, AFIv6 for `show ipv6 route json`) into
// RIBRoute values, tolerant of both shapes FRR is observed to produce
// (see planning/reports/evidence/pve-9.2.4-routing-2026-08-28.txt's "vtysh
// -c show ip route vrf all json" section):
//
//   - the plain, single (default)-VRF shape: a top-level JSON object keyed
//     by prefix, each value an array of candidate routes for that prefix
//     (`show ip route json`'s own output — the shape internal/host's
//     Real.FRRRIBV4/V6 fetches day-to-day, since no node in this
//     project's lab cluster runs a non-default VRF).
//   - the `vrf all`-wrapped shape: a top-level object keyed by VRF name,
//     each value itself a per-prefix object of the same shape as above.
//
// Rather than branching on which shape the top-level document is (a
// structural guess that a future FRR release could invalidate the same
// way frr.go's ParseBGPSummary found real builds disagree on BGP's own
// AFI-nesting), this walks the top level and, for each key's value,
// tries "is this an array of route objects" first; if that fails, it
// tries "is this an object whose values are each an array of route
// objects" (the vrf-all wrapper) instead. Either way, each route's own
// embedded vrfName field is what ends up in RIBRoute.VRF — never the
// wrapper key alone — so a future third shape that nests differently but
// still carries vrfName per-route degrades to "parsed, correctly
// VRF-labeled" rather than "silently mis-attributed to the wrong VRF."
func ParseFRRRIB(raw []byte, afi AFI) (routes []RIBRoute, err error) {
	defer func() {
		if r := recover(); r != nil {
			routes, err = nil, fmt.Errorf("route: frrrib: parser panic recovered: %v", r)
		}
	}()

	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, nil
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &top); err != nil {
		return nil, fmt.Errorf("route: frrrib: parsing %s RIB: %w", afi, err)
	}

	var keys []string
	for k := range top {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic iteration order

	var out []RIBRoute
	for _, k := range keys {
		if entries, ok := tryRIBEntryArray(top[k]); ok {
			out = append(out, ribRoutesFromEntries(entries, afi, k)...)
			continue
		}
		// Not an array of route objects for this key — try the
		// vrf-all wrapper shape: this key names a VRF, its value is
		// itself a per-prefix object.
		var inner map[string]json.RawMessage
		if err := json.Unmarshal(top[k], &inner); err != nil {
			continue // neither shape — skip this key rather than failing the whole parse
		}
		var innerKeys []string
		for ik := range inner {
			innerKeys = append(innerKeys, ik)
		}
		sort.Strings(innerKeys)
		for _, ik := range innerKeys {
			if entries, ok := tryRIBEntryArray(inner[ik]); ok {
				out = append(out, ribRoutesFromEntries(entries, afi, k)...)
			}
		}
	}
	return out, nil
}

// tryRIBEntryArray attempts to unmarshal raw as a []ribRouteJSON,
// reporting ok=false (rather than an error) when it is not shaped that
// way — the caller uses this purely to distinguish the plain vs. vrf-all
// shape, not to surface a parse failure.
func tryRIBEntryArray(raw json.RawMessage) ([]ribRouteJSON, bool) {
	var entries []ribRouteJSON
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, false
	}
	return entries, true
}

// ribRoutesFromEntries converts a decoded per-prefix array to RIBRoute
// values. fallbackVRF is used only when an entry's own vrfName field is
// empty (defensive — every observed entry always carries one).
func ribRoutesFromEntries(entries []ribRouteJSON, afi AFI, fallbackVRF string) []RIBRoute {
	out := make([]RIBRoute, 0, len(entries))
	for _, e := range entries {
		vrf := e.VrfName
		if vrf == "" {
			vrf = fallbackVRF
		}
		nhs := make([]RIBNextHop, 0, len(e.Nexthops))
		for _, nh := range e.Nexthops {
			nhs = append(nhs, RIBNextHop{
				IP:                nh.IP,
				Interface:         nh.InterfaceName,
				DirectlyConnected: nh.DirectlyConnected,
				Active:            nh.Active,
				FIB:               nh.FIB,
				Weight:            nh.Weight,
			})
		}
		out = append(out, RIBRoute{
			AFI:       afi,
			VRF:       vrf,
			Prefix:    e.Prefix,
			Protocol:  e.Protocol,
			Distance:  e.Distance,
			Metric:    e.Metric,
			Selected:  e.Selected,
			Installed: e.Installed,
			Uptime:    e.Uptime,
			Nexthops:  nhs,
		})
	}
	return out
}
