// SPDX-License-Identifier: Apache-2.0

// T-1007 "History playback": pure, framework-free helpers for
// HistoryTimeline.tsx's scrubber — playback-range clamping, nearest-sample
// lookup, and the historical-rates-to-utilization mapping. Kept separate
// from the component (mirrors flowEdges.ts's/trafficMode.ts's own
// "pure logic module, directly Vitest-table-testable with no React/canvas
// involved" split) so the scrub math is unit-tested without mounting
// anything.
import type { HistoryPoint } from "../../api/types";
import { computeUtilizationPct } from "../trafficMode";

/** docs/features/monitoring.md §2: "24h ring in SQLite ... nothing longer" —
 * the metrics-history half of the playback range's outer bound. Mirrors
 * internal/store.MetricRetention exactly (kept as a client-side constant,
 * not read from the server, since this task's card fixes it at 24h and
 * there is no live "what's my retention window" config route to read
 * instead — see this task's completion report). */
export const METRICS_RETENTION_SECONDS = 24 * 3600;

/** T-1002's own documented default ([flows] retention_minutes) — used as
 * this component's default when the caller doesn't have a live per-node
 * config value to pass instead (see report: vnprox has no route today that
 * surfaces the *configured* value back to the frontend). */
export const DEFAULT_FLOW_RETENTION_MINUTES = 60;

/** How far before a scrubbed instant to search for the nearest stored
 * 30s-downsampled metrics-history sample. Wide enough to always straddle
 * at least one real sample even when the scrub lands exactly on a bucket
 * boundary; narrow enough that a scrub near the very edge of retained
 * history doesn't silently pick up a wildly stale point instead of "no
 * data yet here". */
export const HISTORY_SNAPSHOT_LOOKBACK_SECONDS = 90;

/** The scrubber's own playback bounds at a given instant (docs/api.md's
 * History section: "clamped to the shorter of the metrics window (24h)
 * and the flows window"). `earliestAt`/`nowAt` bound the slider itself;
 * `flowsEarliestAt` (always >= earliestAt) is where the Flows layer stops
 * being available going further back — the UI discloses that boundary
 * explicitly rather than silently showing an empty overlay past it. */
export interface PlaybackRange {
  earliestAt: number;
  flowsEarliestAt: number;
  nowAt: number;
}

/** Computes the playback range for "right now" (nowAt, unix seconds).
 * flowRetentionMinutes defaults to T-1002's own default (60); a
 * misconfigured non-positive value degrades to "no flow history at all"
 * (flowsEarliestAt === nowAt) rather than a negative/nonsensical window. */
export function computePlaybackRange(nowAt: number, flowRetentionMinutes: number = DEFAULT_FLOW_RETENTION_MINUTES): PlaybackRange {
  const flowRetentionSeconds = Math.max(0, flowRetentionMinutes) * 60;
  const boundedFlowRetention = Math.min(METRICS_RETENTION_SECONDS, flowRetentionSeconds);
  return {
    earliestAt: nowAt - METRICS_RETENTION_SECONDS,
    flowsEarliestAt: nowAt - boundedFlowRetention,
    nowAt,
  };
}

/** True iff `at` (a candidate scrub position) still has flow history
 * available under `range` — false past `range.flowsEarliestAt`, per this
 * task's card ("the UI discloses the flow-history limit explicitly rather
 * than showing a silent gap"). */
export function flowsAvailableAt(at: number, range: PlaybackRange): boolean {
  return at >= range.flowsEarliestAt;
}

/** Picks the point in `points` closest (by absolute difference) to `at`;
 * ties keep the earlier candidate encountered (stable for an
 * already-ascending input, which is what GET /metrics/history returns).
 * `undefined` for an empty input — "no sample known near this instant" is
 * a real, honestly-reported case (docs/features/monitoring.md §2), never
 * fabricated as a zeroed point. */
export function nearestHistoryPoint<T extends { at: number }>(points: readonly T[], at: number): T | undefined {
  let best: T | undefined;
  let bestDelta = Infinity;
  for (const p of points) {
    const delta = Math.abs(p.at - at);
    if (delta < bestDelta) {
      best = p;
      bestDelta = delta;
    }
  }
  return best;
}

/** Maps a per-ref snapshot of "the nearest historical HistoryPoint" plus a
 * per-ref link-speed snapshot into the same ref -> utilizationPct shape
 * metricsQueries.ts's utilizationMap produces for live data — the exact
 * computeUtilizationPct math trafficMode's live path already uses
 * (docs/features/monitoring.md §1), just fed historical rates instead of
 * a live WS tick. A ref with no nearby sample, or no known link speed, is
 * simply absent from the result (matches utilizationMap's own "absent,
 * not zero/error" convention for a ref with nothing to report). */
export function historicalUtilizationByRef(
  points: ReadonlyMap<string, HistoryPoint | undefined>,
  speedMbpsByRef: ReadonlyMap<string, number | undefined>,
): ReadonlyMap<string, number> {
  const out = new Map<string, number>();
  for (const [ref, point] of points) {
    if (!point) continue;
    const speed = speedMbpsByRef.get(ref);
    const rxUtilPct = computeUtilizationPct(point.rates.rxBps, speed);
    const txUtilPct = computeUtilizationPct(point.rates.txBps, speed);
    const pct = Math.max(rxUtilPct, txUtilPct);
    if (pct > 0) out.set(ref, pct);
  }
  return out;
}
