// SPDX-License-Identifier: Apache-2.0

package host

// Open vSwitch runtime state via `ovs-vsctl` (T-407). This file holds the
// OS-neutral parsing half: decoding `ovs-vsctl -f json --columns=... list
// <table>` output (OVSDB's JSON wire format — see decodeOVSDBString and its
// siblings below for the atom-encoding rules) and joining the Bridge/Port/
// Interface tables into the OVSBridgeStatus shape callers consume. The
// exec-based half that actually invokes the `ovs-vsctl` binary (fixed argv,
// gracefully degrading when the tool is not installed) lives in
// netlink_linux.go/netlink_other.go, per this package's established
// per-platform-file convention (see Real.LLDP for the sibling exec-based
// reader this mirrors).
//
// ovs-vsctl gives no equivalent of /proc/net/bonding for an OVS bond (OVS
// bonds are not Linux-bonding-driver bonds — they're OVS's own userspace
// port-with-multiple-interfaces construct), so this is the only source for
// live OVS bond member link state. Port statistics for any interface
// (including OVS ports/bonds, which are still real Linux netdevices) are
// already covered generically by Stats() — Interface.statistics is
// surfaced here anyway since it is what "port stats" means from
// ovs-vsctl's own point of view and is fixture-testable without a live
// kernel.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// ErrOVSUnavailable indicates the `ovs-vsctl` binary is not installed (or
// not reachable at all) on this node — the graceful-degradation case (T-407
// AC4: "absent OVS tooling degrades to config-only view without errors").
// Real.OVSStatus wraps exec's "not found" failure with this sentinel so
// callers can tell "OVS never installed" apart from "installed but
// erroring" without string-matching, mirroring ErrLLDPUnavailable exactly.
var ErrOVSUnavailable = errors.New("host: ovs-vsctl not installed")

// OVSReader is host-level access to live Open vSwitch state via ovs-vsctl,
// kept as a separate interface from Reader (whose four-method shape is a
// deliberate, documented contract other tasks depend on — see Reader's doc
// comment) since OVS tooling may be entirely absent on a non-OVS
// node/host, unlike the always-available netlink/interfaces-file/LLDP/
// sysfs sources Reader's methods read. A node with no OVS bridges at all
// (the overwhelmingly common case) simply has no OVSReader-shaped data to
// offer; callers treat ErrOVSUnavailable (and, per real ovs-vsctl's own
// behavior, an empty result when the daemon is up but has no bridges
// configured) as "nothing to show," never as an error condition to
// surface to the user.
type OVSReader interface {
	// OVSStatus returns live per-bridge/port/interface OVS state for node:
	// bond mode, per-member link state, and each interface's counters.
	OVSStatus(ctx context.Context, node string) ([]OVSBridgeStatus, error)
}

// OVSBridgeStatus is one OVS bridge's live state, as `ovs-vsctl list
// Bridge/Port/Interface` reports it.
type OVSBridgeStatus struct {
	Name  string
	Ports []OVSPortStatus
}

// OVSPortStatus is one OVS port's live state — a "port" in OVS's model may
// back a single interface (a plain access/trunk port) or several (an OVS
// bond), which is why Interfaces is a slice.
type OVSPortStatus struct {
	Name       string
	BondMode   string
	Interfaces []OVSInterfaceStatus
	Trunks     []int
	Tag        int
}

// OVSInterfaceStatus is one OVS interface's live state: its type (system,
// internal, patch, ...), link state, and counters (ovs-vsctl's view of the
// same counters Stats() reads from sysfs — kept here too since it is what
// "port stats via ovs-vsctl" means from the tool's own vocabulary).
type OVSInterfaceStatus struct {
	Name      string
	Type      string
	LinkState string
	RxBytes   uint64
	TxBytes   uint64
	RxPackets uint64
	TxPackets uint64
}

// --- OVSDB JSON atom decoding ----------------------------------------------
//
// `ovs-vsctl -f json --columns=... list <table>` renders each requested
// table as {"headings":[...],"data":[[...cell per heading...],...]}. Cell
// encoding follows the OVSDB wire protocol's set/map convention: a set
// column with zero members is ["set",[]]; with exactly one member it is
// the bare atom itself (no wrapper); with two or more it is
// ["set",[atom1,atom2,...]]. A uuid atom is ["uuid","<id>"]. A map column
// is always ["map",[[k1,v1],[k2,v2],...]] (never bare, even for one pair).
// The three tables this file queries only ever need string/int scalars,
// uuid sets, an int set (trunks), and a string map (statistics), so the
// decoders below cover exactly those four shapes rather than a fully
// general OVSDB value type.

// ovsdbTable is the raw `list` output shape.
type ovsdbTable struct {
	Headings []string            `json:"headings"`
	Data     [][]json.RawMessage `json:"data"`
}

func parseOVSDBTable(data []byte) (*ovsdbTable, error) {
	var t ovsdbTable
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("host: ovs-vsctl: parsing table json: %w", err)
	}
	return &t, nil
}

// wrapped decodes a ["tag", value] pair (the "set"/"map"/"uuid" wrapper
// shape); ok is false for a bare (unwrapped) atom.
func wrapped(raw json.RawMessage) (tag string, value json.RawMessage, ok bool) {
	var pair []json.RawMessage
	if err := json.Unmarshal(raw, &pair); err != nil || len(pair) != 2 {
		return "", nil, false
	}
	if err := json.Unmarshal(pair[0], &tag); err != nil {
		return "", nil, false
	}
	switch tag {
	case "set", "map", "uuid":
		return tag, pair[1], true
	default:
		return "", nil, false
	}
}

// decodeOVSDBString decodes an optional-string column: a bare JSON string,
// or ["set",[]] for "not set" (rendered as "").
func decodeOVSDBString(raw json.RawMessage) (string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	if tag, _, ok := wrapped(raw); ok && tag == "set" {
		return "", nil
	}
	return "", fmt.Errorf("host: ovs-vsctl: unexpected string cell %s", raw)
}

// decodeOVSDBInt decodes an optional-integer column (e.g. Port.tag): a bare
// JSON number, or ["set",[]] for "not set" (rendered as 0).
func decodeOVSDBInt(raw json.RawMessage) (int, error) {
	var n float64
	if err := json.Unmarshal(raw, &n); err == nil {
		return int(n), nil
	}
	if tag, _, ok := wrapped(raw); ok && tag == "set" {
		return 0, nil
	}
	return 0, fmt.Errorf("host: ovs-vsctl: unexpected int cell %s", raw)
}

// decodeOVSDBIntSet decodes an integer-set column (Port.trunks): "not set"/
// empty is nil, a single member is the bare number, 2+ members are
// ["set",[...]].
func decodeOVSDBIntSet(raw json.RawMessage) ([]int, error) {
	var n float64
	if err := json.Unmarshal(raw, &n); err == nil {
		return []int{int(n)}, nil
	}
	tag, value, ok := wrapped(raw)
	if !ok || tag != "set" {
		return nil, fmt.Errorf("host: ovs-vsctl: unexpected int-set cell %s", raw)
	}
	var members []json.RawMessage
	if err := json.Unmarshal(value, &members); err != nil {
		return nil, fmt.Errorf("host: ovs-vsctl: decoding int-set members: %w", err)
	}
	if len(members) == 0 {
		return nil, nil
	}
	out := make([]int, 0, len(members))
	for _, m := range members {
		var v float64
		if err := json.Unmarshal(m, &v); err != nil {
			return nil, fmt.Errorf("host: ovs-vsctl: decoding int-set member: %w", err)
		}
		out = append(out, int(v))
	}
	return out, nil
}

// decodeOVSDBUUID decodes a single uuid atom: ["uuid","<id>"].
func decodeOVSDBUUID(raw json.RawMessage) (string, error) {
	tag, value, ok := wrapped(raw)
	if !ok || tag != "uuid" {
		return "", fmt.Errorf("host: ovs-vsctl: unexpected uuid cell %s", raw)
	}
	var id string
	if err := json.Unmarshal(value, &id); err != nil {
		return "", fmt.Errorf("host: ovs-vsctl: decoding uuid value: %w", err)
	}
	return id, nil
}

// decodeOVSDBUUIDSet decodes a uuid-set column (Bridge.ports, Port.interfaces):
// "not set"/empty is nil, a single member is the bare ["uuid",id] atom, 2+
// members are ["set",[["uuid",id1],["uuid",id2],...]].
func decodeOVSDBUUIDSet(raw json.RawMessage) ([]string, error) {
	if id, err := decodeOVSDBUUID(raw); err == nil {
		return []string{id}, nil
	}
	tag, value, ok := wrapped(raw)
	if !ok || tag != "set" {
		return nil, fmt.Errorf("host: ovs-vsctl: unexpected uuid-set cell %s", raw)
	}
	var members []json.RawMessage
	if err := json.Unmarshal(value, &members); err != nil {
		return nil, fmt.Errorf("host: ovs-vsctl: decoding uuid-set members: %w", err)
	}
	out := make([]string, 0, len(members))
	for _, m := range members {
		id, err := decodeOVSDBUUID(m)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

// decodeOVSDBStringMap decodes a map column (Interface.statistics):
// ["map",[[k1,v1],[k2,v2],...]].
func decodeOVSDBStringMap(raw json.RawMessage) (map[string]string, error) {
	tag, value, ok := wrapped(raw)
	if !ok || tag != "map" {
		return nil, fmt.Errorf("host: ovs-vsctl: unexpected map cell %s", raw)
	}
	var pairs [][]json.RawMessage
	if err := json.Unmarshal(value, &pairs); err != nil {
		return nil, fmt.Errorf("host: ovs-vsctl: decoding map pairs: %w", err)
	}
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		if len(p) != 2 {
			return nil, fmt.Errorf("host: ovs-vsctl: map pair with %d elements, want 2", len(p))
		}
		var k string
		if err := json.Unmarshal(p[0], &k); err != nil {
			return nil, fmt.Errorf("host: ovs-vsctl: decoding map key: %w", err)
		}
		var v string
		// Statistics values are OVSDB integers on the wire; re-encode as a
		// decimal string so callers can strconv.ParseUint uniformly
		// regardless of whether the source used a string or number atom.
		var n float64
		if err := json.Unmarshal(p[1], &n); err == nil {
			v = strconv.FormatInt(int64(n), 10)
		} else if err := json.Unmarshal(p[1], &v); err != nil {
			return nil, fmt.Errorf("host: ovs-vsctl: decoding map value: %w", err)
		}
		out[k] = v
	}
	return out, nil
}

// --- table row shapes and the Bridge/Port/Interface join -------------------

// ovsBridgeColumns/ovsPortColumns/ovsInterfaceColumns are the fixed
// --columns argv this reader always requests, in the exact order the row
// decoders below expect — "fixed argv" per the task card, not
// user-influenced in any way.
var (
	ovsBridgeColumns    = []string{"name", "ports"}
	ovsPortColumns      = []string{"_uuid", "name", "tag", "trunks", "bond_mode", "interfaces"}
	ovsInterfaceColumns = []string{"_uuid", "name", "type", "link_state", "statistics"}
)

type ovsBridgeRow struct {
	Name      string
	PortUUIDs []string
}

type ovsPortRow struct {
	UUID       string
	Name       string
	BondMode   string
	Trunks     []int
	IfaceUUIDs []string
	Tag        int
}

type ovsInterfaceRow struct {
	Statistics map[string]string
	UUID       string
	Name       string
	Type       string
	LinkState  string
}

func parseOVSBridgeTable(data []byte) ([]ovsBridgeRow, error) {
	t, err := parseOVSDBTable(data)
	if err != nil {
		return nil, err
	}
	out := make([]ovsBridgeRow, 0, len(t.Data))
	for i, row := range t.Data {
		if len(row) != len(ovsBridgeColumns) {
			return nil, fmt.Errorf("host: ovs-vsctl: bridge row %d has %d column(s), want %d", i, len(row), len(ovsBridgeColumns))
		}
		name, err := decodeOVSDBString(row[0])
		if err != nil {
			return nil, fmt.Errorf("host: ovs-vsctl: bridge row %d name: %w", i, err)
		}
		ports, err := decodeOVSDBUUIDSet(row[1])
		if err != nil {
			return nil, fmt.Errorf("host: ovs-vsctl: bridge row %d ports: %w", i, err)
		}
		out = append(out, ovsBridgeRow{Name: name, PortUUIDs: ports})
	}
	return out, nil
}

func parseOVSPortTable(data []byte) ([]ovsPortRow, error) {
	t, err := parseOVSDBTable(data)
	if err != nil {
		return nil, err
	}
	out := make([]ovsPortRow, 0, len(t.Data))
	for i, row := range t.Data {
		if len(row) != len(ovsPortColumns) {
			return nil, fmt.Errorf("host: ovs-vsctl: port row %d has %d column(s), want %d", i, len(row), len(ovsPortColumns))
		}
		uuid, err := decodeOVSDBUUID(row[0])
		if err != nil {
			return nil, fmt.Errorf("host: ovs-vsctl: port row %d uuid: %w", i, err)
		}
		name, err := decodeOVSDBString(row[1])
		if err != nil {
			return nil, fmt.Errorf("host: ovs-vsctl: port row %d name: %w", i, err)
		}
		tag, err := decodeOVSDBInt(row[2])
		if err != nil {
			return nil, fmt.Errorf("host: ovs-vsctl: port row %d tag: %w", i, err)
		}
		trunks, err := decodeOVSDBIntSet(row[3])
		if err != nil {
			return nil, fmt.Errorf("host: ovs-vsctl: port row %d trunks: %w", i, err)
		}
		bondMode, err := decodeOVSDBString(row[4])
		if err != nil {
			return nil, fmt.Errorf("host: ovs-vsctl: port row %d bond_mode: %w", i, err)
		}
		ifaces, err := decodeOVSDBUUIDSet(row[5])
		if err != nil {
			return nil, fmt.Errorf("host: ovs-vsctl: port row %d interfaces: %w", i, err)
		}
		out = append(out, ovsPortRow{UUID: uuid, Name: name, Tag: tag, Trunks: trunks, BondMode: bondMode, IfaceUUIDs: ifaces})
	}
	return out, nil
}

func parseOVSInterfaceTable(data []byte) ([]ovsInterfaceRow, error) {
	t, err := parseOVSDBTable(data)
	if err != nil {
		return nil, err
	}
	out := make([]ovsInterfaceRow, 0, len(t.Data))
	for i, row := range t.Data {
		if len(row) != len(ovsInterfaceColumns) {
			return nil, fmt.Errorf("host: ovs-vsctl: interface row %d has %d column(s), want %d", i, len(row), len(ovsInterfaceColumns))
		}
		uuid, err := decodeOVSDBUUID(row[0])
		if err != nil {
			return nil, fmt.Errorf("host: ovs-vsctl: interface row %d uuid: %w", i, err)
		}
		name, err := decodeOVSDBString(row[1])
		if err != nil {
			return nil, fmt.Errorf("host: ovs-vsctl: interface row %d name: %w", i, err)
		}
		typ, err := decodeOVSDBString(row[2])
		if err != nil {
			return nil, fmt.Errorf("host: ovs-vsctl: interface row %d type: %w", i, err)
		}
		linkState, err := decodeOVSDBString(row[3])
		if err != nil {
			return nil, fmt.Errorf("host: ovs-vsctl: interface row %d link_state: %w", i, err)
		}
		stats, err := decodeOVSDBStringMap(row[4])
		if err != nil {
			return nil, fmt.Errorf("host: ovs-vsctl: interface row %d statistics: %w", i, err)
		}
		out = append(out, ovsInterfaceRow{UUID: uuid, Name: name, Type: typ, LinkState: linkState, Statistics: stats})
	}
	return out, nil
}

// BuildOVSBridgeStatus joins the three `ovs-vsctl list` table responses
// (Bridge, Port, Interface — in that order, each the exact JSON bytes one
// `ovs-vsctl -f json --columns=... list <table>` invocation returns) into
// the OVSBridgeStatus tree callers consume. A Bridge/Port referencing a
// UUID absent from the corresponding table (a benign race between the
// three separate invocations — OVS state can change between them) is
// skipped rather than treated as an error.
func BuildOVSBridgeStatus(bridgeJSON, portJSON, ifaceJSON []byte) ([]OVSBridgeStatus, error) {
	bridges, err := parseOVSBridgeTable(bridgeJSON)
	if err != nil {
		return nil, err
	}
	ports, err := parseOVSPortTable(portJSON)
	if err != nil {
		return nil, err
	}
	ifaces, err := parseOVSInterfaceTable(ifaceJSON)
	if err != nil {
		return nil, err
	}

	ifaceByUUID := make(map[string]ovsInterfaceRow, len(ifaces))
	for _, r := range ifaces {
		ifaceByUUID[r.UUID] = r
	}
	portByUUID := make(map[string]ovsPortRow, len(ports))
	for _, r := range ports {
		portByUUID[r.UUID] = r
	}

	out := make([]OVSBridgeStatus, 0, len(bridges))
	for _, br := range bridges {
		bs := OVSBridgeStatus{Name: br.Name}
		for _, puuid := range br.PortUUIDs {
			pr, ok := portByUUID[puuid]
			if !ok {
				continue
			}
			ps := OVSPortStatus{Name: pr.Name, Tag: pr.Tag, Trunks: pr.Trunks, BondMode: pr.BondMode}
			for _, iuuid := range pr.IfaceUUIDs {
				ir, ok := ifaceByUUID[iuuid]
				if !ok {
					continue
				}
				is := OVSInterfaceStatus{Name: ir.Name, Type: ir.Type, LinkState: ir.LinkState}
				is.RxBytes = statUint(ir.Statistics, "rx_bytes")
				is.TxBytes = statUint(ir.Statistics, "tx_bytes")
				is.RxPackets = statUint(ir.Statistics, "rx_packets")
				is.TxPackets = statUint(ir.Statistics, "tx_packets")
				ps.Interfaces = append(ps.Interfaces, is)
			}
			bs.Ports = append(bs.Ports, ps)
		}
		out = append(out, bs)
	}
	return out, nil
}

func statUint(stats map[string]string, key string) uint64 {
	v, err := strconv.ParseUint(stats[key], 10, 64)
	if err != nil {
		return 0
	}
	return v
}
