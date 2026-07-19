// TanStack Query hooks for T-1403's Edge & NAT cockpit (docs/development.md's
// TypeScript standards: "server state via TanStack Query only — no fetch in
// components"). No WS event backs either route (a config-derived read, not
// a streamed delta), so "live" here means polling on a moderate interval —
// the same pattern GET /sdn's own view already uses (sdn/queries.ts).
import { useQuery } from "@tanstack/react-query";
import { fetchEdgeNAT, fetchEdgeRoutes } from "../api/edge";
import type { EdgeNATView, EdgeRoutesView } from "../api/types";

export const EDGE_REFETCH_MS = 15_000;

export function useEdgeRoutesQuery() {
  return useQuery<EdgeRoutesView>({
    queryKey: ["edge", "routes"],
    queryFn: fetchEdgeRoutes,
    refetchInterval: EDGE_REFETCH_MS,
  });
}

export function useEdgeNATQuery() {
  return useQuery<EdgeNATView>({
    queryKey: ["edge", "nat"],
    queryFn: fetchEdgeNAT,
    refetchInterval: EDGE_REFETCH_MS,
  });
}
