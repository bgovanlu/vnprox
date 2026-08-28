// SPDX-License-Identifier: Apache-2.0

// Polls GET /wireguard/tunnels while the "WireGuard" map layer is active
// (T-1402) — mirrors mtuProbeQueries.ts's useMTUProbeResultsQuery exactly:
// no WS push exists for this data (docs/api.md's WireGuard section
// documents none), so a plain refetch-interval poll is the whole freshness
// mechanism, same "only fetch while genuinely paintable" scope every other
// topology overlay layer already uses.
import { useQuery } from "@tanstack/react-query";
import { fetchWireGuardTunnels } from "../api/wireguard";
import type { WireGuardTunnel } from "../api/types";

export const wireGuardTunnelsKey = ["wireguard-tunnels"] as const;

/** Handshakes re-occur roughly every 2 minutes on a live tunnel (T-1401's
 * WgHandshakeStaleThreshold doc comment) — 30s keeps the "healthy" ->
 * "stale" transition reasonably prompt without hammering the read route. */
const REFETCH_MS = 30_000;

export function useWireGuardTunnelsQuery(
  enabled: boolean,
): { data: WireGuardTunnel[] | undefined; isLoading: boolean; isError: boolean } {
  const { data, isLoading, isError } = useQuery({
    queryKey: wireGuardTunnelsKey,
    queryFn: fetchWireGuardTunnels,
    enabled,
    staleTime: 10_000,
    refetchInterval: enabled ? REFETCH_MS : false,
  });
  return { data, isLoading, isError };
}
