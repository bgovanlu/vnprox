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
}

// LinkState is one netlink-equivalent link (physical NIC, bond, bridge, or
// VLAN sub-interface) as internal/host would report it.
type LinkState struct {
	Name      string
	Kind      string
	Mac       string
	Driver    string
	Duplex    string
	PCIAddr   string
	Members   []string
	SpeedMbps int
	MTU       int
	LinkUp    bool
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
		switch iface.Type {
		case "bridge", "OVSBridge":
			ls.Members = strings.Fields(iface.BridgePorts)
		case "bond":
			ls.Members = strings.Fields(iface.Slaves)
		}
		out = append(out, ls)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
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
