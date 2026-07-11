package pvemock

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// HostReader is the contract for local host-level reads that the PVE API
// does not expose: the literal /etc/network/interfaces(5) file content,
// netlink-equivalent link/bridge/bond state, LLDP neighbor data, and
// interface counters. It is defined here (rather than in internal/host,
// which is T-102's package to build) so the mock server can back it today;
// T-102's `real` implementation reads actual netlink/lldpd/ethtool/procfs
// state and must satisfy the same method set — Go's structural interface
// typing means it does not need to import this package to do so, only to
// match these signatures.
//
// Every method takes a node name because vnprox is cluster-aware: any
// node's daemon may need another node's host state via the peer API
// (docs/architecture.md §1, §5).
type HostReader interface {
	// InterfacesFile returns the literal content of /etc/network/interfaces
	// (or interfaces.new when includePending is true and a staged config
	// exists) for node, rendered in ifupdown2(5) stanza syntax.
	InterfacesFile(ctx context.Context, node string, includePending bool) (string, error)

	// Links returns netlink-equivalent link state (NICs, bonds, bridges,
	// VLANs) for node.
	Links(ctx context.Context, node string) ([]LinkState, error)

	// LLDP returns raw LLDP neighbor JSON for node, matching the shape
	// `lldpctl -f json` would produce closely enough for internal/host to
	// parse into inventory.LldpNeighbor.
	LLDP(ctx context.Context, node string) ([]byte, error)

	// Stats returns interface counters for node.
	Stats(ctx context.Context, node string) (map[string]IfaceStats, error)

	// Services returns fixture-declared systemd unit status for node (T-602).
	Services(ctx context.Context, node string) (map[string]bool, error)
}

// LinkState is one netlink-equivalent link (physical NIC, bond, bridge, or
// VLAN sub-interface) as internal/host would report it.
type LinkState struct {
	Name    string
	Kind    string
	Mac     string
	Driver  string
	Duplex  string
	PCIAddr string
	Members []string
	// FDB is this (bridge-kind) link's fixture-declared forwarding
	// database (T-306's MAC/FDB browser) — nil for every non-bridge Kind.
	FDB       []FDBEntry
	SpeedMbps int
	MTU       int
	LinkUp    bool
}

// FDBEntry is one bridge forwarding-database entry, as internal/host would
// report it (mirrors internal/host.FDBEntry's field set; kept as this
// package's own type for the same reason the rest of LinkState is, per
// internal/host's Reader doc comment: Go's structural typing requires
// identical result types, not just structurally similar ones).
type FDBEntry struct {
	Mac       string
	Port      string
	Vlan      int
	Master    bool
	Permanent bool
	Stale     bool
}

// FixtureHostReader implements HostReader by reading a mock server's
// runtime State — i.e. the same YAML fixture backing the HTTP API, so a
// test exercising both the PVE API and host.Reader sees one consistent
// view of the world.
type FixtureHostReader struct {
	state *State
}

// NewFixtureHostReader builds a FixtureHostReader over srv's state.
func NewFixtureHostReader(srv *Server) *FixtureHostReader {
	return &FixtureHostReader{state: srv.state}
}

var _ HostReader = (*FixtureHostReader)(nil)

func (h *FixtureHostReader) InterfacesFile(_ context.Context, node string, includePending bool) (string, error) {
	ns, ok := h.state.node(node)
	if !ok {
		return "", fmt.Errorf("pvemock: host reader: %w: node %q", ErrNotFound, node)
	}
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	ifaces := ns.network
	if includePending && ns.networkPending != nil {
		ifaces = ns.networkPending
	}
	return RenderInterfaces(ifaces), nil
}

func (h *FixtureHostReader) Links(_ context.Context, node string) ([]LinkState, error) {
	ns, ok := h.state.node(node)
	if !ok {
		return nil, fmt.Errorf("pvemock: host reader: %w: node %q", ErrNotFound, node)
	}
	ns.mu.RLock()
	defer ns.mu.RUnlock()

	out := make([]LinkState, 0, len(ns.network))
	for _, iface := range ns.network {
		ls := LinkState{Name: iface.Iface, Kind: iface.Type, MTU: iface.MTU}
		if link, ok := ns.links[iface.Iface]; ok {
			ls.Mac = link.Mac
			ls.Driver = link.Driver
			ls.SpeedMbps = link.SpeedMbps
			ls.Duplex = link.Duplex
			ls.LinkUp = link.LinkUp
			ls.PCIAddr = link.PCIAddr
			if ls.MTU == 0 {
				ls.MTU = link.MTU
			}
		}
		if link, ok := ns.links[iface.Iface]; ok && len(link.Members) > 0 {
			// Explicit fixture override: live membership diverges from the
			// declared bridge_ports/slaves (see LinkInfo.Members' doc
			// comment) — a fixture-simulated manual `ip link` change.
			ls.Members = append([]string(nil), link.Members...)
		} else {
			switch iface.Type {
			case "bridge", "OVSBridge":
				ls.Members = strings.Fields(iface.BridgePorts)
			case "bond":
				ls.Members = strings.Fields(iface.Slaves)
			}
		}
		if iface.Type == "bridge" || iface.Type == "OVSBridge" {
			if link, ok := ns.links[iface.Iface]; ok {
				ls.FDB = convertFDBSpecs(link.FDB)
			}
		}
		out = append(out, ls)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// convertFDBSpecs converts a fixture's declared FDB entries (LinkInfo.FDB,
// YAML-tagged) to this file's plain LinkState.FDB shape.
func convertFDBSpecs(specs []FDBEntrySpec) []FDBEntry {
	if len(specs) == 0 {
		return nil
	}
	out := make([]FDBEntry, len(specs))
	for i, s := range specs {
		out[i] = FDBEntry(s)
	}
	return out
}

func (h *FixtureHostReader) LLDP(_ context.Context, node string) ([]byte, error) {
	ns, ok := h.state.node(node)
	if !ok {
		return nil, fmt.Errorf("pvemock: host reader: %w: node %q", ErrNotFound, node)
	}
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	return marshalLLDP(ns.lldp)
}

func (h *FixtureHostReader) Stats(_ context.Context, node string) (map[string]IfaceStats, error) {
	ns, ok := h.state.node(node)
	if !ok {
		return nil, fmt.Errorf("pvemock: host reader: %w: node %q", ErrNotFound, node)
	}
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	return cloneMap(ns.stats), nil
}

// watchedServiceNames is host.WatchedServices, duplicated here (as a plain
// literal, not an import) so this package — deliberately host-package-free,
// per its own doc comment on why HostReader is defined standalone rather
// than depending on internal/host — doesn't need to import internal/host
// purely for this one constant.
var watchedServiceNames = []string{"dnsmasq", "frr"}

// Services implements HostReader: every fixture-declared override in
// ns.services wins; every other watched service name defaults to
// active=true (see NodeSpec.Services' doc comment on why "unremarkable
// unless declared otherwise" is the right default for a fixture).
func (h *FixtureHostReader) Services(_ context.Context, node string) (map[string]bool, error) {
	ns, ok := h.state.node(node)
	if !ok {
		return nil, fmt.Errorf("pvemock: host reader: %w: node %q", ErrNotFound, node)
	}
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	out := make(map[string]bool, len(watchedServiceNames))
	for _, name := range watchedServiceNames {
		if v, ok := ns.services[name]; ok {
			out[name] = v
			continue
		}
		out[name] = true
	}
	return out, nil
}
