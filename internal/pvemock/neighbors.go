// SPDX-License-Identifier: Apache-2.0

// neighbors.go backs T-805's internal/host.Reader.Neighbors for fixture
// tests: FixtureHostReader.Neighbors returns a node's fixture-declared ARP/
// IPv6-neighbor table verbatim (NodeSpec.Neighbors — see that field's doc
// comment in types.go for why it's already-structured data rather than a
// raw-text blob like DHCPLeases), unfiltered — internal/host.FixtureReader
// applies the resolved-states-only filter Reader.Neighbors documents, the
// same division of responsibility InterfacesFile/Links/etc. already have
// between this package (raw fixture data) and internal/host (the documented
// Reader contract's semantics).

package pvemock

import (
	"context"
	"fmt"
)

// Neighbor is one ARP/IPv6-neighbor-table entry, as internal/host would
// report it (mirrors internal/host.Neighbor's field set; kept as this
// package's own type for the same reason the rest of this package's types
// are — see HostReader's doc comment on why it's defined standalone).
type Neighbor struct {
	IP    string
	Mac   string
	Iface string
	State string
}

// Neighbors implements HostReader (T-805): node's fixture-declared ARP/
// IPv6-neighbor table, every entry returned as declared (State defaulting
// to "REACHABLE" when the fixture leaves it unset, the same
// "unremarkable-unless-declared-otherwise" default Services/Stats/Links
// already follow) — no state filtering at this layer, see this file's doc
// comment.
func (h *FixtureHostReader) Neighbors(_ context.Context, node string) ([]Neighbor, error) {
	ns, ok := h.state.node(node)
	if !ok {
		return nil, fmt.Errorf("pvemock: host reader: %w: node %q", ErrNotFound, node)
	}
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	out := make([]Neighbor, 0, len(ns.neighbors))
	for _, n := range ns.neighbors {
		state := n.State
		if state == "" {
			state = "REACHABLE"
		}
		out = append(out, Neighbor{IP: n.IP, Mac: n.Mac, Iface: n.Iface, State: state})
	}
	return out, nil
}
