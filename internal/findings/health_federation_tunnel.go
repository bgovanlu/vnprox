// SPDX-License-Identifier: Apache-2.0

// health_federation_tunnel.go implements T-1407's tunnel_down_peer_unreachable
// finding (source "federation"): a federated cluster that declares itself
// reachable only via a specific T-1401-managed WireGuard tunnel
// (federation.Cluster.WgTunnelID) whose live handshake state has gone
// stale. This is the single named signal that replaces the per-surface
// partial/failedClusters noise plain PVE-API unreachability would otherwise
// raise across topology, audit, and IPAM-conflict reads simultaneously —
// internal/federation.Aggregator suppresses those individually via its own
// TunnelHealth seam (aggregator.go) once a cluster's tunnel is down,
// deferring entirely to this one finding. Both key off the identical
// WgTunnelHasFreshHandshake definition (health_wireguard.go), so they can
// never disagree about which tunnels are down (T-1407 AC2).

package findings

import (
	"fmt"
	"sort"
	"time"
)

// CheckTunnelDownPeerUnreachable is T-1407's finding check name.
const CheckTunnelDownPeerUnreachable = "tunnel_down_peer_unreachable"

const federationDocsLink = "docs/api.md#federation"

// FederatedCluster is one attached federation cluster that declares itself
// reachable only via a specific T-1401-managed WireGuard tunnel, pre-resolved
// to that tunnel's (node, ifName) so this producer can correlate it against
// the live WireGuard state WGProvider already exposes — without this package
// depending on internal/federation or internal/store directly. cmd/vnproxd's
// federationTunnelAdapter builds these from federation.Cluster.WgTunnelID.
type FederatedCluster struct {
	ClusterID    string
	ClusterName  string
	TunnelNode   string
	TunnelIfName string
}

// FederationProvider is the findings engine's seam onto federation's cluster
// registry — just enough to name a tunnel-linked cluster in the
// tunnel_down_peer_unreachable finding (T-1407). A nil provider skips this
// producer entirely, the same degradation every other optional Config field
// uses.
type FederationProvider interface {
	TunnelLinkedClusters() ([]FederatedCluster, error)
}

// federationTunnelFindings flags every tunnel-linked attached cluster whose
// linked tunnel currently has no peer with a fresh handshake,
// hysteresis-debounced like wg_handshake_stale (T-1407 AC2's "fixture-driven
// handshake goes stale past threshold" case). A cluster linked to a healthy
// tunnel never fires (T-1407 AC1). No fix/auto-remediation is ever attached
// (T-1407 AC4, Finding.Fixable stays false) — the only action a human gets
// is the docs link into the tunnel's own changeset editor; nothing here
// re-applies or restarts a tunnel automatically.
func federationTunnelFindings(prov FederationProvider, wg WGProvider, db *debouncer, now time.Time) []Finding {
	if prov == nil || wg == nil {
		return nil
	}
	clusters, err := prov.TunnelLinkedClusters()
	if err != nil || len(clusters) == 0 {
		return nil
	}
	state := wg.WireGuardState()
	var out []Finding
	live := map[string]bool{}
	for _, c := range clusters {
		live[c.ClusterID] = true
		breach := !WgTunnelHasFreshHandshake(state, c.TunnelNode, c.TunnelIfName, now)
		if !db.Evaluate(c.ClusterID, breach, wgRiseCycles, wgFallCycles) {
			continue
		}
		detail := fmt.Sprintf("Federated cluster %q is reachable only via WireGuard tunnel %s on %s, whose handshake is stale past %s — the tunnel is down, so this cluster's aggregate reads (topology, audit, IPAM) show no data instead of failing individually on each",
			c.ClusterName, c.TunnelIfName, c.TunnelNode, WgHandshakeStaleThreshold)
		out = append(out, Finding{
			ID:       "federation:" + CheckTunnelDownPeerUnreachable + "|" + c.ClusterID,
			Source:   SourceFederation,
			Check:    CheckTunnelDownPeerUnreachable,
			Severity: SeverityWarning,
			Detail:   detail,
			Nodes:    []string{c.TunnelNode},
			Refs:     []string{"wg-tunnel:" + c.TunnelNode + ":" + c.TunnelIfName},
			DocsLink: federationDocsLink,
		})
	}
	db.Prune(live)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
