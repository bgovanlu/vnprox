// T-1402: turns GET /wireguard/tunnels' live tunnels into the map's
// "WireGuard" layer overlay — synthetic {nodes, edges} merged into the real
// topology canvas via TopologyPage's existing extraNodes/extraEdges seam
// (the same injection point T-1003's expanded guest-group pills already
// use), a pure `WireGuardTunnel[] -> {nodes, edges}` transform so it is
// directly Vitest-able without mounting React Flow — mirrors mtuOverlay.ts/
// latencyMode.ts's identical "pure compute, injected node-name resolver"
// shape exactly.
//
// A tunnel's own node isn't rendered as a new synthetic node — it anchors
// to the REAL rendered per-node entity (the first-by-id element in that
// node's own nodeGroup band, the same anchor the latency/MTU overlays now
// resolve onto — see topology/nodeAnchor.ts) via the injected
// `nodeIdForName` resolver, so this overlay never duplicates a node the
// base topology already renders. The far side
// of a tunnel, though, is very often NOT a rendered inventory entity at
// all — an external/road-warriorless peer, or (per this task's federation
// seam note) a node belonging to a cluster this repo cannot yet resolve —
// so every peer gets its own synthetic "standalone endpoint" node, always,
// never conditionally: a peer whose public key happens to match another
// locally-known tunnel's own node is still rendered as its own endpoint
// node here (T-1401's read surface has no cross-node peer fan-out to
// correlate two tunnels' records into one node-to-node edge yet — see this
// file's own doc comment on WG_ENDPOINT_KIND for the seam this leaves for
// T-1407/cluster wg routing to close).
import type { EntityStatus, TopologyEdge, TopologyNode, WireGuardTunnel } from "../api/types";
import { wgPeerEdgePaint } from "./wgEdgeStatus";

/** Synthetic node kind for a wg peer's far-side endpoint — never a real
 * inventory Ref, so a click-through never tries to open the inspector on
 * it (mirrors the wizard preview's "wizard-preview:"-namespaced synthetic
 * nodes, which are excluded from the inspector the same way). */
export const WG_ENDPOINT_KIND = "wg-external-endpoint";

/** Synthetic node id for one tunnel/peer pair's far-side endpoint —
 * namespaced so it can never collide with a real inventory Ref (every real
 * Ref's kind comes from inventory's closed knownKinds set, which this
 * prefixed string never matches). */
export function wgEndpointNodeId(tunnelId: string, peerPublicKey: string): string {
  return `wg-endpoint:${tunnelId}:${peerPublicKey}`;
}

function endpointLabel(peer: WireGuardTunnel["peers"][number]): string {
  if (peer.endpoint) return peer.endpoint;
  if (peer.observedEndpoint) return peer.observedEndpoint;
  return `${peer.publicKey.slice(0, 8)}…`;
}

export interface WgTunnelOverlay {
  nodes: TopologyNode[];
  edges: TopologyEdge[];
}

/** Builds the WireGuard layer's overlay nodes/edges. `nodeIdForName`
 * resolves a tunnel's owning PVE node name to its rendered map node id
 * (undefined if that node isn't currently on the canvas — e.g. filtered
 * out, or the collector hasn't produced it yet); a tunnel whose own node
 * can't be resolved is skipped entirely, the same "no anchor, no overlay
 * edge" rule mtuOverlay.ts/latencyMode.ts already apply. `nowUnix`
 * defaults to the real clock but is injectable for deterministic tests. */
export function computeWgTunnelOverlay(
  tunnels: readonly WireGuardTunnel[],
  nodeIdForName: (nodeName: string) => string | undefined,
  nowUnix: number = Math.floor(Date.now() / 1000),
): WgTunnelOverlay {
  const nodes: TopologyNode[] = [];
  const edges: TopologyEdge[] = [];

  for (const tunnel of tunnels) {
    const fromId = nodeIdForName(tunnel.node);
    if (!fromId) continue;

    for (const peer of tunnel.peers) {
      const toId = wgEndpointNodeId(tunnel.id, peer.publicKey);
      const paint = wgPeerEdgePaint(peer, nowUnix);
      const nodeBadges = [...paint.badges];
      if (peer.external) nodeBadges.push("external");

      const status: EntityStatus = paint.status;
      nodes.push({
        id: toId,
        kind: WG_ENDPOINT_KIND,
        label: endpointLabel(peer),
        layer: "phys",
        // "" is the cluster-spanning-band sentinel (docs/features/
        // topology.md §3 / TopologyNode.nodeGroup's own doc comment) — an
        // external endpoint belongs to no per-node column, the same
        // placement every cluster-scoped SDN entity already uses.
        nodeGroup: "",
        status,
        badges: nodeBadges,
      });
      edges.push({ from: fromId, to: toId, kind: "wg-tunnel", status, badges: paint.badges });
    }
  }

  return { nodes, edges };
}
