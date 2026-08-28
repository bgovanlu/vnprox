// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/federation"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/store"
)

// federationTunnelAdapter is T-1407's composition-root bridge from
// federation's clusters.wg_tunnel_id linkage and the live WireGuard state to
// the two consumers that both need the identical "is this cluster's linked
// tunnel down" answer:
//   - internal/federation.Aggregator's TunnelHealth seam (suppresses the
//     per-surface partial/failedClusters noise a tunnel-down cluster would
//     otherwise raise across topology/audit/IPAM-conflict reads)
//   - internal/findings' own tunnel_down_peer_unreachable producer (the one
//     named finding that replaces that noise)
//
// Both ultimately call findings.WgTunnelHasFreshHandshake, so they can never
// disagree about which tunnels are down (T-1407 AC2).
//
// Lazily set, mirroring fwAnalyticsAdapter/mgmtStatusAdapter's pattern:
// setupFindings builds the findings.Engine — and federation.NewAggregator
// needs this same adapter for its TunnelHealth option — before federationSvc
// exists (server.go constructs federation after both, see its own call
// site). Wired in with its targets unset and filled via set() once
// federationSvc/wgRepo are built, always before the daemon starts serving
// requests or either consumer actually runs.
type federationTunnelAdapter struct {
	baseCtx context.Context //nolint:containedctx // neither findings.FederationProvider nor federation.TunnelHealth carry a ctx param (see findingsAdapterCtx's doc comment for why every lazily-set findings adapter in this package does this)
	svc     *federation.Service
	wgRepo  *store.WireGuardRepo
	wg      findings.WGProvider
	mu      sync.Mutex
}

func (a *federationTunnelAdapter) set(svc *federation.Service, wgRepo *store.WireGuardRepo, wg findings.WGProvider) {
	a.mu.Lock()
	a.svc, a.wgRepo, a.wg = svc, wgRepo, wg
	a.mu.Unlock()
}

func (a *federationTunnelAdapter) snapshot() (*federation.Service, *store.WireGuardRepo, findings.WGProvider) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.svc, a.wgRepo, a.wg
}

// TunnelLinkedClusters implements findings.FederationProvider. Returns
// nothing (not an error) if called before set — the same degrade-before-
// ready contract fwAnalyticsAdapter.Analytics documents. A cluster whose
// wg_tunnel_id no longer resolves (the tunnel was deleted after linking) is
// silently omitted — a dangling link is a config problem for the operator to
// notice via the ordinary cluster editor, not this producer's concern.
func (a *federationTunnelAdapter) TunnelLinkedClusters() ([]findings.FederatedCluster, error) {
	svc, wgRepo, _ := a.snapshot()
	if svc == nil || wgRepo == nil {
		return nil, nil
	}
	ctx, cancel := findingsAdapterCtx(a.baseCtx)
	defer cancel()
	clusters, err := svc.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []findings.FederatedCluster
	for _, c := range clusters {
		if c.WgTunnelID == "" {
			continue
		}
		tun, terr := wgRepo.GetTunnel(ctx, c.WgTunnelID)
		if terr != nil {
			continue
		}
		out = append(out, findings.FederatedCluster{
			ClusterID: c.ID, ClusterName: c.Name, TunnelNode: tun.Node, TunnelIfName: tun.IfName,
		})
	}
	return out, nil
}

// TunnelDown implements internal/federation.TunnelHealth: whether the
// tunnel identified by tunnelID (a clusters.wg_tunnel_id value, i.e. a
// wireguard_tunnels.id) currently has no peer with a fresh handshake. An
// unresolvable tunnelID (deleted after linking), or a call before set,
// reports NOT down — a dangling link degrades to ordinary PVE-reachability
// handling rather than silently and permanently hiding a cluster's data with
// no live signal explaining why.
func (a *federationTunnelAdapter) TunnelDown(tunnelID string) bool {
	_, wgRepo, wg := a.snapshot()
	if wgRepo == nil || wg == nil {
		return false
	}
	ctx, cancel := findingsAdapterCtx(a.baseCtx)
	defer cancel()
	tun, err := wgRepo.GetTunnel(ctx, tunnelID)
	if err != nil {
		return false
	}
	return !findings.WgTunnelHasFreshHandshake(wg.WireGuardState(), tun.Node, tun.IfName, time.Now())
}
