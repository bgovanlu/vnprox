// SPDX-License-Identifier: Apache-2.0

// TanStack Query hooks for T-3903's route explorer (docs/development.md's
// TypeScript standards: "server state via TanStack Query only — no fetch
// in components"). Unlike the live, polling Conntrack/Flow explorers, a
// routing table is not a fast-changing live view the way a connection
// table is — it changes on interface/route configuration events, not
// continuously — so these queries are on-demand (refetched on node/query
// change, no `refetchInterval`), matching GET /sdn/evpn/status' own
// on-demand (not polling) cadence.
import { useQuery } from "@tanstack/react-query";
import { fetchRouteLookup, fetchRouteNodes, fetchRouteSnapshot } from "../api/route";
import type { RouteLookupResult, RouteNodesResponse, RouteSnapshot } from "../api/types";

export const ROUTE_NODES_QUERY_KEY = ["route", "nodes"] as const;

export function useRouteNodesQuery() {
  return useQuery<RouteNodesResponse>({
    queryKey: ROUTE_NODES_QUERY_KEY,
    queryFn: fetchRouteNodes,
  });
}

export function routeSnapshotQueryKey(node: string) {
  return ["route", "snapshot", node] as const;
}

/** GET /route/snapshot?node= — enabled only once a node is selected (an
 * empty node string is a valid "ask for the local node" API call, but the
 * UI always has a concrete node selected once GET /route/nodes resolves,
 * so there is no meaningful "no node chosen yet" state to query for). */
export function useRouteSnapshotQuery(node: string, enabled = true) {
  return useQuery<RouteSnapshot>({
    queryKey: routeSnapshotQueryKey(node),
    queryFn: () => fetchRouteSnapshot(node),
    enabled: enabled && node !== "",
  });
}

export function routeLookupQueryKey(node: string, dst: string, iface: string) {
  return ["route", "lookup", node, dst, iface] as const;
}

/** GET /route/lookup?node=&dst=&iface= — enabled only once dst is
 * non-empty (the API 400s on a missing dst; the query simply doesn't run
 * until the operator has typed something to look up). */
export function useRouteLookupQuery(node: string, dst: string, iface: string, enabled = true) {
  return useQuery<RouteLookupResult>({
    queryKey: routeLookupQueryKey(node, dst, iface),
    queryFn: () => fetchRouteLookup(node, dst, iface || undefined),
    enabled: enabled && dst.trim() !== "",
    retry: false, // an invalid-address 400 is a caller-input state, not a transient failure to retry
  });
}
