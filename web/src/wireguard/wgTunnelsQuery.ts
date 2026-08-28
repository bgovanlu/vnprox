// SPDX-License-Identifier: Apache-2.0

// Polls GET /wireguard/tunnels while the "WireGuard" map layer is active
// (T-1402) — mirrors mtuProbeQueries.ts's useMTUProbeResultsQuery exactly:
// no WS push exists for this data (docs/api.md's WireGuard section
// documents none), so a plain refetch-interval poll is the whole freshness
// mechanism, same "only fetch while genuinely paintable" scope every other
// topology overlay layer already uses.
import { useQuery, type UseQueryResult } from "@tanstack/react-query";
import { fetchWireGuardPeerConfig, fetchWireGuardPubkey, fetchWireGuardTunnels } from "../api/wireguard";
import type { WireGuardTunnel } from "../api/types";

export const wireGuardTunnelsKey = ["wireguard-tunnels"] as const;

/** Handshakes re-occur roughly every 2 minutes on a live tunnel (T-1401's
 * WgHandshakeStaleThreshold doc comment) — 30s keeps the "healthy" ->
 * "stale" transition reasonably prompt without hammering the read route. */
const REFETCH_MS = 30_000;

export function useWireGuardTunnelsQuery(
  enabled: boolean,
): {
  data: WireGuardTunnel[] | undefined;
  isLoading: boolean;
  isError: boolean;
  /** T-4209: exposed so the page-level "could not load" empty state can
   * offer a real retry action instead of just "reload the page". */
  refetch: UseQueryResult<WireGuardTunnel[]>["refetch"];
} {
  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: wireGuardTunnelsKey,
    queryFn: fetchWireGuardTunnels,
    enabled,
    staleTime: 10_000,
    refetchInterval: enabled ? REFETCH_MS : false,
  });
  return { data, isLoading, isError, refetch };
}

/** T-4015: the general management surface's "view public key" affordance —
 * fetched only while the viewer dialog is open (`enabled`), never eagerly,
 * since it's one more round trip the tunnel list itself doesn't need. Only
 * ever the DERIVED public key: GET /wireguard/tunnels/{id}/pubkey has no
 * route or param that can return the private key (internal/api/wireguard.go's
 * own doc comment on WireGuardService.PublicKey). */
export function useWireGuardPubkeyQuery(id: string, enabled: boolean) {
  return useQuery({
    queryKey: ["wireguard-tunnels", id, "pubkey"] as const,
    queryFn: () => fetchWireGuardPubkey(id),
    enabled,
    staleTime: 60_000,
  });
}

/** The exportable wg-quick config block an external peer would install on
 * its own side — the far side's own private key is left a placeholder
 * (vnprox never holds it), and this tunnel's own private key never appears
 * in the rendered text (docs/api.md's WireGuard section). */
export function useWireGuardPeerConfigQuery(id: string, enabled: boolean) {
  return useQuery({
    queryKey: ["wireguard-tunnels", id, "peer-config"] as const,
    queryFn: () => fetchWireGuardPeerConfig(id),
    enabled,
    staleTime: 60_000,
  });
}
