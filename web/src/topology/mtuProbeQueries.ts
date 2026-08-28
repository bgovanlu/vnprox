// SPDX-License-Identifier: Apache-2.0

// Polls GET /mtuprobe/results while the "Verified MTU" layer is active
// (T-1306) — like latMeshQueries.ts's heatmap poll, there is no WS event
// for this data (docs/api.md's Path MTU prober section documents no such
// push), so a plain refetch-interval poll is the whole freshness mechanism.
import { useQuery } from "@tanstack/react-query";
import { fetchMTUProbeResults } from "../api/mtuprobe";
import type { MTUProbeResult } from "../api/types";

export const mtuProbeResultsKey = ["mtuprobe-results"] as const;

/** REFETCH_MS is deliberately much coarser than latMeshQueries.ts's 10s
 * (mirrors internal/mtuprobe.DefaultProbeIntervalSec, 300s, vs
 * internal/latmesh.DefaultProbeIntervalSec, 10s) — polling faster than the
 * server itself produces new readings would just refetch the same data. */
const REFETCH_MS = 60_000;

export function useMTUProbeResultsQuery(enabled: boolean): { data: MTUProbeResult[] | undefined; isLoading: boolean } {
  const { data, isLoading } = useQuery({
    queryKey: mtuProbeResultsKey,
    queryFn: fetchMTUProbeResults,
    enabled,
    staleTime: 30_000,
    refetchInterval: enabled ? REFETCH_MS : false,
  });
  return { data, isLoading };
}
