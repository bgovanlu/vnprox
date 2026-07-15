package pve

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestSDNZone_DecodesRealPVENodeString is the regression test for the
// hardware-validation gap that took down every SDN/IPAM read: real PVE
// (9.2.4) returns a zone's `nodes` as a comma-separated STRING, not a JSON
// array. This is the exact body observed from GET /cluster/sdn/zones on a
// live node.
func TestSDNZone_DecodesRealPVENodeString(t *testing.T) {
	const body = `{"digest":"e5df","ipam":"pve","nodes":"pvecube","type":"simple","zone":"labz"}`
	var z SDNZone
	if err := json.Unmarshal([]byte(body), &z); err != nil {
		t.Fatalf("decoding real-PVE zone: %v", err)
	}
	if z.ID != "labz" || z.Type != "simple" {
		t.Errorf("zone = %+v, want id=labz type=simple", z)
	}
	if !reflect.DeepEqual(z.Nodes, []string{"pvecube"}) {
		t.Errorf("nodes = %#v, want [pvecube]", z.Nodes)
	}
}

func TestSDNZone_DecodesMultiNodeAndArrayForms(t *testing.T) {
	// Comma-string with several nodes (real PVE, multi-node cluster).
	var z SDNZone
	if err := json.Unmarshal([]byte(`{"zone":"z","type":"simple","nodes":"pve1,pve2,pve3"}`), &z); err != nil {
		t.Fatalf("decoding multi-node string: %v", err)
	}
	if !reflect.DeepEqual(z.Nodes, []string{"pve1", "pve2", "pve3"}) {
		t.Errorf("nodes = %#v, want [pve1 pve2 pve3]", z.Nodes)
	}
	// A JSON array is still tolerated (fixtures / forward-compat).
	var z2 SDNZone
	if err := json.Unmarshal([]byte(`{"zone":"z","type":"simple","nodes":["pve1","pve2"]}`), &z2); err != nil {
		t.Fatalf("decoding array form: %v", err)
	}
	if !reflect.DeepEqual(z2.Nodes, []string{"pve1", "pve2"}) {
		t.Errorf("nodes (array form) = %#v, want [pve1 pve2]", z2.Nodes)
	}
}

// TestSDNZone_MarshalsNodesAsCommaString proves the write path sends PVE the
// comma-string it expects on create/update, not a JSON array.
func TestSDNZone_MarshalsNodesAsCommaString(t *testing.T) {
	out, err := json.Marshal(SDNZone{ID: "labz", Type: "simple", Nodes: []string{"pve1", "pve2"}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Decode into a generic map to assert the wire type of `nodes`.
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if got, ok := m["nodes"].(string); !ok || got != "pve1,pve2" {
		t.Errorf("nodes wire value = %#v, want the string \"pve1,pve2\"", m["nodes"])
	}
	// An empty node list is omitted entirely (omitempty), not sent as "".
	out2, _ := json.Marshal(SDNZone{ID: "z", Type: "simple"})
	var m2 map[string]any
	_ = json.Unmarshal(out2, &m2)
	if _, present := m2["nodes"]; present {
		t.Errorf("empty nodes should be omitted, got %v", m2["nodes"])
	}
}

// TestSDNZone_RoundTrip proves marshal->unmarshal preserves the node list
// through PVE's string form.
func TestSDNZone_RoundTrip(t *testing.T) {
	in := SDNZone{ID: "z", Type: "evpn", Nodes: []string{"a", "b"}, Peers: []string{"10.0.0.1", "10.0.0.2"}}
	out, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got SDNZone
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got.Nodes, in.Nodes) || !reflect.DeepEqual(got.Peers, in.Peers) {
		t.Errorf("round trip = %+v, want %+v", got, in)
	}
}
