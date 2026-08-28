// SPDX-License-Identifier: Apache-2.0

// TanStack Query hook for T-1305's Conntrack explorer
// (docs/development.md's TypeScript standards: "server state via TanStack
// Query only — no fetch in components"). Unlike the Flow Explorer
// (flows/flowsQueries.ts), there is no WS event for conntrack — a live
// conntrack table is a point-in-time kernel read with no natural "delta"
// shape to push (docs/api.md's Conntrack section documents no
// `conntrack.*` WS event, on purpose), so "live" here means polling on a
// short interval, the same pattern this codebase already uses for the SDN
// EVPN status view (sdn/queries.ts) and the metrics safety-net refetch
// (topology/metricsQueries.ts).
import { useQuery } from "@tanstack/react-query";
import { fetchConntrack, type ConntrackFilter } from "../api/conntrack";
import type { ConntrackPage } from "../api/types";

/** Polling cadence: frequent enough that "live" reads honestly (a
 * connection that closed a few seconds ago should disappear promptly), not
 * so frequent it hammers every cluster peer on every open explorer tab. */
export const CONNTRACK_REFETCH_MS = 5_000;

export function conntrackQueryKey(filter: ConntrackFilter) {
  return ["conntrack", filter] as const;
}

/** GET /conntrack?node=&guest=&srcIp=&dstIp=&port=&state= — polls on
 * CONNTRACK_REFETCH_MS while `enabled` (default true; the explorer's own
 * unmount / a closed inspector tab stops polling automatically via
 * TanStack Query's usual unsubscribe-on-unmount behavior). */
export function useConntrackQuery(filter: ConntrackFilter, enabled = true) {
  return useQuery<ConntrackPage>({
    queryKey: conntrackQueryKey(filter),
    queryFn: () => fetchConntrack(filter),
    staleTime: 0,
    refetchInterval: enabled ? CONNTRACK_REFETCH_MS : false,
    enabled,
  });
}
