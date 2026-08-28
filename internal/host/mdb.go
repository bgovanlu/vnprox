// SPDX-License-Identifier: Apache-2.0

// mdb.go implements T-3902's bridge multicast-forwarding-database (MDB)
// browser support: parsing `bridge -d -j mdb show` output into typed Go
// values. Like frr.go's ParseBGPSummary/ParseEVPNVNI, this is a pure
// function over already-fetched bytes (Real fetches them via exec in
// netlink_linux.go; FixtureReader delegates to pvemock) so both production
// and fixture data flow through one parser.
//
// Unlike FDB (fdb.go's FlattenFDB, which flattens per-bridge tables already
// embedded in Links()' BridgeDetail by netlink), there is no MDB dump
// anywhere in github.com/vishvananda/netlink v1.3.1 — confirmed absent by
// grepping that module's source for any MDB-shaped netlink message type —
// so this package cannot get MDB rows from the same netlink.NeighList call
// FDB uses. `bridge -d -j mdb show` is the only read path, hence the
// exec-then-parse split this file follows instead.
//
// The wire shape and every field/value documented below is grounded in
// planning/reports/evidence/pve-9.2.4-bridge-mdb-2026-08-27.txt, captured
// read-only against pvecube (PVE 9.2.4, iproute2-6.15.0) on 2026-08-27 —
// per CLAUDE.md's rule, never modeled from docs or invented. That host's
// MDB table was NOT empty (four real entries, all IPv6 mDNS ff02::fb,
// state "temp", protocol "kernel"), which is itself useful grounding, but
// the observed population is narrow: no "permanent" entry, no IPv4 group,
// no VLAN-tagged row, no non-empty "flags" array, and no populated
// "router" map were observed. This parser stays permissive (plain strings
// for State/Protocol, not closed enums) precisely because of those gaps —
// see the evidence file's closing section for the full list of what was
// and wasn't observed.

package host

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// ErrMDBUnavailable indicates the `bridge` binary (iproute2) is not
// installed or not reachable at all on this node. Uncommon in practice —
// iproute2 ships with every Debian/PVE install, and PVE's own networking
// depends on it — but handled with the same defensive exec.ErrNotFound
// detection every other exec-based reader in this package (LLDP, FRR,
// corosync, OVS) uses, rather than assuming the binary is always present.
var ErrMDBUnavailable = errors.New("host: bridge (iproute2) not installed")

// MDBRow is one bridge multicast forwarding-database entry, flattened and
// bridge-tagged — the same row shape (group/port/vlan tagged by a bridge
// name) FDBRow/FlattenFDB establish for the MAC/FDB browser, adapted for
// multicast: Group replaces Mac, and State/Protocol carry `bridge`'s own
// entry-origin vocabulary (evidence: only "temp"/"kernel" observed on
// pvecube — see this file's doc comment).
type MDBRow struct {
	Bridge string
	Group  string
	Port   string
	// State is the entry's MDB state as `bridge` reports it: "temp"
	// (dynamically learned via snooping, ages out) or "permanent"
	// (statically added, e.g. `bridge mdb add ... permanent` — never
	// observed on pvecube, taken from `bridge`'s own documented
	// vocabulary, not fabricated).
	State string
	// Protocol is the entry's originating protocol ("kernel" for
	// snooping-learned entries — the only value observed; a routing
	// daemon such as PIM/mrouted-injected static entries would show a
	// different value, UNVERIFIED against real output).
	Protocol string
	// Vlan is 0 when the entry carries no VLAN tag — the observed case on
	// pvecube, whose bridges are not VLAN-filtering (evidence file §4).
	// The JSON key `bridge -d -j mdb show` uses for a VLAN-tagged entry
	// was not observed; parseMDBDoc tolerates either "vid" or "vlan" as a
	// defensive guess, not a verified fact.
	Vlan int
}

// mdbShowDoc is the top-level shape of `bridge -d -j mdb show`: a
// single-element array (observed on pvecube) whose element carries the
// flattened "mdb" row list and a "router" map of declared multicast-router
// ports — always empty {} on pvecube (evidence file §3/§4: nothing on that
// host is a *declared* router port, only auto-learn mode is enabled). The
// shape of a populated "router" map is UNVERIFIED against real output, so
// it is decoded as raw JSON and not otherwise interpreted.
type mdbShowDoc struct {
	Router map[string]json.RawMessage `json:"router"`
	MDB    []mdbRowJSON               `json:"mdb"`
}

// mdbRowJSON is the tolerant wire shape of one "mdb" array entry. Vid/Vlan
// both accept a VLAN tag under either key name — see MDBRow.Vlan's doc
// comment for why this is a defensive guess rather than a verified fact.
type mdbRowJSON struct {
	Vid      *int   `json:"vid"`
	Vlan     *int   `json:"vlan"`
	Dev      string `json:"dev"`
	Port     string `json:"port"`
	Grp      string `json:"grp"`
	State    string `json:"state"`
	Protocol string `json:"protocol"`
}

// ParseMDB parses `bridge -d -j mdb show` output into a flat, bridge-tagged
// row list, sorted for deterministic output (by bridge, then group, then
// port). Malformed, truncated, or adversarial input never panics: any
// unexpected internal panic is recovered and returned as an error
// (matching ParseBGPSummary's convention). Empty input returns (nil, nil)
// — "no output" is not itself an error, and an empty MDB table is the
// common case this file's doc comment documents as directly observed on a
// real PVE 9.2.4 host for most bridges.
func ParseMDB(raw []byte) (rows []MDBRow, err error) {
	defer func() {
		if r := recover(); r != nil {
			rows, err = nil, fmt.Errorf("host: mdb: parser panic recovered: %v", r)
		}
	}()

	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, nil
	}

	var docs []mdbShowDoc
	if err := json.Unmarshal(trimmed, &docs); err != nil {
		return nil, fmt.Errorf("host: mdb: parsing bridge mdb show output: %w", err)
	}

	for _, doc := range docs {
		for _, row := range doc.MDB {
			if row.Dev == "" && row.Grp == "" {
				continue // defensive: skip a wholly empty entry
			}
			vlan := 0
			switch {
			case row.Vid != nil:
				vlan = *row.Vid
			case row.Vlan != nil:
				vlan = *row.Vlan
			}
			rows = append(rows, MDBRow{
				Bridge:   row.Dev,
				Group:    row.Grp,
				Port:     row.Port,
				State:    row.State,
				Protocol: row.Protocol,
				Vlan:     vlan,
			})
		}
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Bridge != rows[j].Bridge {
			return rows[i].Bridge < rows[j].Bridge
		}
		if rows[i].Group != rows[j].Group {
			return rows[i].Group < rows[j].Group
		}
		return rows[i].Port < rows[j].Port
	})
	return rows, nil
}
