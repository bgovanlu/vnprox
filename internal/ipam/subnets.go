package ipam

import (
	"context"
	"net"
	"sort"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// Subnets builds docs/api.md's `GET /ipam/subnets` response: every SDN
// subnet (live from PVE, docs/features/ipam.md §2), plus every detected
// non-SDN subnet derived from a bridge address (read-only).
func (s *Service) Subnets(ctx context.Context) (SubnetsResponse, error) {
	snap := s.inv.Snapshot()

	sdnInfo, err := s.sdnSubnets(ctx)
	if err != nil {
		return SubnetsResponse{}, err
	}
	allocByCIDR, err := s.allocationsByCIDR(ctx)
	if err != nil {
		return SubnetsResponse{}, err
	}
	obs := s.enrichmentObservations(ctx, snap)
	known := knownGuestsFromSnapshot(snap)

	sdnCIDRs := make([]string, 0, len(sdnInfo))
	items := make([]Subnet, 0, len(sdnInfo))
	for _, info := range sdnInfo {
		sdnCIDRs = append(sdnCIDRs, info.cidr)
		cellObs := observationsForCIDR(info.cidr, obs)
		cells, conflicts := mergeSubnet(allocByCIDR[info.cidr], cellObs, known, info.gateway)
		total, _, _ := subnetAddrCount(info.cidr)
		row := Subnet{
			CIDR: info.cidr, Zone: info.zone, Vnet: info.vnet, Gateway: info.gateway,
			Source: "sdn", DHCPEnabled: info.dhcp, Total: total,
		}
		summarize(&row, cells, conflicts)
		items = append(items, row)
	}

	items = append(items, nonSDNSubnets(snap, sdnCIDRs, obs)...)
	items = append(items, s.externalSubnetRows(ctx)...)

	sort.Slice(items, func(i, j int) bool { return items[i].CIDR < items[j].CIDR })
	return SubnetsResponse{Items: items, GeneratedAt: s.now().Unix()}, nil
}

// summarize fills row's Allocated/Observed/Conflicts/Utilization counts
// from a merged cell map plus its conflict findings. Allocated/Observed
// count every cell touched by that source (Cell.Sources), not a
// mutually-exclusive partition — a "both"/conflict cell counts toward both,
// which is the more useful signal for "how much of this subnet does each
// source know about" than an artificially exclusive split would be.
func summarize(row *Subnet, cells map[string]Cell, conflicts []Conflict) {
	for _, c := range cells {
		for _, src := range c.Sources {
			switch src {
			case "pve-ipam":
				row.Allocated++
			case "guest-agent", "neighbor", "dhcp-lease":
				row.Observed++
			}
		}
	}
	row.Conflicts = len(conflicts)
	if row.Total > 0 {
		row.Utilization = float64(row.Allocated) / float64(row.Total)
	}
}

// nonSDNSubnets derives docs/features/ipam.md §2's read-only "detected
// non-SDN subnets" list from every Bridge entity's declared addresses,
// deduplicated by network (not by the bridge's own host address, which
// legitimately differs node-to-node within the same L2 subnet — e.g. a
// 3-node cluster's identically-purposed management bridge each carrying
// its own /24 host address) and excluding any network an SDN subnet
// already covers.
func nonSDNSubnets(snap inventory.Snapshot, sdnCIDRs []string, obs []Observation) []Subnet {
	seen := map[string]*Subnet{}
	order := make([]string, 0)

	for _, e := range snap.All() {
		br, ok := e.(*inventory.Bridge)
		if !ok {
			continue
		}
		for _, addr := range br.Addresses {
			ip, ipnet, err := net.ParseCIDR(addr)
			if err != nil {
				continue
			}
			network := ipnet.String()
			if coveredBySDN(network, sdnCIDRs) {
				continue
			}
			if _, exists := seen[network]; exists {
				continue
			}
			total, _, _ := subnetAddrCount(network)
			row := &Subnet{
				CIDR: network, Node: br.Node, Gateway: ip.String(),
				Source: "bridge", ReadOnly: true, Total: total,
			}
			for _, o := range observationsForCIDR(network, obs) {
				_ = o
				row.Observed++
			}
			if row.Total > 0 {
				row.Utilization = float64(row.Observed) / float64(row.Total)
			}
			seen[network] = row
			order = append(order, network)
		}
	}

	out := make([]Subnet, 0, len(order))
	for _, network := range order {
		out = append(out, *seen[network])
	}
	return out
}

func coveredBySDN(network string, sdnCIDRs []string) bool {
	_, target, err := net.ParseCIDR(network)
	if err != nil {
		return false
	}
	for _, cidr := range sdnCIDRs {
		_, ipnet, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if ipnet.String() == target.String() {
			return true
		}
	}
	return false
}
