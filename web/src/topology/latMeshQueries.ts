// SPDX-License-Identifier: Apache-2.0

// Polls GET /latmesh/heatmap while the "Latency" paint mode is active
// (T-1303) — unlike metricsQueries.ts's live rates, there is no WS event
// for the heatmap (docs/api.md's Latency mesh section documents no such
// push), so a plain refetch-interval poll is the whole freshness
// mechanism, matching useMetricsHistoryQuery's own "no WS event, poll on
// an interval" shape.
import { useQuery } from "@tanstack/react-query";
import { fetchLatMeshHeatmap } from "../api/latmesh";
import type { LatMeshLink } from "../api/types";

export const latMeshHeatmapKey = ["latmesh-heatmap"] as const;

/** REFETCH_MS matches internal/latmesh.DefaultProbeIntervalSec (10s) —
 * polling faster than the server itself produces new samples would just
 * refetch the same reading. */
const REFETCH_MS = 10_000;

export function useLatMeshHeatmapQuery(enabled: boolean): { data: LatMeshLink[] | undefined; isLoading: boolean } {
  const { data, isLoading } = useQuery({
    queryKey: latMeshHeatmapKey,
    queryFn: fetchLatMeshHeatmap,
    enabled,
    staleTime: 5_000,
    refetchInterval: enabled ? REFETCH_MS : false,
  });
  return { data, isLoading };
}
