package ipam

import (
	"context"
	"errors"
	"net"
)

// ErrSubnetNotFound is returned by Allocations for a CIDR that names
// neither a currently-configured SDN subnet nor a detected non-SDN
// (bridge-derived) subnet.
var ErrSubnetNotFound = errors.New("ipam: subnet not found")

// GridOptions parameterizes Allocations' `?block=` paging query
// (docs/features/ipam.md §2's "paged block summaries").
type GridOptions struct {
	// Block, when set, must be one of the CIDR values the same subnet's
	// default (Block-less) paged response listed under Blocks — it drills
	// into that one /24-sized (or /120 for IPv6) block's full Cells.
	Block string
}

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
// `GET /ipam/subnets/{cidr}/allocations` response.
func (s *Service) Allocations(ctx context.Context, cidr string, opts GridOptions) (AllocationGrid, error) {
	rs, err := s.resolveSubnet(ctx, cidr)
	if err != nil {
		return AllocationGrid{}, err
	}
	canonical, gateway, readOnly := rs.canonical, rs.gateway, rs.readOnly
	allocs, subnetObs, known := rs.allocs, rs.obs, rs.known
	total, prefix := rs.total, rs.prefix
	paged := total > pagedThreshold

	if !paged || opts.Block != "" {
		target := canonical
		if opts.Block != "" {
			target = opts.Block
		}
		targetAllocs := filterAllocsForCIDR(allocs, target)
		targetObs := observationsForCIDR(target, subnetObs)
		cellMap, conflicts := mergeSubnet(targetAllocs, targetObs, known, gateway)

		addrs, ok := hostAddresses(target)
		if !ok {
			return AllocationGrid{}, ErrSubnetNotFound
		}
		cells := make([]Cell, 0, len(addrs))
		for _, ip := range addrs {
			c, ok := cellMap[ip]
			if !ok {
				c = Cell{IP: ip, State: CellFree}
			}
			cells = append(cells, c)
		}
		grid := AllocationGrid{
			CIDR: canonical, Prefix: prefix, Total: total, Paged: paged,
			Cells: cells, Conflicts: conflicts, ReadOnly: readOnly, GeneratedAt: s.now().Unix(),
		}
		if paged {
			grid.Block = target
		}
		return grid, nil
	}

	// Paged, no specific block requested: one merge pass over the whole
	// subnet's (sparse — proportional to actual allocations/observations,
	// never to address space) allocs/obs, bucketed into per-block
	// summaries in a single sweep (O(occupied cells), not
	// O(blocks x occupied cells)) — see this task's report for the
	// /16-without-jank perf note this is the backbone of.
	cellMap, conflicts := mergeSubnet(allocs, subnetObs, known, gateway)
	blocks, _ := blockCIDRs(canonical)
	summaries := bucketIntoBlocks(blocks, cellMap)
	return AllocationGrid{
		CIDR: canonical, Prefix: prefix, Total: total, Paged: true,
		Blocks: summaries, Conflicts: conflicts, ReadOnly: readOnly, GeneratedAt: s.now().Unix(),
	}, nil
}

// bucketIntoBlocks sums cellMap into one BlockSummary per entry in blocks
// (canonical order preserved). Every block is the same fixed size (256
// addresses), so which block a cell belongs to is found by masking the
// cell's own address down to that same size and doing an O(1) map lookup
// by the resulting network string, rather than scanning every block's
// net.IPNet.Contains — O(occupied cells), not O(occupied cells x blocks),
// the perf property this task's report's /16-paging note relies on.
func bucketIntoBlocks(blocks []string, cellMap map[string]Cell) []BlockSummary {
	summaries := make([]BlockSummary, len(blocks))
	indexByNetwork := make(map[string]int, len(blocks))
	var mask net.IPMask
	for i, b := range blocks {
		total, _, _ := subnetAddrCount(b)
		summaries[i] = BlockSummary{CIDR: b, Total: total}
		if _, ipnet, err := net.ParseCIDR(b); err == nil {
			indexByNetwork[ipnet.String()] = i
			mask = ipnet.Mask
		}
	}
	if mask == nil {
		return summaries
	}

	for ipStr, c := range cellMap {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			continue
		}
		network := (&net.IPNet{IP: ip.Mask(mask), Mask: mask}).String()
		i, ok := indexByNetwork[network]
		if !ok {
			continue
		}
		for _, src := range c.Sources {
			switch src {
			case "pve-ipam":
				summaries[i].Allocated++
			case "guest-agent", "neighbor", "dhcp-lease":
				summaries[i].Observed++
			}
		}
		if c.State == CellConflict {
			summaries[i].Conflicts++
		}
	}
	for i := range summaries {
		if summaries[i].Total > 0 {
			summaries[i].Utilization = float64(summaries[i].Allocated) / float64(summaries[i].Total)
		}
	}
	return summaries
}

func filterAllocsForCIDR(allocs []Allocation, cidr string) []Allocation {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil
	}
	var out []Allocation
	for _, a := range allocs {
		ip := net.ParseIP(a.IP)
		if ip != nil && ipnet.Contains(ip) {
			out = append(out, a)
		}
	}
	return out
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
