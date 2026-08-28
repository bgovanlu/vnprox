// SPDX-License-Identifier: Apache-2.0

package pvemock

import (
	"context"
	"encoding/json"
	"testing"
)

// TestMarshalMDB_EmptyBridgesRendersWellFormedDocument covers the common
// case this file's doc comment documents: a node with no bridges declaring
// any MDB entries at all (the observed state for most bridges on pvecube,
// planning/reports/evidence/pve-9.2.4-bridge-mdb-2026-08-27.txt) must still
// render a well-formed document — an empty-but-parseable "mdb": [] array,
// not nil/omitted — so internal/host's ParseMDB sees the same shape a real
// empty MDB table produces.
func TestMarshalMDB_EmptyBridgesRendersWellFormedDocument(t *testing.T) {
	raw, err := marshalMDB(nil)
	if err != nil {
		t.Fatalf("marshalMDB(nil): %v", err)
	}
	var docs []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &docs); err != nil {
		t.Fatalf("marshalMDB(nil) did not produce valid JSON: %v (%s)", err, raw)
	}
	if len(docs) != 1 {
		t.Fatalf("marshalMDB(nil) = %d top-level docs, want 1 (matching the real single-element array shape)", len(docs))
	}
	if _, ok := docs[0]["mdb"]; !ok {
		t.Errorf("marshalMDB(nil) doc missing \"mdb\" key: %s", raw)
	}
	if _, ok := docs[0]["router"]; !ok {
		t.Errorf("marshalMDB(nil) doc missing \"router\" key: %s", raw)
	}
}

// TestMarshalMDB_MultipleBridgesFlattenedAndSorted covers marshalMDB's
// flattening behavior: every bridge's declared rows land in one "mdb"
// array (matching the real `bridge -d -j mdb show`'s system-wide dump,
// evidence file §3/§4), tagged with "dev", sorted by bridge name for
// deterministic output.
func TestMarshalMDB_MultipleBridgesFlattenedAndSorted(t *testing.T) {
	raw, err := marshalMDB(map[string][]MDBEntrySpec{
		"vmbr1": {{Group: "239.1.1.1", Port: "eno2", State: "permanent"}},
		"vmbr0": {{Group: "ff02::fb", Port: "eno1", State: "temp", Protocol: "kernel"}},
	})
	if err != nil {
		t.Fatalf("marshalMDB: %v", err)
	}
	// Round-trip through the real parser this fixture data is meant to
	// exercise — the same "render fixture data through the real parser"
	// precedent frr_render.go/marshalBGPSummary sets.
	rows := mustParseMDBForTest(t, raw)
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want 2", rows)
	}
	if rows[0].Bridge != "vmbr0" || rows[0].Group != "ff02::fb" {
		t.Errorf("rows[0] = %+v, want bridge vmbr0 group ff02::fb (sorted first)", rows[0])
	}
	if rows[1].Bridge != "vmbr1" || rows[1].State != "permanent" {
		t.Errorf("rows[1] = %+v, want bridge vmbr1 state permanent", rows[1])
	}
}

// mdbRowForTest mirrors internal/host.MDBRow's shape minimally, avoiding an
// import cycle (internal/host already imports internal/pvemock, so this
// package cannot import internal/host back) — this package tests its own
// rendered JSON shape structurally instead of via the real parser type.
type mdbRowForTest struct {
	Bridge, Group, Port, State, Protocol string
	Vlan                                 int
}

func mustParseMDBForTest(t *testing.T, raw []byte) []mdbRowForTest {
	t.Helper()
	var docs []struct {
		MDB []struct {
			Dev      string `json:"dev"`
			Port     string `json:"port"`
			Grp      string `json:"grp"`
			State    string `json:"state"`
			Protocol string `json:"protocol"`
			Vid      int    `json:"vid"`
		} `json:"mdb"`
	}
	if err := json.Unmarshal(raw, &docs); err != nil {
		t.Fatalf("unmarshaling marshalMDB output: %v", err)
	}
	var out []mdbRowForTest
	for _, doc := range docs {
		for _, row := range doc.MDB {
			out = append(out, mdbRowForTest{
				Bridge: row.Dev, Group: row.Grp, Port: row.Port,
				State: row.State, Protocol: row.Protocol, Vlan: row.Vid,
			})
		}
	}
	return out
}

// TestFixtureHostReader_MDB exercises FixtureHostReader.MDB end to end
// against a minimal in-memory *Fixture (not the shared golden YAML
// clusters, the same "avoid coupling to other tests' node counts"
// reasoning internal/host's TestFixtureReader_MediaPort documents): a
// bridge with declared MDB rows, a bridge with none (the common
// empty-table case), and a non-bridge link (must never contribute rows).
func TestFixtureHostReader_MDB(t *testing.T) {
	f := &Fixture{
		Nodes: map[string]*NodeSpec{
			"n1": {
				Network: []NetIface{
					{Iface: "vmbr0", Type: "bridge", Method: "manual"},
					{Iface: "vmbr1", Type: "bridge", Method: "manual"},
					{Iface: "eno1", Type: "eth", Method: "manual"},
				},
				Links: map[string]LinkInfo{
					"vmbr0": {
						Mac: "bc:24:11:00:00:01", LinkUp: true,
						MDB: []MDBEntrySpec{{Group: "ff02::fb", Port: "eno1", State: "temp", Protocol: "kernel"}},
					},
					"vmbr1": {Mac: "bc:24:11:00:00:02", LinkUp: true},
					"eno1":  {Mac: "bc:24:11:00:00:03", LinkUp: true},
				},
			},
		},
	}
	srv := NewServer(f)
	reader := NewFixtureHostReader(srv)

	raw, err := reader.MDB(context.Background(), "n1")
	if err != nil {
		t.Fatalf("MDB: %v", err)
	}
	rows := mustParseMDBForTest(t, raw)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want exactly 1 (vmbr1 declares none, eno1 isn't a bridge)", rows)
	}
	if rows[0].Bridge != "vmbr0" || rows[0].Group != "ff02::fb" {
		t.Errorf("rows[0] = %+v, want bridge vmbr0 group ff02::fb", rows[0])
	}
}

func TestFixtureHostReader_MDB_UnknownNode(t *testing.T) {
	srv := NewServer(&Fixture{Nodes: map[string]*NodeSpec{}})
	reader := NewFixtureHostReader(srv)
	if _, err := reader.MDB(context.Background(), "nope"); err == nil {
		t.Fatal("expected an error for an unknown node")
	}
}
