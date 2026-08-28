// SPDX-License-Identifier: Apache-2.0

// T-3908: the data fetch behind the recency ("what changed") overlay.
//
// NO NEW BACKEND ROUTE. This reuses exactly the two routes History and
// diffOverlay.ts already call: `GET /snapshots` (web/src/api/snapshots.ts,
// T-206) to find the oldest snapshot in one page, and
// `GET /topology/diff?from=&to=now` (web/src/api/topologyDiff.ts, T-2704) —
// the same route topologyDiffQuery.ts's `useTopologyDiffQuery` wraps for a
// user-selected range — to diff that snapshot against the live cluster.
//
// WHY THE OLDEST SNAPSHOT IN ONE PAGE, NOT A FIXED LOOKBACK WINDOW. A fixed
// wall-clock window (e.g. "30 days ago") would `ErrNoSnapshotForPoint` on
// any cluster younger than the window — the exact failure mode this
// codebase's diff error path treats as a real, actionable refusal rather
// than something to silently paper over (topodiff.go's
// ErrNoSnapshotForPoint doc comment). Passing a real snapshot id as `from`
// instead always resolves: `TopologyDiff` looks it up directly
// (`s.snapshots.Get`), no "is there history that far back" gate at all. The
// tradeoff, stated rather than hidden: the lookback is bounded to whatever
// history fits in one `GET /snapshots` page (RECENCY_SNAPSHOT_PAGE_LIMIT),
// the same "bounded ring, not a warehouse" scoping T-3910's card applies to
// the flow/metric rings — an entity last touched before that page's oldest
// snapshot reads as "no change in the lookback window" (no mark at all, see
// recencyOverlay.ts), never as a false "definitely never changed".
import { useQuery } from "@tanstack/react-query";
import { fetchSnapshots } from "../api/snapshots";
import { fetchTopologyDiff, TOPOLOGY_DIFF_NOW, type TopologyDiffResponse } from "../api/topologyDiff";

/** One page's worth of snapshots (`GET /snapshots`' own default limit) —
 * the recency overlay's lookback bound. See this file's doc comment. */
export const RECENCY_SNAPSHOT_PAGE_LIMIT = 50;

export interface RecencyDiffResult {
  diff: TopologyDiffResponse;
  /** The oldest snapshot's own capture time — surfaced so the UI can say
   * "since <date>" rather than leaving the lookback bound unstated. */
  oldestSnapshotAt: number;
}

/** `undefined` means "no snapshots exist yet" — a real, honestly-reported
 * state (mirrors ErrNoSnapshotForPoint's own "there are no snapshots at
 * all" message), not an error. */
async function fetchRecencyDiff(): Promise<RecencyDiffResult | undefined> {
  const page = await fetchSnapshots(undefined, RECENCY_SNAPSHOT_PAGE_LIMIT);
  // fetchSnapshots is newest-first (docs/api.md), so the last item in one
  // page is the oldest one this call can see.
  const oldest = page.items[page.items.length - 1];
  if (!oldest) return undefined;
  const diff = await fetchTopologyDiff(oldest.id, TOPOLOGY_DIFF_NOW);
  return { diff, oldestSnapshotAt: oldest.takenAt };
}

/** Fetches the recency overlay's data while the layer is toggled on. Not
 * polled at a fast interval — like useTopologyDiffQuery, a silently
 * re-fetching diff would shift an operator's read mid-triage; a 60s
 * staleTime just keeps repeated toggles from re-fetching needlessly. */
export function useRecencyOverlayQuery(enabled: boolean) {
  return useQuery({
    queryKey: ["topology-recency", RECENCY_SNAPSHOT_PAGE_LIMIT],
    queryFn: fetchRecencyDiff,
    enabled,
    staleTime: 60_000,
  });
}
