// SPDX-License-Identifier: Apache-2.0

// conntrack.go backs T-1305's internal/host.Reader.Conntrack for fixture
// tests: FixtureHostReader.Conntrack returns a node's fixture-declared live
// conntrack table verbatim (NodeSpec.Conntrack — already-structured data,
// mirroring neighbors.go's own precedent for why this is a declared entry
// list rather than a raw-text blob like DHCPLeases: unlike dnsmasq's
// .leases format, /proc/net/nf_conntrack's own text format has its own
// dedicated golden-fixture parser tests in internal/host/conntrack_test.go
// already — a fixture only needs to express the parsed shape the
// API/UI consume).

package pvemock

import (
	"context"
	"fmt"
)

// NatAddr is one NAT-translated endpoint, as internal/host would report it
// (mirrors internal/host.NatAddr's field set; kept as this package's own
// type for the same Go-structural-typing reason the rest of this package's
// types are — see HostReader's doc comment).
type NatAddr struct {
	IP   string
	Port int
}

// ConntrackEntry is one live conntrack table entry, as internal/host would
// report it (mirrors internal/host.ConntrackEntry's field set).
type ConntrackEntry struct {
	NatSrc     *NatAddr
	NatDst     *NatAddr
	SrcIP      string
	DstIP      string
	State      string
	Proto      int
	SrcPort    int
	DstPort    int
	TimeoutSec int
}

// Conntrack implements HostReader (T-1305): node's fixture-declared
// conntrack table, converting ConntrackEntrySpec to this file's plain
// ConntrackEntry shape.
func (h *FixtureHostReader) Conntrack(_ context.Context, node string) ([]ConntrackEntry, error) {
	ns, ok := h.state.node(node)
	if !ok {
		return nil, fmt.Errorf("pvemock: host reader: %w: node %q", ErrNotFound, node)
	}
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	out := make([]ConntrackEntry, len(ns.conntrack))
	for i, e := range ns.conntrack {
		out[i] = ConntrackEntry{
			Proto: e.Proto, SrcIP: e.SrcIP, DstIP: e.DstIP, SrcPort: e.SrcPort, DstPort: e.DstPort,
			State: e.State, TimeoutSec: e.TimeoutSec,
			NatSrc: convertNatAddrSpec(e.NatSrc), NatDst: convertNatAddrSpec(e.NatDst),
		}
	}
	return out, nil
}

func convertNatAddrSpec(s *NatAddrSpec) *NatAddr {
	if s == nil {
		return nil
	}
	return &NatAddr{IP: s.IP, Port: s.Port}
}
