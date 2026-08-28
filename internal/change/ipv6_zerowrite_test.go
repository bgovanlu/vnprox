// SPDX-License-Identifier: Apache-2.0

package change

import (
	"strings"
	"testing"
)

// TestNoDHCPv6PDOpType is T-1404 acceptance criterion 7: no changeset op
// type exists anywhere in the v1 op vocabulary that could write a DHCPv6-PD
// (prefix delegation) request to an upstream device — IPv6 visibility
// (GET /ipv6/segments) is read-only, and the dual-stack rollout wizard's
// own mutation surface is the pre-existing, generic sdn.subnet.create/
// update/delete family (a v6 CIDR is just a string to those ops, see
// internal/ipam/v6plan.go's doc comment) — this task adds zero new op
// types. Grep-verifiable at the source level too; this test pins it
// structurally against the actual registered op vocabulary so a future op
// addition trips it immediately rather than silently drifting.
func TestNoDHCPv6PDOpType(t *testing.T) {
	for opType := range paramFactories {
		lower := strings.ToLower(string(opType))
		if strings.Contains(lower, "dhcpv6") || strings.Contains(lower, "delegat") {
			t.Errorf("op type %q suggests a DHCPv6-PD write surface — T-1404 must never introduce one", opType)
		}
	}
}
