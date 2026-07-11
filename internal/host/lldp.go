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

// ErrLLDPUnavailable indicates lldpd's lldpctl binary is not installed (or
// not reachable at all) on this node — docs/features/lldp-discovery.md §1's
// "if lldpd is absent: feature degrades gracefully" case, as distinct from a
// transient/parse failure. Real.LLDP wraps exec's "not found" failure with
// this sentinel so callers (the collector, eventually the API layer) can
// tell "never installed" apart from "installed but erroring" without
// string-matching an error message.
var ErrLLDPUnavailable = errors.New("host: lldpd/lldpctl not installed")

// LLDPNeighbor is one LLDP- (or CDP-, via lldpd's built-in CDP decoding)
// discovered neighbor on a local interface, parsed from `lldpctl -f json`
// (or an equivalent raw JSON source — see ParseLLDP) into the full field set
// docs/features/lldp-discovery.md §1 documents: "chassis name/ID, port
// ID/description, mgmt address, advertised VLANs (PVID + tagged), MAU/speed,
// TTL. CDP neighbors appear too (lldpd decodes CDP)."
type LLDPNeighbor struct {
	PortID        string
	PortIDType    string
	Age           string
	ChassisID     string
	ChassisIDType string
	ChassisName   string
	Protocol      string
	ChassisDescr  string
	LocalIface    string
	SpeedDescr    string
	PortDescr     string
	TaggedVLANs   []int
	MgmtIPs       []string
	PVID          int
	SpeedMbps     int
	TTL           int
}

// ParseLLDP defensively parses raw LLDP neighbor JSON (host.Reader.LLDP's
// output) into a neighbor list. Two shapes are recognized, auto-detected
// from the top-level JSON value:
//
//   - A JSON object: lldpd's own `lldpctl -f json` schema,
//     {"lldp":[{"interface":[{"name","via","age","chassis":{"<name>":{...}},
//     "port":{...}}]}]}. This is real lldpctl output's documented shape —
//     lldpd is notoriously inconsistent about collapsing single-item lists
//     to a bare object instead of a one-element array, so every list-typed
//     field here (top-level "lldp", "interface", chassis "mgmt-ip", port
//     "vlan") is parsed leniently as either form.
//   - A JSON array: the flat simplified shape internal/pvemock's fixture
//     LLDP data (and this package's FixtureReader, via
//     pvemock.FixtureHostReader) renders — one object per neighbor with
//     lowercase snake_case keys (local-iface, chassis_name, chassis_id,
//     port_id, port_descr, mgmt_ip, vlan, ttl). Recognized for fixture/test
//     compatibility; production Real.LLDP only ever produces the object
//     shape.
//
// Malformed, truncated, or adversarial input never panics: parsing errors
// (including any unexpected internal panic, guarded by a recover as
// defense in depth) are returned as an error, and a malformed individual
// neighbor entry within an otherwise-parseable document is skipped rather
// than failing the whole parse.
func ParseLLDP(raw []byte) (neighbors []LLDPNeighbor, err error) {
	defer func() {
		if r := recover(); r != nil {
			neighbors, err = nil, fmt.Errorf("host: lldp: parser panic recovered: %v", r)
		}
	}()

	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, nil
	}
	switch trimmed[0] {
	case '{':
		return parseNestedLLDP(trimmed)
	case '[':
		return parseFlatLLDP(trimmed)
	default:
		return nil, fmt.Errorf("host: lldp: unrecognized top-level JSON value (want object or array)")
	}
}

// --- real lldpctl -f json schema ------------------------------------------

type lldpRootJSON struct {
	LLDP json.RawMessage `json:"lldp"`
}

type lldpBlockJSON struct {
	Interface json.RawMessage `json:"interface"`
}

type interfaceJSON struct {
	Port    portJSON        `json:"port"`
	Name    string          `json:"name"`
	Via     string          `json:"via"`
	Age     string          `json:"age"`
	Chassis json.RawMessage `json:"chassis"`
}

type chassisDetailJSON struct {
	ID struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"id"`
	Name   string          `json:"name"`
	Descr  string          `json:"descr"`
	MgmtIP json.RawMessage `json:"mgmt-ip"`
}

type mgmtIPJSON struct {
	Value string `json:"value"`
}

type portJSON struct {
	AutoNeg *autoNegJSON `json:"auto-negotiation"`
	ID      struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"id"`
	Descr string          `json:"descr"`
	TTL   json.RawMessage `json:"ttl"`
	VLAN  json.RawMessage `json:"vlan"`
}

type autoNegJSON struct {
	Current string `json:"current"`
	Support bool   `json:"support"`
	Enabled bool   `json:"enabled"`
}

type vlanEntryJSON struct {
	Label  string          `json:"label"`
	VlanID json.RawMessage `json:"vlan-id"`
	PVID   bool            `json:"pvid"`
}

// parseNestedLLDP parses the real lldpctl -f json schema.
func parseNestedLLDP(raw []byte) ([]LLDPNeighbor, error) {
	var root lldpRootJSON
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("host: lldp: parsing root object: %w", err)
	}
	blocks, err := flexUnmarshal[lldpBlockJSON](root.LLDP)
	if err != nil {
		return nil, fmt.Errorf("host: lldp: parsing lldp blocks: %w", err)
	}

	var out []LLDPNeighbor
	for _, block := range blocks {
		items := rawItems(block.Interface)
		for _, item := range items {
			var iface interfaceJSON
			if err := json.Unmarshal(item, &iface); err != nil {
				// Defensive: one malformed neighbor entry does not sink
				// the rest of the document.
				continue
			}
			n, ok := neighborFromInterface(iface)
			if ok {
				out = append(out, n)
			}
		}
	}
	return out, nil
}

func neighborFromInterface(iface interfaceJSON) (LLDPNeighbor, bool) {
	if iface.Name == "" {
		return LLDPNeighbor{}, false
	}
	n := LLDPNeighbor{
		LocalIface: iface.Name,
		Protocol:   iface.Via,
		Age:        iface.Age,
	}
	if n.Protocol == "" {
		n.Protocol = "LLDP"
	}

	chassisName, chassis := firstChassis(iface.Chassis)
	n.ChassisID = chassis.ID.Value
	n.ChassisIDType = chassis.ID.Type
	n.ChassisName = chassis.Name
	if n.ChassisName == "" {
		n.ChassisName = chassisName
	}
	n.ChassisDescr = chassis.Descr
	n.MgmtIPs = flexMgmtIPs(chassis.MgmtIP)

	n.PortID = iface.Port.ID.Value
	n.PortIDType = iface.Port.ID.Type
	n.PortDescr = iface.Port.Descr
	n.TTL = flexInt(iface.Port.TTL)
	n.PVID, n.TaggedVLANs = flexVlans(iface.Port.VLAN)
	if iface.Port.AutoNeg != nil {
		n.SpeedDescr = iface.Port.AutoNeg.Current
		n.SpeedMbps = parseMauSpeedMbps(n.SpeedDescr)
	}

	if n.ChassisID == "" && n.PortID == "" {
		// Nothing identifying about this entry at all — not a usable
		// neighbor observation.
		return LLDPNeighbor{}, false
	}
	return n, true
}

// firstChassis extracts the sole (by lldpd convention, always exactly one)
// entry of the chassis map, keyed by chassis name. Deterministic (sorted by
// key) in the pathological case of more than one key, so parsing the same
// bytes twice never yields a different result.
func firstChassis(raw json.RawMessage) (name string, detail chassisDetailJSON) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return "", chassisDetailJSON{}
	}
	var m map[string]chassisDetailJSON
	if err := json.Unmarshal(raw, &m); err != nil || len(m) == 0 {
		return "", chassisDetailJSON{}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys[0], m[keys[0]]
}

func flexMgmtIPs(raw json.RawMessage) []string {
	items := rawItems(raw)
	var out []string
	for _, item := range items {
		var s string
		if err := json.Unmarshal(item, &s); err == nil && s != "" {
			out = append(out, s)
			continue
		}
		var obj mgmtIPJSON
		if err := json.Unmarshal(item, &obj); err == nil && obj.Value != "" {
			out = append(out, obj.Value)
		}
	}
	return out
}

func flexVlans(raw json.RawMessage) (pvid int, tagged []int) {
	items := rawItems(raw)
	for _, item := range items {
		var v vlanEntryJSON
		if err := json.Unmarshal(item, &v); err != nil {
			continue
		}
		id := flexInt(v.VlanID)
		if id <= 0 {
			continue
		}
		if v.PVID {
			pvid = id
			continue
		}
		tagged = append(tagged, id)
	}
	sort.Ints(tagged)
	return pvid, tagged
}

// flexInt tolerates lldpd's inconsistent numeric encodings: a bare JSON
// number, a numeric string ("120"), or an object wrapper ({"value":"120"}).
// Returns 0 for anything it cannot interpret, never an error — every caller
// treats 0 as "not reported".
func flexInt(raw json.RawMessage) int {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return 0
	}
	var n int
	if err := json.Unmarshal(raw, &n); err == nil {
		return n
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			return v
		}
		return 0
	}
	var obj struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && len(obj.Value) > 0 && !bytes.Equal(obj.Value, raw) {
		return flexInt(obj.Value)
	}
	return 0
}

// rawItems normalizes a JSON value that lldpd may emit as either an array
// or, when it holds exactly one item, a bare object, into a slice of raw
// per-item messages. Returns nil for null/empty/unparseable input rather
// than erroring — callers skip what they cannot use.
func rawItems(raw json.RawMessage) []json.RawMessage {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}
	// Not an array: treat as a single item — lldpd collapses a one-element
	// list to a bare value for several fields (an object for "interface"/
	// chassis entries, but a bare *string* for a single "mgmt-ip"). Any
	// syntactically valid single JSON value is accepted here; a value that
	// doesn't fit what the caller's per-item unmarshal expects is simply
	// dropped there, not here.
	var probe any
	if json.Unmarshal(raw, &probe) == nil {
		return []json.RawMessage{raw}
	}
	return nil
}

// flexUnmarshal is rawItems plus per-item strict decoding into T, silently
// dropping items that don't decode as T.
func flexUnmarshal[T any](raw json.RawMessage) ([]T, error) {
	items := rawItems(raw)
	out := make([]T, 0, len(items))
	for _, item := range items {
		var v T
		if err := json.Unmarshal(item, &v); err != nil {
			continue
		}
		out = append(out, v)
	}
	return out, nil
}

// parseMauSpeedMbps best-effort extracts a numeric Mbps figure from an
// lldpd MAU/auto-negotiation "current" description string, e.g.
// "1000BaseTFD - Four-pair Category 5 UTP, full duplex mode" -> 1000,
// "10GigBaseT - ..." -> 10000. Returns 0 when the leading token doesn't
// match a recognized MAU speed prefix — the verbatim SpeedDescr string is
// always retained regardless, so no information is lost.
func parseMauSpeedMbps(descr string) int {
	descr = strings.TrimSpace(descr)
	switch {
	case strings.HasPrefix(descr, "100Gig"), strings.HasPrefix(descr, "100000"):
		return 100000
	case strings.HasPrefix(descr, "40Gig"):
		return 40000
	case strings.HasPrefix(descr, "25Gig"):
		return 25000
	case strings.HasPrefix(descr, "10Gig"), strings.HasPrefix(descr, "10GBase"):
		return 10000
	case strings.HasPrefix(descr, "2500Base"):
		return 2500
	case strings.HasPrefix(descr, "1000Base"):
		return 1000
	case strings.HasPrefix(descr, "100Base"):
		return 100
	case strings.HasPrefix(descr, "10Base"):
		return 10
	default:
		return 0
	}
}

// --- flat simplified fixture schema ---------------------------------------

// flatNeighborJSON mirrors internal/pvemock's marshalLLDP output and
// inventory's pre-T-302 flat parsing (kept for fixture/back-compat — see
// ParseLLDP's doc comment).
type flatNeighborJSON struct {
	Local       string `json:"local-iface"`
	ChassisName string `json:"chassis_name"`
	ChassisID   string `json:"chassis_id"`
	PortID      string `json:"port_id"`
	PortDescr   string `json:"port_descr"`
	MgmtIP      string `json:"mgmt_ip"`
	VLAN        int    `json:"vlan"`
	TTL         int    `json:"ttl"`
}

func parseFlatLLDP(raw []byte) ([]LLDPNeighbor, error) {
	var rows []json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("host: lldp: parsing flat array: %w", err)
	}
	out := make([]LLDPNeighbor, 0, len(rows))
	for _, row := range rows {
		var r flatNeighborJSON
		if err := json.Unmarshal(row, &r); err != nil {
			continue
		}
		if r.Local == "" {
			continue
		}
		n := LLDPNeighbor{
			LocalIface:  r.Local,
			Protocol:    "LLDP",
			ChassisName: r.ChassisName,
			ChassisID:   r.ChassisID,
			PortID:      r.PortID,
			PortDescr:   r.PortDescr,
			PVID:        r.VLAN,
			TTL:         r.TTL,
		}
		if r.MgmtIP != "" {
			n.MgmtIPs = []string{r.MgmtIP}
		}
		out = append(out, n)
	}
	return out, nil
}
