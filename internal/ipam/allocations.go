package ipam

import (
	"context"
	"errors"
	"net"
	"sort"
)

// ErrSubnetNotFound is returned by Allocations for a CIDR that names
// neither a currently-configured SDN subnet nor a detected non-SDN
// (bridge-derived) subnet.
var ErrSubnetNotFound = errors.New("ipam: subnet not found")

// resolvedSubnet bundles what Allocations/AllocationsCSV both need once a
// requested CIDR has been matched against either the live SDN subnet list
// or the detected non-SDN (bridge-derived) list.
type resolvedSubnet struct {
	known     knownGuests
	canonical string
	gateway   string
	allocs    []Allocation
	obs       []Observation
	total     int
	prefix    int
	readOnly  bool
}

// resolveSubnet looks up cidr (any address/mask within the subnet, not
// necessarily its canonical network form) against every currently-known
// subnet (SDN, live from PVE; non-SDN, detected from bridge addresses) and
// gathers everything a grid/CSV render needs. Returns ErrSubnetNotFound if
// cidr names neither.
func (s *Service) resolveSubnet(ctx context.Context, cidr string) (resolvedSubnet, error) {
	snap := s.inv.Snapshot()
	known := knownGuestsFromSnapshot(snap)
	obsAll := s.enrichmentObservations(ctx, snap)

	sdnInfo, err := s.sdnSubnets(ctx)
	if err != nil {
		return resolvedSubnet{}, err
	}
	sdnCIDRs := make([]string, 0, len(sdnInfo))
	for _, info := range sdnInfo {
		sdnCIDRs = append(sdnCIDRs, info.cidr)
	}

	var out resolvedSubnet
	found := false
	for _, info := range sdnInfo {
		if sameNetwork(info.cidr, cidr) {
			out.canonical, out.gateway, found = info.cidr, info.gateway, true
			break
		}
	}
	if found {
		allocByCIDR, err := s.allocationsByCIDR(ctx)
		if err != nil {
			return resolvedSubnet{}, err
		}
		out.allocs = allocByCIDR[out.canonical]
	} else {
		for _, ns := range nonSDNSubnets(snap, sdnCIDRs, obsAll) {
			if sameNetwork(ns.CIDR, cidr) {
				out.canonical, out.gateway, out.readOnly, found = ns.CIDR, ns.Gateway, true, true
				break
			}
		}
	}
	if !found {
		return resolvedSubnet{}, ErrSubnetNotFound
	}

	total, prefix, ok := subnetAddrCount(out.canonical)
	if !ok {
		return resolvedSubnet{}, ErrSubnetNotFound
	}
	out.total, out.prefix = total, prefix
	out.obs = observationsForCIDR(out.canonical, obsAll)
	out.known = known
	return out, nil
}

// Allocations builds docs/api.md's
// `GET /ipam/subnets/{cidr}/allocations` response: the address list
// (occupied Entries + collapsed FreeRanges) for the whole subnet, at any
// size. The merged cell map is sparse (proportional to actual allocations
// and observations, never to the address space), so the same single pass
// serves a /30 and a /16 alike — the free space between occupied addresses
// is emitted as ranges, not materialized address by address.
func (s *Service) Allocations(ctx context.Context, cidr string) (AllocationList, error) {
	rs, err := s.resolveSubnet(ctx, cidr)
	if err != nil {
		return AllocationList{}, err
	}
	cellMap, conflicts := mergeSubnet(rs.allocs, rs.obs, rs.known, rs.gateway)

	entries := sortCellsByIP(cellMap)
	ranges := freeRanges(rs.canonical, cellMap)
	counts := tallyCounts(cellMap, ranges)

	// Marshal empty collections as [] rather than null, so every client can
	// treat entries/freeRanges/conflicts as arrays unconditionally.
	if ranges == nil {
		ranges = []FreeRange{}
	}
	if conflicts == nil {
		conflicts = []Conflict{}
	}

	return AllocationList{
		CIDR:        rs.canonical,
		Gateway:     rs.gateway,
		Entries:     entries,
		FreeRanges:  ranges,
		Conflicts:   conflicts,
		Counts:      counts,
		Prefix:      rs.prefix,
		Total:       rs.total,
		ReadOnly:    rs.readOnly,
		GeneratedAt: s.now().Unix(),
	}, nil
}

// sortCellsByIP returns cellMap's occupied cells sorted by ascending numeric
// address (not lexical — so .10 sorts after .9, and IPv6 orders correctly).
func sortCellsByIP(cellMap map[string]Cell) []Cell {
	out := make([]Cell, 0, len(cellMap))
	for _, c := range cellMap {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := ipToBig(net.ParseIP(out[i].IP)), ipToBig(net.ParseIP(out[j].IP))
		if a == nil || b == nil {
			return out[i].IP < out[j].IP
		}
		return a.Cmp(b) < 0
	})
	return out
}

// tallyCounts buckets the merged cells by render state and takes Free from
// the sum of the free ranges, so the buckets partition the usable-host space
// exactly (the summary strip's segments always add up).
func tallyCounts(cellMap map[string]Cell, ranges []FreeRange) Counts {
	var c Counts
	for _, cell := range cellMap {
		switch cell.State {
		case CellAllocated:
			c.Allocated++
		case CellReserved:
			c.Reserved++
		case CellObserved:
			c.Observed++
		case CellGateway:
			c.Gateway++
		case CellConflict:
			c.Conflict++
		}
	}
	for _, r := range ranges {
		c.Free += r.Count
	}
	return c
}

// sameNetwork reports whether a and b name the same network (parsing both
// as CIDRs and comparing their canonical, masked form) — so "10.50.0.5/24"
// and "10.50.0.0/24" (or two differently-cased IPv6 literals) are
// recognized as the same subnet.
func sameNetwork(a, b string) bool {
	_, na, errA := net.ParseCIDR(a)
	_, nb, errB := net.ParseCIDR(b)
	if errA != nil || errB != nil {
		return false
	}
	return na.String() == nb.String()
}
