// SPDX-License-Identifier: Apache-2.0

// frr.go implements T-404's FRR/BGP EVPN observability reader: parsing
// `vtysh -c "show bgp summary json"` and `vtysh -c "show evpn vni json"`
// output into typed Go values. Like lldp.go's ParseLLDP, these are pure
// functions over already-fetched bytes (Real fetches them via exec in
// netlink_linux.go; FixtureReader delegates to pvemock) so both production
// and fixture data flow through one parser, and so the parser itself is
// fuzzable in isolation (frr_fuzz_test.go).
//
// FRR's JSON output is not schema-versioned in any machine-checkable way,
// and varies across FRR releases (v8.x vs v9.x observed in the wild):
// `show bgp summary json`'s peer table is sometimes nested under one block
// per address family ("ipv4Unicast", "l2VpnEvpn", ...) and sometimes flat
// (a bare {"routerId","as","peers"} object with no AFI nesting, the shape
// `show ip bgp summary json` — and some older `show bgp summary json`
// builds — produce for a single-AFI setup); numeric fields are sometimes
// rendered as JSON numbers and sometimes as numeric strings (observed for
// large 4-byte ASNs on some builds); `show evpn vni json` is sometimes an
// object keyed by VNI-as-string and sometimes a bare array. Both parsers
// below tolerate all of these shapes rather than assuming one.

package host

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ErrFRRUnavailable indicates FRR's vtysh binary is not installed (or not
// reachable at all) on this node — docs/features/sdn.md §3's EVPN/BGP
// observability degrades gracefully when a node runs no FRR daemon at all
// (e.g. a node with no SDN zones configured), distinct from "installed but
// erroring". Real.FRRBGPSummary/FRREVPNVNI wrap exec's "not found" failure
// with this sentinel so callers (internal/evpn's aggregator, eventually)
// can report a clean per-node "no EVPN" instead of a hard error.
var ErrFRRUnavailable = errors.New("host: frr/vtysh not installed")

// BGPPeer is one BGP neighbor session as reported by one address-family
// block of `show bgp summary json` (or the top level, for the flat/
// single-AFI shape — see this file's doc comment).
type BGPPeer struct {
	// Addr is the neighbor's IP address (the JSON object's own key in
	// vtysh's "peers" map).
	Addr string
	// Hostname is the neighbor's advertised hostname, when FRR's hostname
	// capability negotiation resolved one (empty otherwise).
	Hostname string
	// AddressFamily is the AFI/SAFI block this observation came from
	// (e.g. "ipv4Unicast", "l2VpnEvpn"), or "" for the flat/single-AFI
	// shape where there is no block name at all. A single neighbor can
	// appear once per configured address family; docs/features/sdn.md
	// §3's EVPN peering matrix is built by internal/evpn preferring the
	// "l2VpnEvpn" observation for a given Addr when one exists.
	AddressFamily string
	// State is the BGP FSM state, normalized to FRR's canonical
	// capitalization: Idle|Connect|Active|OpenSent|OpenConfirm|
	// Established. FRR appends a parenthetical reason to some Idle
	// states (e.g. "Idle (Admin)", "Idle (PfxCt)") — that reason is
	// split out into StateReason, not left in State, so callers can
	// switch on State alone.
	State string
	// StateReason is the parenthetical qualifier FRR sometimes appends
	// to State (e.g. "Admin" for an administratively shut down neighbor,
	// "PfxCt" for a prefix-limit trip), or "" when State carried none.
	// This is the closest FRR's summary JSON comes to a "last error" for
	// a down session — docs/features/sdn.md §3's session detail surfaces
	// it as such.
	StateReason string
	RemoteAS    int
	PfxRcd      int
	PfxSnt      int
	// UptimeSecs is the session's current-state duration in seconds,
	// parsed from vtysh's human-readable peerUptime string (or, when
	// present and parseable, the peerUptimeMsec field). 0 when the
	// session has never been up ("never") or the uptime field could not
	// be parsed.
	UptimeSecs int64
}

// BGPSummary is one node's full `show bgp summary json` parse: every
// address-family block's router id/ASN (assumed consistent across blocks,
// which is always true for a single bgpd instance) and every observed
// peer, one BGPPeer per (address, address family) pair.
type BGPSummary struct {
	RouterID string
	Peers    []BGPPeer
	ASN      int
}

// bgpPeerJSON is the tolerant wire shape of one entry in an AFI block's
// "peers" map. Numeric fields accept either a JSON number or a numeric
// string (flexInt, shared with lldp.go, handles both) since observed FRR
// builds differ on this.
type bgpPeerJSON struct {
	Hostname       string          `json:"hostname"`
	State          string          `json:"state"`
	PeerUptime     string          `json:"peerUptime"`
	RemoteAS       json.RawMessage `json:"remoteAs"`
	PfxRcd         json.RawMessage `json:"pfxRcd"`
	PfxSnt         json.RawMessage `json:"pfxSnt"`
	PeerUptimeMsec json.RawMessage `json:"peerUptimeMsec"`
}

// bgpAFIBlockJSON is the tolerant wire shape of one address-family block
// (or the whole document, for the flat shape).
type bgpAFIBlockJSON struct {
	Peers    map[string]json.RawMessage `json:"peers"`
	RouterID string                     `json:"routerId"`
	AS       json.RawMessage            `json:"as"`
}

// ParseBGPSummary parses `vtysh -c "show bgp summary json"` output (see
// this file's doc comment for the shape variance tolerated). Malformed,
// truncated, or adversarial input never panics: any unexpected internal
// panic is recovered and returned as an error (matching ParseLLDP's
// convention), and an individual malformed peer or AFI block within an
// otherwise-parseable document is skipped rather than failing the whole
// parse. Empty input returns a zero BGPSummary, nil — "no output" is not
// itself an error; ErrFRRUnavailable is a distinct, sentinel-carrying
// condition callers detect from the *exec* failure, not from parsing empty
// output.
func ParseBGPSummary(raw []byte) (summary BGPSummary, err error) {
	defer func() {
		if r := recover(); r != nil {
			summary, err = BGPSummary{}, fmt.Errorf("host: frr: bgp summary parser panic recovered: %v", r)
		}
	}()

	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return BGPSummary{}, nil
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &top); err != nil {
		return BGPSummary{}, fmt.Errorf("host: frr: parsing bgp summary: %w", err)
	}

	// Flat shape: the document itself is one AFI block (no per-AFI
	// nesting) when a top-level "peers" key is present.
	if _, ok := top["peers"]; ok {
		block, ok := parseBGPAFIBlock(top)
		if !ok {
			return BGPSummary{}, nil
		}
		return summaryFromBlocks([]bgpParsedBlock{{name: "", block: block}}), nil
	}

	var blocks []bgpParsedBlock
	var afiNames []string
	for afi := range top {
		afiNames = append(afiNames, afi)
	}
	sort.Strings(afiNames) // deterministic iteration order
	for _, afi := range afiNames {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(top[afi], &obj); err != nil {
			continue // not an object (e.g. a stray top-level scalar) — skip
		}
		if _, ok := obj["peers"]; !ok {
			continue // not an AFI block
		}
		block, ok := parseBGPAFIBlock(obj)
		if !ok {
			continue
		}
		blocks = append(blocks, bgpParsedBlock{name: afi, block: block})
	}
	return summaryFromBlocks(blocks), nil
}

type bgpParsedBlock struct {
	name  string
	block bgpAFIBlockJSON
}

func parseBGPAFIBlock(obj map[string]json.RawMessage) (bgpAFIBlockJSON, bool) {
	b, err := json.Marshal(obj)
	if err != nil {
		return bgpAFIBlockJSON{}, false
	}
	var block bgpAFIBlockJSON
	if err := json.Unmarshal(b, &block); err != nil {
		return bgpAFIBlockJSON{}, false
	}
	return block, true
}

func summaryFromBlocks(blocks []bgpParsedBlock) BGPSummary {
	var summary BGPSummary
	for _, pb := range blocks {
		if summary.RouterID == "" {
			summary.RouterID = pb.block.RouterID
		}
		if summary.ASN == 0 {
			summary.ASN = flexInt(pb.block.AS)
		}
		var addrs []string
		for addr := range pb.block.Peers {
			addrs = append(addrs, addr)
		}
		sort.Strings(addrs)
		for _, addr := range addrs {
			var pj bgpPeerJSON
			if err := json.Unmarshal(pb.block.Peers[addr], &pj); err != nil {
				continue
			}
			state, reason := splitBGPState(pj.State)
			summary.Peers = append(summary.Peers, BGPPeer{
				Addr:          addr,
				Hostname:      pj.Hostname,
				AddressFamily: pb.name,
				State:         state,
				StateReason:   reason,
				RemoteAS:      flexInt(pj.RemoteAS),
				PfxRcd:        flexInt(pj.PfxRcd),
				PfxSnt:        flexInt(pj.PfxSnt),
				UptimeSecs:    parseFRRUptime(pj.PeerUptime, flexInt64(pj.PeerUptimeMsec)),
			})
		}
	}
	return summary
}

// splitBGPState splits a raw FRR state string into its canonical state and
// an optional parenthetical reason, e.g. "Idle (Admin)" -> ("Idle",
// "Admin"); "Established" -> ("Established", "").
func splitBGPState(raw string) (state, reason string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	open := strings.IndexByte(raw, '(')
	if open < 0 || !strings.HasSuffix(raw, ")") {
		return raw, ""
	}
	state = strings.TrimSpace(raw[:open])
	reason = strings.TrimSpace(strings.TrimSuffix(raw[open+1:], ")"))
	if state == "" {
		return raw, ""
	}
	return state, reason
}

// parseFRRUptime resolves a session's uptime in seconds, preferring the
// millisecond field (msec, when > 0 and parseable) and falling back to
// vtysh's human-readable peerUptime string, which takes one of (observed
// across FRR releases): "never" (session has never been established, or
// uptime is not tracked), "mm:ss", "hh:mm:ss", "XdYYhZZm" (1 day to <1
// week), or "XwYdZZh" (>=1 week). Returns 0 for "never", empty, or any
// unrecognized form rather than erroring — uptime is a display nicety, not
// a field this package's callers depend on for correctness.
func parseFRRUptime(s string, msec int64) int64 {
	if msec > 0 {
		return msec / 1000
	}
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "never") {
		return 0
	}
	if secs, ok := parseColonUptime(s); ok {
		return secs
	}
	if secs, ok := parseLetterUptime(s); ok {
		return secs
	}
	return 0
}

// parseColonUptime parses "mm:ss" or "hh:mm:ss".
func parseColonUptime(s string) (int64, bool) {
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	var vals []int64
	for _, p := range parts {
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil || n < 0 {
			return 0, false
		}
		vals = append(vals, n)
	}
	var secs int64
	for _, v := range vals {
		secs = secs*60 + v
	}
	return secs, true
}

// parseLetterUptime parses FRR's "XdYYhZZm" (days/hours/minutes) or
// "XwYdZZh" (weeks/days/hours) long-uptime formats: a sequence of
// <number><unit> tokens, unit in {w,d,h,m,s}, summed.
func parseLetterUptime(s string) (int64, bool) {
	var total int64
	var numBuf strings.Builder
	found := false
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			numBuf.WriteRune(r)
		case r == 'w' || r == 'd' || r == 'h' || r == 'm' || r == 's':
			if numBuf.Len() == 0 {
				return 0, false
			}
			n, err := strconv.ParseInt(numBuf.String(), 10, 64)
			if err != nil {
				return 0, false
			}
			numBuf.Reset()
			switch r {
			case 'w':
				total += n * 7 * 24 * 3600
			case 'd':
				total += n * 24 * 3600
			case 'h':
				total += n * 3600
			case 'm':
				total += n * 60
			case 's':
				total += n
			}
			found = true
		default:
			return 0, false
		}
	}
	if numBuf.Len() > 0 {
		return 0, false // trailing digits with no unit
	}
	return total, found
}

// flexInt64 is flexInt's (lldp.go) int64 counterpart, needed here for
// peerUptimeMsec — tolerates FRR's inconsistent numeric encodings (a bare
// JSON number or a numeric string, observed for ASNs and msec fields on
// some builds). Returns 0 for anything it cannot interpret.
func flexInt64(raw json.RawMessage) int64 {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return 0
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
			return v
		}
	}
	return 0
}

// EVPNVni is one VNI as reported by `show evpn vni json`: an L2 VNI
// (tenant bridge domain) or an L3 VNI (tenant VRF).
type EVPNVni struct {
	Type      string // "L2" | "L3"
	VxlanIf   string
	TenantVRF string
	VNI       int
	NumMacs   int
	NumArpND  int
}

// evpnVniJSON is the tolerant per-VNI wire shape.
type evpnVniJSON struct {
	Type      string          `json:"type"`
	VxlanIf   string          `json:"vxlanIf"`
	TenantVRF string          `json:"tenantVrf"`
	VNI       json.RawMessage `json:"vni"`
	NumMacs   json.RawMessage `json:"numMacs"`
	NumArpND  json.RawMessage `json:"numArpNd"`
}

// ParseEVPNVNI parses `vtysh -c "show evpn vni json"` output. Two shapes
// are recognized, auto-detected from the top-level JSON value (see this
// file's doc comment): a JSON object keyed by VNI-as-string (each value an
// evpnVniJSON), or a bare JSON array of the same per-VNI objects. As with
// ParseBGPSummary, malformed input never panics and a malformed individual
// entry is skipped rather than failing the whole parse.
func ParseEVPNVNI(raw []byte) (vnis []EVPNVni, err error) {
	defer func() {
		if r := recover(); r != nil {
			vnis, err = nil, fmt.Errorf("host: frr: evpn vni parser panic recovered: %v", r)
		}
	}()

	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, nil
	}
	switch trimmed[0] {
	case '{':
		return parseEVPNVniObject(trimmed)
	case '[':
		return parseEVPNVniArray(trimmed)
	default:
		return nil, fmt.Errorf("host: frr: evpn vni: unrecognized top-level JSON value (want object or array)")
	}
}

func parseEVPNVniObject(raw []byte) ([]EVPNVni, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("host: frr: parsing evpn vni object: %w", err)
	}
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]EVPNVni, 0, len(keys))
	for _, k := range keys {
		var vj evpnVniJSON
		if err := json.Unmarshal(m[k], &vj); err != nil {
			continue
		}
		vni := flexInt(vj.VNI)
		if vni == 0 {
			if n, err := strconv.Atoi(strings.TrimSpace(k)); err == nil {
				vni = n
			}
		}
		out = append(out, evpnVniFromJSON(vni, vj))
	}
	return out, nil
}

func parseEVPNVniArray(raw []byte) ([]EVPNVni, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("host: frr: parsing evpn vni array: %w", err)
	}
	out := make([]EVPNVni, 0, len(items))
	for _, item := range items {
		var vj evpnVniJSON
		if err := json.Unmarshal(item, &vj); err != nil {
			continue
		}
		out = append(out, evpnVniFromJSON(flexInt(vj.VNI), vj))
	}
	return out, nil
}

func evpnVniFromJSON(vni int, vj evpnVniJSON) EVPNVni {
	return EVPNVni{
		VNI:       vni,
		Type:      strings.ToUpper(strings.TrimSpace(vj.Type)),
		VxlanIf:   vj.VxlanIf,
		TenantVRF: vj.TenantVRF,
		NumMacs:   flexInt(vj.NumMacs),
		NumArpND:  flexInt(vj.NumArpND),
	}
}
