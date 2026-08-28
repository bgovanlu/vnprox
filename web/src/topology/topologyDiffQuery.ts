// SPDX-License-Identifier: Apache-2.0

// T-2704: the point-in-time topology diff query for the map overlay.
//
// Kept in its own module (the mtuProbeQueries.ts / latMeshQueries.ts
// convention) so TopologyPage imports a hook rather than growing another
// inline useQuery, and so the "only fetch when a range is actually selected"
// rule lives in one place: a map with no historical range selected must not
// poll a diff endpoint at all.
import { useQuery } from "@tanstack/react-query";

import { fetchTopologyDiff, type TopologyDiffResponse } from "../api/topologyDiff";

/** Fetches the diff for a selected range, or nothing at all when either end
 * is unset. Not polled: a historical range's answer only moves when `to` is
 * the live sentinel, and even then a manual refresh is the honest interaction
 * — a silently re-fetching diff would shift under an operator mid-read. */
export function useTopologyDiffQuery(from: string, to: string) {
  const enabled = from !== "" && to !== "";
  return useQuery<TopologyDiffResponse>({
    queryKey: ["topology-diff", from, to],
    queryFn: () => fetchTopologyDiff(from, to),
    enabled,
    staleTime: Number.POSITIVE_INFINITY,
  });
}
