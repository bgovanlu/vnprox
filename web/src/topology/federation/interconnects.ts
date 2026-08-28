// SPDX-License-Identifier: Apache-2.0

// T-3909: the "which attached clusters have a live WireGuard interconnect"
// derivation for the global map's edge layer. Pure `(clusters, tunnels) ->
// ClusterInterconnect[]` — Vitest-able without mounting the map, mirroring
// wgTunnelEdges.ts/wgEdgeStatus.ts's own "pure compute, injected clock" shape
// for the single-cluster WireGuard layer (T-1402); this reuses that file's
// exact stale-handshake threshold rather than redefining it.
//
// An interconnect only exists for a cluster with an *effective* WireGuard
// tunnel linkage (FederationCluster.wgTunnelId, resolved server-side from
// either the explicit `clusters.wg_tunnel_id` column or a tagged peer — see
// docs/api.md's Federation section, "Where a cluster's tunnel linkage comes
// from"). A cluster with none gets no edge at all — nothing is configured to
// draw, so it is omitted rather than reported "unknown".
//
// The tunnel itself is always LOCAL data: `Cluster.wgTunnelId` names "a
// T-1401 tunnel id this daemon manages" (docs/api.md), so its live state
// comes from THIS node's own `GET /wireguard/tunnels` — it is never fanned
// out to the remote cluster's own PVE API the way federationtopo.go's
// aggregator fans out cluster reads. That is why drawing interconnect edges
// needs no federationtopo.go extension: the two facts an edge needs (which
// cluster names which tunnel, and that tunnel's live handshake state) are
// already both exposed by existing, already-shipped, already-netRead-gated
// routes (GET /federation/clusters, GET /wireguard/tunnels).
import type { FederationCluster } from "../../api/federation";
import type { WireGuardTunnel } from "../../api/types";
import { WG_HANDSHAKE_STALE_THRESHOLD_SEC } from "../../wireguard/wgEdgeStatus";

/** Three states, deliberately not two. "down" — a configured tunnel with no
 * fresh handshake, the single most useful signal this view can show — must
 * never be confused with "unknown": this node simply has no live data for
 * the tunnel at all (the local `GET /wireguard/tunnels` read failed, the
 * caller lacks `netRead`, or the tunnel id it named isn't present in what
 * that route returned). "up" is a tunnel with at least one peer handshaked
 * within the stale threshold — the exact
 * `internal/findings.WgTunnelHasFreshHandshake` definition, mirrored here so
 * the map and the `tunnel_down_peer_unreachable` finding can never disagree
 * about which tunnels are healthy. */
export type InterconnectState = "up" | "down" | "unknown";

export interface ClusterInterconnect {
  clusterId: string;
  clusterName: string;
  tunnelId: string;
  tunnelSource: "explicit" | "peer";
  state: InterconnectState;
}

/** One tunnel's up/down verdict — mirrors
 * `internal/findings.WgTunnelHasFreshHandshake`/cmd/vnproxd's `TunnelDown`
 * adapter exactly (any one peer's handshake age within the threshold means
 * up), applied to the already-fetched `WireGuardTunnel` view instead of a
 * live `wg show ... dump` poll. */
function tunnelHasFreshHandshake(tunnel: WireGuardTunnel, nowUnix: number, staleThresholdSec: number): boolean {
  return tunnel.peers.some(
    (p) => p.lastHandshakeUnix !== undefined && p.lastHandshakeUnix > 0 && nowUnix - p.lastHandshakeUnix <= staleThresholdSec,
  );
}

/** Derives one `ClusterInterconnect` per attached cluster that has an
 * effective WireGuard tunnel linkage. `tunnelsUnavailable` is true when the
 * local `GET /wireguard/tunnels` read itself failed (or hasn't resolved
 * yet) — every linked cluster then reports "unknown" rather than silently
 * omitting the edge or guessing "down", so a permissions/transport problem
 * on THIS node is never misread as "the remote tunnel is down". This is the
 * per-cluster (and per-read) failure isolation the rest of federation's
 * surfaces already carry, applied to the one new signal this card adds:
 * one cluster's unresolved linkage, or a wholly failed local tunnel read,
 * degrades only the affected edges — every other cluster's capsule and
 * every other edge is unaffected. `nowUnix` is injectable so tests are
 * deterministic (no real-clock flakiness), mirroring `wgPeerEdgePaint`'s own
 * convention. */
export function deriveInterconnects(
  clusters: readonly FederationCluster[],
  tunnels: readonly WireGuardTunnel[] | undefined,
  tunnelsUnavailable: boolean,
  nowUnix: number = Math.floor(Date.now() / 1000),
  staleThresholdSec: number = WG_HANDSHAKE_STALE_THRESHOLD_SEC,
): ClusterInterconnect[] {
  const out: ClusterInterconnect[] = [];
  for (const c of clusters) {
    if (!c.wgTunnelId) continue;
    const tunnelId = c.wgTunnelId;
    const source = c.wgTunnelSource ?? "explicit";

    let state: InterconnectState;
    if (tunnelsUnavailable || tunnels === undefined) {
      state = "unknown";
    } else {
      const tunnel = tunnels.find((t) => t.id === tunnelId);
      state = !tunnel ? "unknown" : tunnelHasFreshHandshake(tunnel, nowUnix, staleThresholdSec) ? "up" : "down";
    }

    out.push({ clusterId: c.id, clusterName: c.name, tunnelId, tunnelSource: source, state });
  }
  return out;
}
