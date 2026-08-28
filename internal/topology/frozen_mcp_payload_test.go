// SPDX-License-Identifier: Apache-2.0

package topology_test

import (
	"encoding/json"
	"testing"

	"github.com/bgovanlu/vnprox/internal/topology"
)

// TestTopology_JSONSchema_Stable is a regression guard in the same family as
// internal/sim.TestRuleRef_JSONSchema_Stable (planning/reports/T-2002.md):
// *topology.Topology is not just GET /topology's response body — it is also
// the frozen `topology.get` MCP tool's payload, returned VERBATIM
// (cmd/vnproxd/mcpwire.go's setupMCP: `return topoSvc.Topology(topology.Filter{}), nil`
// — no projection). docs/architecture.md §13.1 (decision D10) makes this an
// additive-only contract for both surfaces at once: no field is ever removed
// or renamed without a version bump. This test golden-checks that Topology's,
// Node's, and Edge's documented field names are still present, byte for
// byte, on a representative populated value — not merely that the zero value
// marshals (which "omitempty" could hide a removed field behind).
func TestTopology_JSONSchema_Stable(t *testing.T) {
	topo := topology.Topology{
		Nodes: []topology.Node{{
			ID: "bridge:pve1:vmbr0", Kind: "bridge", Label: "vmbr0", Layer: topology.LayerPhysical,
			NodeGroup: "pve1", Status: topology.StatusOK, Badges: []string{"mgmt"},
		}},
		Edges: []topology.Edge{{
			From: "bridge:pve1:vmbr0", To: "physnic:pve1:eno1", Kind: "member", Status: topology.StatusOK, Badges: []string{},
		}},
		Layers:      topology.AllLayers,
		GeneratedAt: 1,
	}

	got, err := json.Marshal(topo)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(got, &generic); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, field := range []string{"nodes", "edges", "layers", "generatedAt"} {
		if _, ok := generic[field]; !ok {
			t.Errorf("Topology JSON missing frozen top-level field %q (got %s)", field, got)
		}
	}

	nodes, _ := generic["nodes"].([]any)
	if len(nodes) != 1 {
		t.Fatalf("nodes = %v, want one entry", nodes)
	}
	nodeEntry, _ := nodes[0].(map[string]any)
	for _, field := range []string{"id", "kind", "label", "layer", "nodeGroup", "status", "badges"} {
		if _, ok := nodeEntry[field]; !ok {
			t.Errorf("Topology.Nodes[0] JSON missing frozen field %q (got %v)", field, nodeEntry)
		}
	}

	edges, _ := generic["edges"].([]any)
	if len(edges) != 1 {
		t.Fatalf("edges = %v, want one entry", edges)
	}
	edgeEntry, _ := edges[0].(map[string]any)
	for _, field := range []string{"from", "to", "kind", "status", "badges"} {
		if _, ok := edgeEntry[field]; !ok {
			t.Errorf("Topology.Edges[0] JSON missing frozen field %q (got %v)", field, edgeEntry)
		}
	}
}
