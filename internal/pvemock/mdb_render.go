// SPDX-License-Identifier: Apache-2.0

package pvemock

import (
	"encoding/json"
	"sort"
)

// mdb_render.go renders a node's fixture-declared per-bridge MDBEntrySpec
// lists (T-3902) into the same JSON shape internal/host's ParseMDB parses
// from a real `bridge -d -j mdb show` — the same "fixture data rendered
// through the real parser" precedent frr_render.go set for FRR.
//
// The real command dumps every bridge on the host in one call (a
// single-element array wrapping one flattened "mdb" row list across all
// bridges — planning/reports/evidence/pve-9.2.4-bridge-mdb-2026-08-27.txt
// §3/§4), not one document per bridge, so marshalMDB mirrors that: it takes
// every bridge's declared rows keyed by bridge name and flattens them into
// one "mdb" array, tagging each row with its "dev".

// mdbRowOut is one rendered "mdb" array entry.
type mdbRowOut struct {
	Dev      string `json:"dev"`
	Port     string `json:"port,omitempty"`
	Grp      string `json:"grp"`
	State    string `json:"state,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	Vid      int    `json:"vid,omitempty"`
}

// mdbDocOut is the rendered top-level document — see mdbShowDoc in
// internal/host/mdb.go for the parser's tolerant read side of this exact
// shape.
type mdbDocOut struct {
	Router map[string]any `json:"router"`
	MDB    []mdbRowOut    `json:"mdb"`
}

// marshalMDB renders bridges (bridge name -> its fixture-declared MDB
// entries) into `bridge -d -j mdb show`'s wire shape: a single-element
// array (matching the real command's own top-level shape) whose "mdb"
// array is every bridge's rows flattened together, sorted by bridge name
// for deterministic output. An empty/nil bridges map renders an empty-but-
// well-formed document — the observed common case on a real host (see this
// file's doc comment).
func marshalMDB(bridges map[string][]MDBEntrySpec) ([]byte, error) {
	doc := mdbDocOut{MDB: []mdbRowOut{}, Router: map[string]any{}}

	var names []string
	for name := range bridges {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		for _, e := range bridges[name] {
			doc.MDB = append(doc.MDB, mdbRowOut{
				Dev: name, Port: e.Port, Grp: e.Group,
				State: e.State, Protocol: e.Protocol, Vid: e.Vlan,
			})
		}
	}
	return json.Marshal([]mdbDocOut{doc})
}
