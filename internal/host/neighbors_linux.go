//go:build linux

package host

import (
	"context"

	"github.com/vishvananda/netlink"
)

// Neighbors implements Reader for Real (T-805): this node's IPv4 ARP table
// (readProcNetARP, neighbors.go — /proc/net/arp, portable text) plus its
// IPv6 neighbor table (via netlink — there is no /proc/net/arp equivalent
// for IPv6, so this half needs the same netlink dependency buildLinkState
// already uses for link/bridge/VLAN state), merged and filtered to resolved
// states (isResolvedNeighborState).
func (r *Real) Neighbors(_ context.Context, _ string) ([]Neighbor, error) {
	var out []Neighbor

	v4, err := readProcNetARP()
	if err != nil {
		return nil, err
	}
	out = append(out, v4...)

	v6, err := neighV6()
	if err != nil {
		// A netlink IPv6 neighbor dump failure (e.g. IPv6 disabled on this
		// host entirely) shouldn't blank the IPv4 ARP table this call
		// already successfully read — degrade to IPv4-only rather than
		// erroring the whole read.
		return out, nil
	}
	out = append(out, v6...)
	return out, nil
}

// neighV6 dumps the kernel's IPv6 neighbor table via netlink.NeighList
// (linkIndex 0 = every link), resolving each entry's link index to an
// interface name and its NUD_* state bitmask to this package's normalized
// state strings, filtered to resolved states.
func neighV6() ([]Neighbor, error) {
	entries, err := netlink.NeighList(0, netlink.FAMILY_V6)
	if err != nil {
		return nil, err
	}
	out := make([]Neighbor, 0, len(entries))
	for _, n := range entries {
		state := nudStateString(n.State)
		if !isResolvedNeighborState(state) {
			continue
		}
		iface := ""
		if link, lerr := netlink.LinkByIndex(n.LinkIndex); lerr == nil {
			iface = link.Attrs().Name
		}
		out = append(out, Neighbor{IP: n.IP.String(), MAC: n.HardwareAddr.String(), Iface: iface, State: state})
	}
	return out, nil
}

// nudStateString normalizes a netlink.Neigh.State bitmask (Linux's NUD_*
// neighbor-cache-entry states) to this package's uppercase Neighbor.State
// vocabulary. The bitmask is a single-state-at-a-time value in practice
// (the kernel reports one NUD state per neighbor entry), but is read as a
// bitmask per netlink.Neigh's own doc comment, so this checks each bit in
// most-specific-first order rather than assuming an exact equality match.
func nudStateString(state int) string {
	switch {
	case state&netlink.NUD_PERMANENT != 0:
		return NeighborPermanent
	case state&netlink.NUD_REACHABLE != 0:
		return NeighborReachable
	case state&netlink.NUD_STALE != 0:
		return NeighborStale
	case state&netlink.NUD_FAILED != 0:
		return NeighborFailed
	case state&netlink.NUD_INCOMPLETE != 0:
		return NeighborIncomplete
	default:
		// DELAY/PROBE (in-progress reconfirmation) and NOARP/NONE
		// (no-ARP-needed or uninitialized entries, e.g. a local address)
		// are all "not one of the three resolved states" — collapsed to
		// INCOMPLETE so isResolvedNeighborState excludes them uniformly
		// rather than needing to know every non-resolved NUD_* name.
		return NeighborIncomplete
	}
}
