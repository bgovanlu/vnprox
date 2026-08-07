package topology_test

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pvemock"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// TestDetail covers GET /inventory/{ref}'s underlying projection: resolved
// fields, provenance, and related (edge-linked) entities.
func TestDetail(t *testing.T) {
	graph, _, _ := buildGraph(t, fixtureSingleNode)
	snap := graph.Snapshot()

	t.Run("not found", func(t *testing.T) {
		_, ok := topology.Detail(snap, inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "does-not-exist"})
		if ok {
			t.Error("expected ok=false for an unknown ref")
		}
	})

	t.Run("physnic", func(t *testing.T) {
		ref := inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno1"}
		d, ok := topology.Detail(snap, ref)
		if !ok {
			t.Fatal("expected ok=true for eno1")
		}
		if d.Ref != ref.String() || d.Kind != "physnic" || d.Node != "pve1" {
			t.Errorf("detail identity = %+v, want ref/kind/node for %s", d, ref)
		}
		if d.Label != "eno1" {
			t.Errorf("label = %q, want eno1", d.Label)
		}
		if d.Fields["Mac"] != "bc:24:11:00:00:01" {
			t.Errorf("Fields[Mac] = %v, want bc:24:11:00:00:01 (got fields: %+v)", d.Fields["Mac"], d.Fields)
		}
		// Provenance: linkUp is host-netlink-only and single-source here
		// (only pve1 is polled), so it should be owned by host-netlink with
		// no conflicts.
		fp, ok := d.Provenance["linkUp"]
		if !ok {
			t.Fatalf("expected a linkUp provenance entry; got %+v", d.Provenance)
		}
		if fp.Owner != "host-netlink" {
			t.Errorf("linkUp owner = %q, want host-netlink", fp.Owner)
		}
		// Related: eno1 is a bridge port and an LLDP-adjacent NIC.
		var sawPortOf, sawLldp bool
		for _, rel := range d.Related {
			switch rel.EdgeKind {
			case "port-of":
				sawPortOf = true
				if rel.Direction != "to" {
					t.Errorf("port-of direction = %q, want to (eno1 is the From side)", rel.Direction)
				}
			case "lldp-adjacent":
				sawLldp = true
			}
		}
		if !sawPortOf {
			t.Errorf("expected a port-of related entry; got %+v", d.Related)
		}
		if !sawLldp {
			t.Errorf("expected an lldp-adjacent related entry; got %+v", d.Related)
		}
		if d.GeneratedAt <= 0 {
			t.Errorf("GeneratedAt = %d, want > 0", d.GeneratedAt)
		}
	})
}

// TestDetail_RawSource_LocalBridge is audit finding F-08's local-node half:
// GET /inventory/{ref} for a bridge on the collector's own node must return
// the verbatim interfaces(5) stanza (byte-identical to the fixture's file)
// under rawSource["host-interfaces"], plus the PVE network API object's
// JSON under rawSource["pve-network"] — the "raw source (interfaces stanza
// / PVE API object)" docs/api.md promises.
func TestDetail_RawSource_LocalBridge(t *testing.T) {
	graph, _, _, srv := buildGraphWithMock(t, fixtureSingleNode)
	snap := graph.Snapshot()

	d, ok := topology.Detail(snap, inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"})
	if !ok {
		t.Fatal("expected ok=true for vmbr0")
	}

	stanza, ok := d.RawSource[string(inventory.SourceHostInterfaces)]
	if !ok {
		t.Fatalf("rawSource has no host-interfaces entry; got sources: %v", rawSourceKeys(d.RawSource))
	}
	if !strings.Contains(stanza, "iface vmbr0") {
		t.Errorf("host-interfaces raw source does not look like vmbr0's stanza:\n%s", stanza)
	}
	// Verbatim means byte-identical to the file the fixture host reader
	// serves: the stanza must be a literal substring of it.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	file, err := pvemock.NewFixtureHostReader(srv).InterfacesFile(ctx, "pve1", false)
	if err != nil {
		t.Fatalf("InterfacesFile: %v", err)
	}
	if !strings.Contains(file, stanza) {
		t.Errorf("host-interfaces raw source is not a verbatim substring of the interfaces file.\nstanza:\n%s\nfile:\n%s", stanza, file)
	}

	pveObj, ok := d.RawSource[string(inventory.SourcePVENetwork)]
	if !ok {
		t.Fatalf("rawSource has no pve-network entry; got sources: %v", rawSourceKeys(d.RawSource))
	}
	if !json.Valid([]byte(pveObj)) {
		t.Errorf("pve-network raw source is not valid JSON:\n%s", pveObj)
	}
	if !strings.Contains(pveObj, "vmbr0") {
		t.Errorf("pve-network raw source does not mention vmbr0:\n%s", pveObj)
	}
}

// TestDetail_RawSource_PeerEntity is F-08's peer-node half: an entity on a
// node this daemon never host-polls (pve2 in the three-node fixture) still
// carries the PVE API object JSON as raw source — and no host-interfaces
// entry, since no host source ever contributed.
func TestDetail_RawSource_PeerEntity(t *testing.T) {
	graph, _, _ := buildGraph(t, fixtureThreeNodeVlan)
	snap := graph.Snapshot()

	d, ok := topology.Detail(snap, inventory.Ref{Kind: inventory.KindBridge, Node: "pve2", ID: "vmbr0"})
	if !ok {
		t.Fatal("expected ok=true for pve2's vmbr0")
	}
	pveObj, ok := d.RawSource[string(inventory.SourcePVENetwork)]
	if !ok {
		t.Fatalf("rawSource has no pve-network entry for a peer-node bridge; got sources: %v", rawSourceKeys(d.RawSource))
	}
	if !json.Valid([]byte(pveObj)) {
		t.Errorf("pve-network raw source is not valid JSON:\n%s", pveObj)
	}
	if _, has := d.RawSource[string(inventory.SourceHostInterfaces)]; has {
		t.Errorf("peer-node bridge unexpectedly has a host-interfaces raw source (only the local node is host-polled)")
	}
}

func rawSourceKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestDetailBridgeAddressesShape pins the wire shape of a bridge's addresses
// in GET /inventory/{ref}.
//
// This is a contract test with a specific consumer: the SDN VXLAN zone
// wizard's peer auto-suggest (web/src/sdn/wizards/peerSuggest.ts) reads this
// field to prefill each member node's underlay address. It read a lowercase
// `addresses` key and type-guarded the value to `string`, on the strength of
// a code comment citing inventory.Bridge's `fieldMap` — which is the
// merge/provenance table, not this projection. `fields` is built by
// json.Marshal over the entity, so the key is the Go field name and the
// value is an array. The lookup therefore missed on every node, the wizard
// could never be completed, and its own unit tests passed because their
// fixture invented the shape the code expected (T-2108).
//
// Assert on both halves — key spelling AND element type — because getting
// either wrong is silent on this side and fatal on the other.
func TestDetailBridgeAddressesShape(t *testing.T) {
	graph, _, _ := buildGraph(t, fixtureThreeNodeVlan)
	d, ok := topology.Detail(graph.Snapshot(), inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"})
	if !ok {
		t.Fatal("expected bridge:pve1:vmbr0 in the three-node-vlan fixture")
	}

	raw, present := d.Fields["Addresses"]
	if !present {
		t.Fatalf("Fields has no \"Addresses\" key; keys = %v", fieldKeys(d.Fields))
	}
	list, isSlice := raw.([]any)
	if !isSlice {
		t.Fatalf("Fields[\"Addresses\"] is %T, want a JSON array — the frontend's peer-suggest parses it as a list", raw)
	}
	if len(list) == 0 {
		t.Fatal("Fields[\"Addresses\"] is empty for a bridge the fixture gives an address; nothing downstream could suggest a peer address from this")
	}
	first, isStr := list[0].(string)
	if !isStr {
		t.Fatalf("Fields[\"Addresses\"][0] is %T, want a CIDR string", list[0])
	}
	if !strings.Contains(first, "/") {
		t.Errorf("Fields[\"Addresses\"][0] = %q, want a CIDR (the consumer strips the prefix to get a host address)", first)
	}
}

func fieldKeys(fields map[string]any) []string {
	out := make([]string, 0, len(fields))
	for k := range fields {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
