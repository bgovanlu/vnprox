// SPDX-License-Identifier: Apache-2.0

package findings_test

import (
	"encoding/json"
	"testing"

	"github.com/bgovanlu/vnprox/internal/findings"
)

// TestFinding_JSONSchema_Stable is a regression guard in the same family as
// internal/sim.TestRuleRef_JSONSchema_Stable (planning/reports/T-2002.md):
// findings.Finding is not just GET /findings' response body — it is also the
// frozen `findings.list` MCP tool's payload, returned VERBATIM
// (cmd/vnproxd/mcpwire.go's setupMCP: `return findingsEngine.Findings(), nil`
// — no projection). docs/architecture.md §13.1 (decision D10) makes this an
// additive-only contract for both surfaces at once. This test golden-checks
// every documented field name on a representative, fully-populated Finding
// (including the T-2604/T-2402 optional AckableAt/Ack additions) survives a
// real marshal, byte for byte.
func TestFinding_JSONSchema_Stable(t *testing.T) {
	f := findings.Finding{
		ID: "peer:peer_untrusted|pve2", Source: findings.SourcePeer, Check: "peer_untrusted",
		Severity: "warning", Detail: "cluster CA trust anchor unavailable", DocsLink: "docs/security.md",
		Nodes: []string{"pve2"}, Refs: []string{"peer:pve2"}, Fixable: false,
		AckableAt: 1, Ack: &findings.Ack{},
	}

	got, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(got, &generic); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, field := range []string{"id", "source", "check", "severity", "detail", "docsLink", "nodes", "refs", "fixable", "ackableAt", "ack"} {
		if _, ok := generic[field]; !ok {
			t.Errorf("Finding JSON missing frozen field %q (got %s)", field, got)
		}
	}
}
