// SPDX-License-Identifier: Apache-2.0

// T-4015: the "is this tunnel up" verdict for the general (non-federation)
// WireGuard management surface. Three states, deliberately not two —
// exactly topology/federation/interconnects.ts's InterconnectState
// vocabulary (T-3909), generalized from "federation-linked tunnels only" to
// "every tunnel this node manages":
//
//   - "up"      — at least one peer handshaked within the stale threshold.
//   - "down"    — the tunnel's live state is known and no peer is fresh
//                 (including a tunnel with zero peers configured yet: its
//                 state IS known — nothing is up — so "down" is the honest
//                 read, not "unknown").
//   - "unknown" — this node's own GET /wireguard/tunnels read hasn't
//                 resolved yet or failed. A tunnel whose state cannot be
//                 read is not "down": collapsing the two would misreport a
//                 local read/permissions problem as "the tunnel is broken".
//
// Reuses interconnects.ts's own `tunnelHasFreshHandshake` (itself mirroring
// `internal/findings.WgTunnelHasFreshHandshake` exactly) rather than a
// second implementation of the same freshness check — a third definition of
// "is this tunnel up" would be a bug waiting to happen (this task's brief,
// verbatim). Also reuses wgEdgeStatus.ts's WG_HANDSHAKE_STALE_THRESHOLD_SEC,
// the same constant the T-1402 map layer and the T-3909 interconnect edges
// already share, so this is the third consumer of one threshold, not a
// fourth number to keep in sync by hand.
import type { WireGuardTunnel } from "../api/types";
import { tunnelHasFreshHandshake } from "../topology/federation/interconnects";
import { WG_HANDSHAKE_STALE_THRESHOLD_SEC } from "./wgEdgeStatus";

export type WgTunnelState = "up" | "down" | "unknown";

/** `tunnelsUnavailable` is true while the local tunnel list is loading or
 * failed to load — see wgTunnelsQuery.ts's `isLoading`/`isError`. `nowUnix`
 * is injectable so tests are deterministic (no real-clock flakiness),
 * mirroring `deriveInterconnects`'s own convention. */
export function wgTunnelState(
  tunnel: WireGuardTunnel,
  tunnelsUnavailable: boolean,
  nowUnix: number = Math.floor(Date.now() / 1000),
  staleThresholdSec: number = WG_HANDSHAKE_STALE_THRESHOLD_SEC,
): WgTunnelState {
  if (tunnelsUnavailable) return "unknown";
  return tunnelHasFreshHandshake(tunnel, nowUnix, staleThresholdSec) ? "up" : "down";
}
