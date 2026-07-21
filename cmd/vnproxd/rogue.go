package main

import (
	"context"
	"log/slog"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/ipam"
)

// neighborObserver is the read surface the rogue adapter needs from
// internal/neighbor.Service: the cluster-wide resolved ARP/IPv6-neighbor
// table as ipam.Observation values (the same seam ipam.Config.Neighbors
// already consumes). *neighbor.Service satisfies it directly.
type neighborObserver interface {
	Neighbors(ctx context.Context) ([]ipam.Observation, error)
}

// rogueScanAdapter builds T-1605's findings.RogueScan each findings cycle from
// the collectors already gathering L2 data. Today it wires the one feed that
// exists in production: T-805's cluster-wide neighbor table, which drives
// arp_spoof_suspected's churn detection. The DHCP-offer and IPv6-RA feeds (and
// their legitimate-source config-truth sets) are left empty on purpose —
//
//   - a raw DHCP-traffic / lease-file *server*-identity observation collector
//     does not exist yet, so rogue_dhcp_server has no offers to evaluate and
//     stays a documented no-op until one lands, and
//   - the IPv6-RA feed is T-1404's deliverable (Phase 14), additive here:
//     unexpected_ra is a real no-op against an empty feed until T-1404 ships.
//
// Both are honest no-ops rather than false negatives — the check logic and its
// tests are complete (internal/findings/health_rogue.go); only the production
// observation source is pending. The fourth check,
// unknown_mac_protected_segment, is graph+config-derived and needs no feed
// here — the engine runs it directly from the inventory snapshot and the
// [security] protected_segments list.
//
// A nil neighbors reader (degraded startup, no peer client) makes RogueScan
// return an empty scan, the same nil-safe degradation every other findings
// adapter uses.
type rogueScanAdapter struct {
	baseCtx   context.Context //nolint:containedctx // daemon shutdown ctx — see ipamFindingsAdapter.baseCtx
	neighbors neighborObserver
	logger    *slog.Logger
}

func (a rogueScanAdapter) RogueScan() findings.RogueScan {
	scan := findings.RogueScan{}
	if a.neighbors == nil {
		return scan
	}
	ctx, cancel := findingsAdapterCtx(a.baseCtx)
	defer cancel()
	obs, err := a.neighbors.Neighbors(ctx)
	if err != nil {
		a.logger.Warn("findings: reading neighbor table for rogue detection", "error", err)
		return scan
	}
	scan.Neighbors = make([]findings.NeighborObservation, 0, len(obs))
	for _, o := range obs {
		if o.IP == "" || o.MAC == "" {
			continue
		}
		scan.Neighbors = append(scan.Neighbors, findings.NeighborObservation{IP: o.IP, MAC: o.MAC})
	}
	return scan
}
