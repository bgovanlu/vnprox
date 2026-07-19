package pvemock

import (
	"context"
	"fmt"
	"strconv"
)

// ContainerInterior implements HostReader (T-1304): an lxc guest's
// fixture-declared interior read set, straight off its GuestSpec's
// Interior* fields (see that type's doc comment) — no exec round trip,
// since this package's mock never actually runs nsenter/ip/ss any more
// than it actually runs ping/nc for the qemu probe path.
func (h *FixtureHostReader) ContainerInterior(_ context.Context, node string, vmid int) (ContainerInteriorRaw, error) {
	g, err := h.lxcGuest(node, vmid)
	if err != nil {
		return ContainerInteriorRaw{}, err
	}
	return ContainerInteriorRaw{
		AddrJSON:   []byte(g.InteriorAddrJSON),
		RouteJSON:  []byte(g.InteriorRoutesJSON),
		ResolvConf: []byte(g.InteriorResolvConf),
		Sockets:    []byte(g.InteriorSockets),
	}, nil
}

// ContainerPing implements HostReader (T-1304): matches targetIP against
// the lxc guest's InteriorPingOutcomes table by exact equality; no match
// is Reachable false, matching AgentExecOutcomes' own "unscripted is
// error/no-reply, never a guessed default" convention (here: no reply,
// since a ping either succeeds or it doesn't — there is no third "we don't
// know" outcome the wire response can carry, unlike the qemu path's
// probe.Outcome).
func (h *FixtureHostReader) ContainerPing(_ context.Context, node string, vmid int, targetIP string) (bool, error) {
	g, err := h.lxcGuest(node, vmid)
	if err != nil {
		return false, err
	}
	for _, o := range g.InteriorPingOutcomes {
		if o.IP == targetIP {
			return o.Reachable, nil
		}
	}
	return false, nil
}

// lxcGuest resolves (node, vmid) to its GuestSpec within ns.lxc, or
// ErrNotFound — shared by ContainerInterior/ContainerPing above.
func (h *FixtureHostReader) lxcGuest(node string, vmid int) (*GuestSpec, error) {
	ns, ok := h.state.node(node)
	if !ok {
		return nil, fmt.Errorf("pvemock: host reader: %w: node %q", ErrNotFound, node)
	}
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	g, ok := ns.lxc[strconv.Itoa(vmid)]
	if !ok {
		return nil, fmt.Errorf("pvemock: host reader: %w: lxc %d not found on node %q", ErrNotFound, vmid, node)
	}
	return g, nil
}
