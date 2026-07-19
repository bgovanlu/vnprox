// ipv6ra.go backs T-1404's internal/host.Reader.IPv6RA for fixture tests:
// FixtureHostReader.IPv6RA returns a node's fixture-declared per-interface
// IPv6 Router Advertisement / DHCPv6 observation verbatim (NodeSpec.IPv6RA
// — already-structured data, the same "fixture only needs to express the
// parsed shape" precedent conntrack.go/neighbors.go both set).

package pvemock

import (
	"context"
	"fmt"
)

// IPv6RAObservation is one interface's IPv6 RA/DHCPv6 observation, as
// internal/host would report it (mirrors internal/host.IPv6RAObservation's
// field set; kept as this package's own type for the same Go-structural-
// typing reason the rest of this package's types are — see HostReader's
// doc comment).
type IPv6RAObservation struct {
	Iface                string
	Prefixes             []string
	RouterLifetimeSec    int
	RAPresent            bool
	ManagedFlag          bool
	OtherFlag            bool
	DHCPv6ServerPresent  bool
	DHCPv6InferredFromRA bool
}

// IPv6RA implements HostReader (T-1404): node's fixture-declared RA/DHCPv6
// observations, converting IPv6RASpec to this file's plain
// IPv6RAObservation shape.
func (h *FixtureHostReader) IPv6RA(_ context.Context, node string) ([]IPv6RAObservation, error) {
	ns, ok := h.state.node(node)
	if !ok {
		return nil, fmt.Errorf("pvemock: host reader: %w: node %q", ErrNotFound, node)
	}
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	out := make([]IPv6RAObservation, len(ns.ipv6RA))
	for i, e := range ns.ipv6RA {
		out[i] = IPv6RAObservation{
			Iface: e.Iface, Prefixes: append([]string(nil), e.Prefixes...),
			RouterLifetimeSec: e.RouterLifetimeSec, RAPresent: true,
			ManagedFlag: e.ManagedFlag, OtherFlag: e.OtherFlag,
			DHCPv6ServerPresent:  e.DHCPv6ServerPresent || e.ManagedFlag,
			DHCPv6InferredFromRA: e.DHCPv6ServerPresent || e.ManagedFlag,
		}
	}
	return out, nil
}
