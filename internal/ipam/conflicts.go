package ipam

import "context"

// SubnetConflict pairs an IPAM conflict with the subnet CIDR it was found
// in. The unified findings adapter (cmd/vnproxd) needs the CIDR to build a
// stable per-conflict finding id; the Conflict itself carries no subnet.
type SubnetConflict struct {
	CIDR     string
	Conflict Conflict
}

// Conflicts returns every current IPAM conflict across all SDN subnets,
// each tagged with its subnet. It reuses the exact same per-subnet merge
// (mergeSubnet) that Subnets()'s conflict *count* is derived from, so the
// unified findings stream and the IPAM page's own conflict counts can never
// disagree. Non-SDN "detected" subnets have no PVE-IPAM allocations to
// conflict against, so — like Subnets()'s own conflict computation — only
// SDN subnets are scanned.
func (s *Service) Conflicts(ctx context.Context) ([]SubnetConflict, error) {
	snap := s.inv.Snapshot()

	sdnInfo, err := s.sdnSubnets(ctx)
	if err != nil {
		return nil, err
	}
	allocByCIDR, err := s.allocationsByCIDR(ctx)
	if err != nil {
		return nil, err
	}
	obs := s.enrichmentObservations(ctx, snap)
	known := knownGuestsFromSnapshot(snap)

	var out []SubnetConflict
	for _, info := range sdnInfo {
		cellObs := observationsForCIDR(info.cidr, obs)
		_, conflicts := mergeSubnet(allocByCIDR[info.cidr], cellObs, known, info.gateway)
		for _, c := range conflicts {
			out = append(out, SubnetConflict{CIDR: info.cidr, Conflict: c})
		}
	}
	return out, nil
}
