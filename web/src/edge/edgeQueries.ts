// TanStack Query hooks for T-1403's Edge & NAT cockpit (docs/development.md's
// TypeScript standards: "server state via TanStack Query only — no fetch in
// components"). No WS event backs either route (a config-derived read, not
// a streamed delta), so "live" here means polling on a moderate interval —
// the same pattern GET /sdn's own view already uses (sdn/queries.ts).
//
// T-1406 extends this same file with the Ingress visibility hooks
// (GET/POST/DELETE /ingress/targets, GET /ingress/status) — the Edge layer's
// WAN -> port-forward -> proxy guest -> backend guest chain is a join of
// this file's edge/nat data and ingress/status data, so both live in one
// query-hook module the EdgeCockpit page draws from.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { fetchEdgeNAT, fetchEdgeRoutes } from "../api/edge";
import { createIngressTarget, deleteIngressTarget, fetchIngressStatus, fetchIngressTargets } from "../api/ingress";
import type { EdgeNATView, EdgeRoutesView, IngressStatusView, IngressTargetCreateRequest, IngressTargetsListResponse } from "../api/types";

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

export const INGRESS_TARGETS_QUERY_KEY = ["ingress", "targets"] as const;
export const INGRESS_STATUS_QUERY_KEY = ["ingress", "status"] as const;

export function useIngressTargetsQuery() {
  return useQuery<IngressTargetsListResponse>({
    queryKey: INGRESS_TARGETS_QUERY_KEY,
    queryFn: fetchIngressTargets,
    staleTime: 15_000,
  });
}

/** GET /ingress/status polls a bit slower than /edge/nat by default — a
 * live outbound HTTP call per configured target — so it deliberately
 * doesn't share EDGE_REFETCH_MS's cadence. */
export const INGRESS_STATUS_REFETCH_MS = 20_000;

export function useIngressStatusQuery() {
  return useQuery<IngressStatusView>({
    queryKey: INGRESS_STATUS_QUERY_KEY,
    queryFn: fetchIngressStatus,
    refetchInterval: INGRESS_STATUS_REFETCH_MS,
  });
}

export function useCreateIngressTargetMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (req: IngressTargetCreateRequest) => createIngressTarget(req),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: INGRESS_TARGETS_QUERY_KEY });
      void queryClient.invalidateQueries({ queryKey: INGRESS_STATUS_QUERY_KEY });
    },
  });
}

export function useDeleteIngressTargetMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => deleteIngressTarget(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: INGRESS_TARGETS_QUERY_KEY });
      void queryClient.invalidateQueries({ queryKey: INGRESS_STATUS_QUERY_KEY });
    },
  });
}
