// SPDX-License-Identifier: Apache-2.0

package pvemock

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestSDNZoneSpec_WireFormatIsCommaString locks the mock to real PVE's wire
// format: a zone's `nodes` (and exit_nodes/peers) serialize as a
// comma-separated STRING, not a JSON array — the exact shape a live PVE 9.2.4
// node returns. Modeling it as an array is what let the client-side decode
// bug ship; this keeps the mock honest so the SDN/IPAM integration tests
// exercise the real format.
func TestSDNZoneSpec_WireFormatIsCommaString(t *testing.T) {
	out, err := json.Marshal(SDNZoneSpec{ID: "labz", Type: "simple", Nodes: []string{"pve1", "pve2"}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if got, ok := m["nodes"].(string); !ok || got != "pve1,pve2" {
		t.Errorf("nodes wire value = %#v, want the string \"pve1,pve2\"", m["nodes"])
	}
}

// TestSDNZoneSpec_AcceptsCommaStringOnDecode proves the create/update path
// accepts the comma-string body the real pve client now sends.
func TestSDNZoneSpec_AcceptsCommaStringOnDecode(t *testing.T) {
	var z SDNZoneSpec
	if err := json.Unmarshal([]byte(`{"zone":"labz","type":"simple","nodes":"pve1,pve2"}`), &z); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(z.Nodes, []string{"pve1", "pve2"}) {
		t.Errorf("nodes = %#v, want [pve1 pve2]", z.Nodes)
	}
}
