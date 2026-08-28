// SPDX-License-Identifier: Apache-2.0

package ipam_test

import (
	"encoding/json"
	"testing"

	"github.com/bgovanlu/vnprox/internal/ipam"
)

// TestSubnetsResponse_JSONSchema_Stable is a regression guard in the same
// family as internal/sim.TestRuleRef_JSONSchema_Stable
// (planning/reports/T-2002.md): ipam.SubnetsResponse is not just
// GET /ipam/subnets' response body — it is also the frozen
// `ipam.subnets.list` MCP tool's payload, returned VERBATIM
// (cmd/vnproxd/mcpwire.go's setupMCP: `return ipamSvc.Subnets(ctx)` — no
// projection). docs/architecture.md §13.1 (decision D10) makes this an
// additive-only contract for both surfaces at once. This test golden-checks
// every documented field name on a representative, fully-populated Subnet
// survives a real marshal, byte for byte.
func TestSubnetsResponse_JSONSchema_Stable(t *testing.T) {
	resp := ipam.SubnetsResponse{
		Items: []ipam.Subnet{{
			CIDR: "10.0.0.0/24", Zone: "vlanz", Vnet: "vnet200", Gateway: "10.0.0.1", Node: "pve1",
			Source: "sdn", ReadOnly: true, DHCPEnabled: true,
			Total: 254, Allocated: 10, Observed: 12, Conflicts: 1, Utilization: 0.05,
		}},
		GeneratedAt: 1,
	}

	got, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(got, &generic); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, field := range []string{"items", "generatedAt"} {
		if _, ok := generic[field]; !ok {
			t.Errorf("SubnetsResponse JSON missing frozen top-level field %q (got %s)", field, got)
		}
	}
	items, _ := generic["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %v, want one entry", items)
	}
	item, _ := items[0].(map[string]any)
	for _, field := range []string{"cidr", "zone", "vnet", "gateway", "node", "source", "readOnly", "dhcpEnabled", "total", "allocated", "observed", "conflicts", "utilization"} {
		if _, ok := item[field]; !ok {
			t.Errorf("Subnet JSON missing frozen field %q (got %v)", field, item)
		}
	}
}
