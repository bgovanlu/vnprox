// SPDX-License-Identifier: Apache-2.0

// T-1402: resolves one WireGuard peer's live status into the map-edge
// paint the "WireGuard" topology layer uses — the three states T-1401's
// findings define (docs/features/topology.md's new wg-tunnel edge kind):
//
//   - healthy         -> status "ok", no "drift" badge
//   - handshake-stale -> status "degraded" (amber, the same EntityEdge.tsx
//                        every other degraded edge/node already paints)
//   - endpoint-drift  -> "drift" badge added (dashed outline — the exact
//                        existing convention EntityEdge.tsx/canvasDraw.ts
//                        already key off `badges.includes("drift")` for
//                        every other drift finding on the map)
//
// Status and the drift badge are independent axes, not mutually exclusive
// alternatives — mirroring EntityNode.tsx's own documented convention
// ("a 'down'/'degraded' node can also carry an open drift finding"). A
// peer can in principle be both stale AND drifted; this file does not
// invent a precedence rule between them because none exists upstream —
// each of T-1401's two findings (wg_handshake_stale, wg_endpoint_drift) is
// computed independently from the same live poll.
//
// Thresholds/semantics mirror internal/findings/health_wireguard.go
// exactly (WgHandshakeStaleThreshold = 5m; a peer with no handshake age at
// all — lastHandshakeUnix absent or 0 — is NOT stale, so a freshly-created
// tunnel renders healthy immediately, never amber on its very first poll).
import type { EntityStatus, WireGuardPeer } from "../api/types";

/** Mirrors internal/findings/health_wireguard.go's WgHandshakeStaleThreshold
 * (5 minutes), in seconds — kept in sync by hand like every other Go<->TS
 * constant pair this codebase has no shared-codegen for (see docs/features/
 * topology.md §4's LOD band-threshold precedent for the same convention). */
export const WG_HANDSHAKE_STALE_THRESHOLD_SEC = 5 * 60;

export interface WgPeerEdgePaint {
  status: EntityStatus;
  /** Additive edge badges — always includes "wg"; "drift" is added only
   * when the peer's live endpoint disagrees with its configured one. */
  badges: string[];
}

/** Pure function: given one peer's live fields and the current instant,
 * returns the status/badges its map edge should paint. `nowUnix` is
 * injectable so tests are deterministic (no real-clock flakiness). */
export function wgPeerEdgePaint(
  peer: Pick<WireGuardPeer, "lastHandshakeUnix" | "endpointDrifted">,
  nowUnix: number,
  staleThresholdSec: number = WG_HANDSHAKE_STALE_THRESHOLD_SEC,
): WgPeerEdgePaint {
  const badges = ["wg"];
  if (peer.endpointDrifted) badges.push("drift");

  const age = peer.lastHandshakeUnix ? nowUnix - peer.lastHandshakeUnix : undefined;
  const stale = age !== undefined && age > staleThresholdSec;
  return { status: stale ? "degraded" : "ok", badges };
}

/** Worst-of rollup across every peer of one tunnel, for a tunnel-level
 * summary badge (a multi-peer tunnel's own node chip, if ever rendered) —
 * "degraded" wins over "ok"; "drift" is present if any peer drifted. Not
 * currently used by wgTunnelEdges.ts (which paints one edge per peer, not
 * one per tunnel), kept here as the natural aggregate for a future
 * tunnel-level summary view. */
export function worstWgPeerPaint(paints: readonly WgPeerEdgePaint[]): WgPeerEdgePaint {
  let status: EntityStatus = "ok";
  let drift = false;
  for (const p of paints) {
    if (p.status === "degraded") status = "degraded";
    if (p.badges.includes("drift")) drift = true;
  }
  const badges = ["wg"];
  if (drift) badges.push("drift");
  return { status, badges };
}
