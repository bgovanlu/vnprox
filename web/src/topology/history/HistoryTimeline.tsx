// T-1007 "History playback": a scrubber bound to the map's EXISTING
// traffic-paint (trafficMode.ts) and flows (flowEdges.ts) layers. Dragging
// the scrubber re-queries GET /metrics/history and GET /flows for the
// selected instant/window and hands back data shaped exactly like the live
// path's own inputs (ref -> utilizationPct, raw FlowRecord[]) — the caller
// (TopologyPage.tsx) feeds those straight into the SAME toFlowElements/
// computeFlowEdges calls it already makes for "now", with only a `now`
// param threaded through computeFlowEdges. This component adds no new
// rendering path of its own.
//
// Strictly read-only: no apply/confirm/rollback affordance anywhere in this
// UI. A changeset marker's click handler only opens the existing changeset
// drawer (useChangesetDrawerStore.setActiveId — the same "detail/diff view"
// FindingsStreamPanel.tsx's "Fix" flow already deep-links into) at whatever
// state that changeset is actually in; it never calls apply/confirm/
// rollback itself.
import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { fetchMetricsHistory, fetchMetricsLive } from "../../api/metrics";
import { fetchFlows } from "../../api/flows";
import { fetchHistoryEvents } from "../../api/history";
import type { FlowRecord } from "../../api/types";
import { useChangesetDrawerStore } from "../../changesets/store";
import { DEFAULT_WINDOW_SECONDS } from "../flowEdges";
import {
  DEFAULT_FLOW_RETENTION_MINUTES,
  HISTORY_SNAPSHOT_LOOKBACK_SECONDS,
  computePlaybackRange,
  flowsAvailableAt,
  historicalUtilizationByRef,
  nearestHistoryPoint,
} from "./historyPlayback";

/** The scrub result TopologyPage folds into its existing render inputs.
 * `scrubbing: false` is the passthrough case — `utilizationByRef`/
 * `flowRecords` are simply the live values the caller supplied, unchanged,
 * so wiring this component in changes nothing about "now" rendering. */
export interface HistoryPlaybackState {
  scrubbing: boolean;
  /** The scrubbed instant (unix seconds), or undefined when live. Callers
   * pass this as computeFlowEdges' own `now` param when scrubbing. */
  at: number | undefined;
  utilizationByRef: ReadonlyMap<string, number>;
  flowRecords: readonly FlowRecord[];
  /** False once `at` is older than the flows retention window allows —
   * callers should treat the Flows layer as unavailable (not just empty)
   * at that point, matching this component's own disclosure banner. */
  flowsAvailable: boolean;
}

export interface HistoryTimelineProps {
  /** Candidate metric refs — the same set TopologyPage's own
   * metricsCandidateRefs already computes for useLiveMetrics. */
  metricsRefs: readonly string[];
  /** T-1002's `[flows] retention_minutes` for the CURRENT node. Defaults to
   * T-1002's own documented default (60) — vnprox has no route today that
   * surfaces the live configured value back to the frontend (flagged in
   * this task's report); passing a real value here (once such a route
   * exists) needs no change to this component. */
  flowRetentionMinutes?: number;
  /** The live (WS-fed) utilization map / flow-record buffer TopologyPage
   * already computes for "now" — passed straight through in the
   * `scrubbing: false` case so this component never has to re-derive live
   * state, only override it while scrubbing. */
  liveUtilizationByRef: ReadonlyMap<string, number>;
  liveFlowRecords: readonly FlowRecord[];
  onPlaybackChange: (state: HistoryPlaybackState) => void;
  /** Injectable "now" (unix seconds) for deterministic tests; defaults to
   * the real wall clock, refreshed every 30s so the slider's own "now" end
   * keeps advancing during a long-open session. */
  now?: () => number;
}

const NOW_REFRESH_MS = 30_000;
const HISTORY_EVENTS_REFETCH_MS = 30_000;

function defaultNow(): number {
  return Math.floor(Date.now() / 1000);
}

function formatAt(at: number): string {
  return new Date(at * 1000).toLocaleString();
}

export function HistoryTimeline({
  metricsRefs,
  flowRetentionMinutes = DEFAULT_FLOW_RETENTION_MINUTES,
  liveUtilizationByRef,
  liveFlowRecords,
  onPlaybackChange,
  now,
}: HistoryTimelineProps) {
  const nowFn = now ?? defaultNow;
  const [nowAt, setNowAt] = useState(nowFn);
  useEffect(() => {
    const id = setInterval(() => {
      setNowAt(nowFn());
    }, NOW_REFRESH_MS);
    return () => {
      clearInterval(id);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- nowFn is stable per mount (now prop, if passed, is expected to be a stable test double)
  }, []);

  const range = useMemo(() => computePlaybackRange(nowAt, flowRetentionMinutes), [nowAt, flowRetentionMinutes]);

  const [scrubAt, setScrubAt] = useState<number | undefined>(undefined);
  const scrubbing = scrubAt !== undefined;
  const at = scrubAt ?? range.nowAt;
  const flowsAvailable = !scrubbing || flowsAvailableAt(at, range);

  const sortedRefs = useMemo(() => Array.from(new Set(metricsRefs)).sort(), [metricsRefs]);

  // Historical per-ref rate snapshot (nearest sample to `at`) — only
  // fetched while genuinely scrubbing (AC1: the live WS-fed cache is never
  // read for this; this is a fresh GET /metrics/history call every time
  // `at` changes).
  const { data: historyPoints } = useQuery({
    queryKey: ["history-playback-metrics", sortedRefs, at],
    queryFn: async () => {
      const entries = await Promise.all(
        sortedRefs.map(async (ref) => {
          const points = await fetchMetricsHistory(ref, at - HISTORY_SNAPSHOT_LOOKBACK_SECONDS, at);
          return [ref, nearestHistoryPoint(points, at)] as const;
        }),
      );
      return new Map(entries);
    },
    enabled: scrubbing && sortedRefs.length > 0,
  });

  // Link-speed snapshot: a one-shot fetch per ref set for the scrub
  // session, purely to convert historical rates into a utilization
  // percentage the same way trafficMode's live path does (see
  // historicalUtilizationByRef's doc comment) — NOT subscribed/live, and
  // NOT re-fetched on every drag tick (staleTime: Infinity), since a link's
  // own speed does not change mid-scrub and re-reading it live would be
  // exactly the "WS-fed cache read while scrubbed" this task's card rules
  // out.
  const { data: liveSpeeds } = useQuery({
    queryKey: ["history-playback-link-speeds", sortedRefs],
    queryFn: () => fetchMetricsLive(sortedRefs),
    enabled: scrubbing && sortedRefs.length > 0,
    staleTime: Infinity,
  });
  const speedsByRef = useMemo(() => {
    const m = new Map<string, number | undefined>();
    for (const item of liveSpeeds ?? []) m.set(item.ref, item.speedMbps);
    return m;
  }, [liveSpeeds]);

  const historicalUtilization = useMemo(
    () => (historyPoints ? historicalUtilizationByRef(historyPoints, speedsByRef) : new Map<string, number>()),
    [historyPoints, speedsByRef],
  );

  const { data: historicalFlows } = useQuery({
    queryKey: ["history-playback-flows", at],
    queryFn: () => fetchFlows({ fromTs: at - DEFAULT_WINDOW_SECONDS, toTs: at, limit: 500 }),
    enabled: scrubbing && flowsAvailable,
  });

  const { data: events } = useQuery({
    queryKey: ["history-events", range.earliestAt, range.nowAt],
    queryFn: () => fetchHistoryEvents(range.earliestAt, range.nowAt),
    refetchInterval: HISTORY_EVENTS_REFETCH_MS,
  });

  useEffect(() => {
    if (scrubbing) {
      onPlaybackChange({
        scrubbing: true,
        at,
        utilizationByRef: historicalUtilization,
        flowRecords: flowsAvailable ? (historicalFlows?.items ?? []) : [],
        flowsAvailable,
      });
    } else {
      onPlaybackChange({
        scrubbing: false,
        at: undefined,
        utilizationByRef: liveUtilizationByRef,
        flowRecords: liveFlowRecords,
        flowsAvailable: true,
      });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scrubbing, at, historicalUtilization, historicalFlows, flowsAvailable, liveUtilizationByRef, liveFlowRecords]);

  const setActiveChangeset = useChangesetDrawerStore((s) => s.setActiveId);

  const span = range.nowAt - range.earliestAt || 1;
  function positionPct(eventAt: number): number {
    return Math.min(100, Math.max(0, ((eventAt - range.earliestAt) / span) * 100));
  }

  return (
    <div
      className="flex flex-col gap-1 rounded-md border border-slate-200 bg-white/90 p-2 shadow-sm dark:border-slate-700 dark:bg-slate-900/90"
      data-testid="history-timeline"
    >
      <div className="flex items-center gap-2">
        <span className="text-xs font-medium text-slate-600 dark:text-slate-300">History</span>
        <input
          type="range"
          aria-label="Scrub map history"
          min={range.earliestAt}
          max={range.nowAt}
          step={30}
          value={at}
          onChange={(e) => {
            const v = Number(e.target.value);
            setScrubAt(v >= range.nowAt ? undefined : v);
          }}
          className="h-1 flex-1 accent-accent-600"
        />
        <span className="whitespace-nowrap text-xs text-slate-500 dark:text-slate-400">
          {scrubbing ? formatAt(at) : "Live"}
        </span>
        {scrubbing && (
          <button
            type="button"
            onClick={() => {
              setScrubAt(undefined);
            }}
            className="rounded bg-accent-600 px-2 py-0.5 text-xs font-medium text-white"
          >
            Back to live
          </button>
        )}
      </div>

      <div className="relative h-4">
        {(events ?? []).map((evt, i) => (
          <button
            key={`${evt.kind}-${String(evt.at)}-${String(i)}`}
            type="button"
            title={evt.kind === "changeset" ? `${evt.action ?? "changeset"} (${evt.result ?? ""})` : `Finding ${evt.transition ?? ""}`}
            aria-label={
              evt.kind === "changeset"
                ? `Changeset event: ${evt.action ?? "unknown"} at ${formatAt(evt.at)}`
                : `Finding ${evt.transition ?? "event"} at ${formatAt(evt.at)}`
            }
            onClick={() => {
              if (evt.kind === "changeset" && evt.changesetId) {
                setActiveChangeset(evt.changesetId);
              }
            }}
            disabled={evt.kind === "finding"}
            className={
              evt.kind === "changeset"
                ? "absolute top-0 h-3 w-1.5 -translate-x-1/2 rounded-sm bg-accent-500 hover:bg-accent-700"
                : "absolute top-0 h-3 w-1.5 -translate-x-1/2 cursor-default rounded-sm bg-amber-400"
            }
            style={{ left: `${String(positionPct(evt.at))}%` }}
          />
        ))}
      </div>

      {scrubbing && !flowsAvailable && (
        <p className="text-xs text-amber-700 dark:text-amber-300">
          Flow history available for the last {flowRetentionMinutes} minutes only — the Flows layer is disabled at this
          point in time.
        </p>
      )}
    </div>
  );
}
