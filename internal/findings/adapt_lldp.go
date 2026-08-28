// SPDX-License-Identifier: Apache-2.0

package findings

import (
	"strings"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// LLDPProvider is the subset of *topology.Service Engine needs for the LLDP
// VLAN cross-check producer.
type LLDPProvider interface {
	VlanFindings() []topology.VlanFinding
}

const lldpDocsLink = "docs/features/lldp-discovery.md#2-presentation"

// lldpNodeOf resolves a VlanFinding's node scope from its BridgeRef (the
// bridge is always node-scoped — VlanFindings only ever pairs a bridge with
// an LLDP neighbor seen on that same node).
func lldpNodeOf(ref string) string {
	parsed, err := inventory.ParseRef(ref)
	if err != nil {
		return ""
	}
	return parsed.Node
}

// fromVlanFinding adapts one topology.VlanFinding into the unified shape.
// VlanFinding has no fixing-changeset mechanism (which of the two
// disagreeing sides — the bridge's declared VLANs or the switch's
// advertised trunk — should change is a network-design decision, not
// something LLDP data alone can resolve safely), so every adapted finding
// gets the docs link instead.
func fromVlanFinding(f topology.VlanFinding) Finding {
	node := lldpNodeOf(f.BridgeRef)
	var nodes []string
	if node != "" {
		nodes = []string{node}
	}
	return Finding{
		ID:       "lldp:" + f.Code + "|" + f.BridgeRef + "|" + f.NeighborRef,
		Source:   SourceLLDP,
		Check:    f.Code,
		Severity: f.Severity,
		Detail:   f.Message,
		Nodes:    nodes,
		Refs:     []string{f.BridgeRef, f.NeighborRef},
		DocsLink: lldpDocsLink,
	}
}

// lldpFindings adapts every current VLAN cross-check mismatch (dropping
// VlanCheckOK entries — a clean match is a standing "all good" display in
// the LLDP page, not an actionable item for the alerting-oriented unified
// stream), or nil when p is nil.
func lldpFindings(p LLDPProvider) []Finding {
	if p == nil {
		return nil
	}
	src := p.VlanFindings()
	out := make([]Finding, 0, len(src))
	for _, f := range src {
		if f.Code == topology.VlanCheckOK || strings.TrimSpace(f.Code) == "" {
			continue
		}
		out = append(out, fromVlanFinding(f))
	}
	return out
}
