// SPDX-License-Identifier: Apache-2.0

// ports.go implements docs/features/lldp-discovery.md §2's Ports view: "a
// flat table (node, NIC, switch, port, speed, PVID, tagged VLANs, last
// seen) — exportable CSV; this alone replaces most wiring spreadsheets."

package topology

import (
	"bytes"
	"encoding/csv"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// PortRow is one row of the Ports table: one PhysNic<->LldpNeighbor
// observation. Unlike the map (which drops an entry past the 10-minute
// staleness threshold, switches.go), the table keeps every neighbor the
// graph still holds and instead marks it Stale — spec §3: "kept in the
// table with a 'stale' tag for troubleshooting unplugged links."
type PortRow struct {
	Node        string `json:"node"`
	NIC         string `json:"nic"`
	Switch      string `json:"switch"`
	Port        string `json:"port"`
	SpeedDescr  string `json:"speedDescr,omitempty"`
	TaggedVLANs []int  `json:"taggedVlans,omitempty"`
	SpeedMbps   int    `json:"speedMbps,omitempty"`
	PVID        int    `json:"pvid,omitempty"`
	LastSeen    int64  `json:"lastSeen,omitempty"`
	Stale       bool   `json:"stale"`
}

// Ports builds the flat ports table from every LldpNeighbor entity in snap,
// evaluated at now (staleness only, per PortRow's doc comment — the table
// never drops rows the way the map does).
func Ports(snap inventory.Snapshot, now time.Time) []PortRow {
	var rows []PortRow
	for _, e := range snap.All() {
		n, ok := e.(*inventory.LldpNeighbor)
		if !ok {
			continue
		}
		sw := n.ChassisName
		if sw == "" {
			sw = n.ChassisID
		}
		rows = append(rows, PortRow{
			Node: n.Node, NIC: n.LocalIface, Switch: sw, Port: n.PortID,
			SpeedMbps: n.SpeedMbps, SpeedDescr: n.SpeedDescr,
			PVID: n.VLAN, TaggedVLANs: append([]int(nil), n.TaggedVLANs...),
			LastSeen: n.LastSeen,
			Stale:    lldpDropped(n, now) || lldpNeighborStatus(n, now) == StatusUnknown,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Node != rows[j].Node {
			return rows[i].Node < rows[j].Node
		}
		return rows[i].NIC < rows[j].NIC
	})
	return rows
}

// portsCSVHeader is the fixed column order for PortsCSV — node, NIC,
// switch, port, speed, PVID, tagged VLANs, last seen, per spec §2, plus a
// trailing "stale" column surfacing §3's troubleshooting tag.
var portsCSVHeader = []string{"node", "nic", "switch", "port", "speedMbps", "pvid", "taggedVlans", "lastSeen", "stale"}

// PortsCSV renders rows as CSV (RFC 4180 via encoding/csv), header first —
// docs/features/lldp-discovery.md §2's "exportable CSV". lastSeen is
// rendered as unix seconds (matching docs/api.md's timestamp convention),
// left blank when never observed.
func PortsCSV(rows []PortRow) string {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write(portsCSVHeader)
	for _, r := range rows {
		tagged := make([]string, len(r.TaggedVLANs))
		for i, v := range r.TaggedVLANs {
			tagged[i] = strconv.Itoa(v)
		}
		lastSeen := ""
		if r.LastSeen != 0 {
			lastSeen = strconv.FormatInt(r.LastSeen, 10)
		}
		speed := ""
		if r.SpeedMbps != 0 {
			speed = strconv.Itoa(r.SpeedMbps)
		}
		pvid := ""
		if r.PVID != 0 {
			pvid = strconv.Itoa(r.PVID)
		}
		_ = w.Write([]string{
			r.Node, r.NIC, r.Switch, r.Port, speed, pvid,
			strings.Join(tagged, ";"), lastSeen, strconv.FormatBool(r.Stale),
		})
	}
	w.Flush()
	return buf.String()
}
