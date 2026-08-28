// SPDX-License-Identifier: Apache-2.0

// T-3910 "Flow replay": an animate/scrub control over the map's EXISTING
// traffic-heat (trafficMode.ts) and flow-paths (flowEdges.ts) paint — the
// same visual vocabulary the live Flows/Traffic layers already use,
// repainted at a chosen past instant instead of "now".
//
// DISTINCT FROM HISTORY PLAYBACK, ON PURPOSE. history/HistoryTimeline.tsx
// (T-1007) answers "what CONFIGURATION changed and when" — it drags a
// scrubber past changeset/finding markers and opens the changeset drawer on
// click. This panel answers a different question, "what did TRAFFIC look
// like at this instant" — it has no event markers and no changeset
// deep-link at all. So this is its own toggle (topology/store.ts's
// `replayLayerActive`, off by default like every other v2-canvas overlay)
// and its own panel with its own label, Play/Pause, and Step controls
// HistoryTimeline has none of — an operator reads "Traffic replay" and
// "History" as two different tools, never two skins on one control (this
// task's card AC2).
//
// NO NEW RENDERING PATH. This component only produces a frame
// (utilizationByRef + flowRecords, HistoryTimeline's own
// HistoryPlaybackState shape) that TopologyPage.tsx feeds into the exact
// same toFlowElements/computeFlowEdges calls it already makes for "now"
// and for History's own scrub.
//
// TWO RINGS, TWO BOUNDS — SHOWN, NOT DISCOVERED BY DRAGGING. The roadmap's
// "the bounded 24h flow/metric rings" shorthand is wrong for flows:
// metric_samples is a 24h ring (internal/store/metrics.go's
// MetricRetention) but flow_samples defaults to 60 MINUTES
// (internal/flow/service.go's DefaultRetentionMinutes, docs/data-model.md
// §2). This panel's header states both bounds up front, before the
// scrubber is ever touched — see flowReplay.ts's module doc comment for
// why the split is never collapsed to one figure.
//
// TWO DIFFERENT EMPTY MESSAGES, ON PURPOSE (this task's card's single most
// important correctness bar). Flow ingestion is opt-in and off by default
// (docs/data-model.md §2's flow_samples entry) — a cluster with it off
// looks, at the API layer, identical to a cluster with it on but genuinely
// quiet at this instant: GET /flows returns zero rows either way.
// Conflating the two is exactly the "empty panel that looks like 'no
// traffic'" failure guest/guestEgo.ts's deriveFlowsPanelState already
// solved for the Guest Ego view (T-3906) — flowReplay.ts's
// deriveFlowPathsFrameState reuses that same probe/shape here rather than
// inventing a second vocabulary for the same fact. Traffic-HEAT
// (utilization, from interface counters) is a different, always-on ring
// and keeps replaying regardless, so the map is never fully blank just
// because flow ingestion happens to be off.
//
// ACCESSIBILITY. Play is disabled outright under `prefers-reduced-motion:
// reduce` (lib/useReducedMotion.ts, T-905's app-wide seam) — an
// auto-advancing loop is exactly the unprompted motion that preference
// asks vnprox not to run. Step (Prev/Next) and the scrubber itself remain
// fully available either way: a non-animated way to reach every frame,
// independent of Play, satisfying this task's card without a second
// "reduced" rendering path. The current instant and the flow-paths status
// line are both plain text (`aria-live="polite"`), never conveyed by
// animation or color alone.
import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { fetchMetricsHistory, fetchMetricsLive } from "../../api/metrics";
import { fetchFlows } from "../../api/flows";
import type { FlowRecord } from "../../api/types";
import { HelpAnchor } from "../../help/HelpAnchor";
import { useReducedMotion } from "../../lib/useReducedMotion";
import { useClusterHasAnyFlowsProbe } from "../../guest/guestEgoQueries";
import { DEFAULT_WINDOW_SECONDS } from "../flowEdges";
import type { HistoryPlaybackState } from "../history/HistoryTimeline";
import {
  DEFAULT_FLOW_RETENTION_MINUTES,
  HISTORY_SNAPSHOT_LOOKBACK_SECONDS,
  METRICS_RETENTION_SECONDS,
  computePlaybackRange,
  flowsAvailableAt,
  historicalUtilizationByRef,
  nearestHistoryPoint,
} from "../history/historyPlayback";
import {
  REPLAY_STEP_SECONDS,
  REPLAY_TICK_MS,
  clampReplayInstant,
  deriveFlowPathsFrameState,
  flowPathsFrameMessage,
  formatReplayDuration,
  stepReplayInstant,
  tickReplay,
} from "./flowReplay";

/** Same frame shape HistoryTimeline emits — TopologyPage folds either
 * source into the identical render inputs, so this component reuses the
 * type rather than defining a field-for-field duplicate. */
export type ReplayFrameState = HistoryPlaybackState;

export interface FlowReplayPanelProps {
  /** Candidate metric refs — the same set TopologyPage's own
   * metricsCandidateRefs already computes for useLiveMetrics. */
  metricsRefs: readonly string[];
  /** `[flows] retention_minutes` for the CURRENT node. Defaults to
   * internal/flow's own documented default (60) — vnprox has no route
   * today that surfaces the live configured value back to the frontend
   * (the same gap HistoryTimeline's own doc comment flags); passing a
   * real value here needs no change to this component once such a route
   * exists. */
  flowRetentionMinutes?: number;
  /** The live (WS-fed) utilization map / flow-record buffer TopologyPage
   * already computes for "now" — passed straight through while paused at
   * live, mirroring HistoryTimeline's identical contract. */
  liveUtilizationByRef: ReadonlyMap<string, number>;
  liveFlowRecords: readonly FlowRecord[];
  onReplayChange: (state: ReplayFrameState) => void;
  /** Injectable "now" (unix seconds) for deterministic tests. */
  now?: () => number;
}

const NO_FLOW_RECORDS: readonly FlowRecord[] = [];
const NOW_REFRESH_MS = 30_000;

function defaultNow(): number {
  return Math.floor(Date.now() / 1000);
}

function formatAt(at: number): string {
  return new Date(at * 1000).toLocaleString();
}

function sameEntries<K, V>(a: ReadonlyMap<K, V>, b: ReadonlyMap<K, V>): boolean {
  if (a === b) return true;
  if (a.size !== b.size) return false;
  for (const [k, v] of a) {
    if (!b.has(k) || !Object.is(b.get(k), v)) return false;
  }
  return true;
}

function sameItems<T>(a: readonly T[], b: readonly T[]): boolean {
  if (a === b) return true;
  if (a.length !== b.length) return false;
  return a.every((item, i) => Object.is(item, b[i]));
}

/** Same defence-in-depth as HistoryTimeline's samePlayback (T-2003-bug-01):
 * never re-emit a frame that is field-for-field the one already reported,
 * so an unstable identity on the caller's live-value props can't turn
 * "notify the parent" into a render loop. */
function sameFrame(prev: ReplayFrameState | undefined, next: ReplayFrameState): boolean {
  return (
    prev?.scrubbing === next.scrubbing &&
    prev.at === next.at &&
    prev.flowsAvailable === next.flowsAvailable &&
    sameEntries(prev.utilizationByRef, next.utilizationByRef) &&
    sameItems(prev.flowRecords, next.flowRecords)
  );
}

export function FlowReplayPanel({
  metricsRefs,
  flowRetentionMinutes = DEFAULT_FLOW_RETENTION_MINUTES,
  liveUtilizationByRef,
  liveFlowRecords,
  onReplayChange,
  now,
}: FlowReplayPanelProps) {
  const nowFn = now ?? defaultNow;
  const [nowAt, setNowAt] = useState(nowFn);
  useEffect(() => {
    const id = setInterval(() => {
      setNowAt(nowFn());
    }, NOW_REFRESH_MS);
    return () => {
      clearInterval(id);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- nowFn is stable per mount
  }, []);

  const range = useMemo(() => computePlaybackRange(nowAt, flowRetentionMinutes), [nowAt, flowRetentionMinutes]);
  const reducedMotion = useReducedMotion();

  const [scrubAt, setScrubAt] = useState<number | undefined>(undefined);
  const [playing, setPlaying] = useState(false);
  const scrubbing = scrubAt !== undefined;
  const at = scrubAt ?? range.nowAt;
  const flowsInWindow = !scrubbing || flowsAvailableAt(at, range);

  // Play: advances one REPLAY_STEP_SECONDS frame every REPLAY_TICK_MS while
  // `playing`. Never runs at all under prefers-reduced-motion (see this
  // file's doc comment) — the effect below simply no-ops in that case, so
  // `playing` can only be set true via togglePlay, which itself refuses to
  // start while reducedMotion (the button is `disabled`).
  useEffect(() => {
    if (!playing || reducedMotion) return;
    const id = setInterval(() => {
      setScrubAt((prev) => {
        const current = prev ?? range.nowAt;
        const result = tickReplay(current, range);
        if (!result.playing) setPlaying(false);
        return result.at >= range.nowAt ? undefined : result.at;
      });
    }, REPLAY_TICK_MS);
    return () => {
      clearInterval(id);
    };
  }, [playing, reducedMotion, range]);

  const sortedRefs = useMemo(() => Array.from(new Set(metricsRefs)).sort(), [metricsRefs]);

  // Historical per-ref rate snapshot for the traffic-HEAT half — same
  // fetch/nearest-sample shape HistoryTimeline uses, on this panel's own
  // instant. Never gated on flow ingestion: heat is a different ring.
  const { data: historyPoints } = useQuery({
    queryKey: ["flow-replay-metrics", sortedRefs, at],
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

  // Link-speed snapshot: one-shot per ref set, not re-fetched on every
  // tick/drag (staleTime: Infinity) — a link's own speed doesn't change
  // mid-replay, mirroring HistoryTimeline's identical reasoning.
  const { data: liveSpeeds } = useQuery({
    queryKey: ["flow-replay-link-speeds", sortedRefs],
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

  // Flow-paths half: the cluster-wide "does ingestion even exist" probe
  // (same one guest/guestEgoQueries.ts's Guest Ego view uses) plus this
  // instant's own GET /flows window.
  const clusterProbe = useClusterHasAnyFlowsProbe();
  const {
    data: historicalFlows,
    isLoading: frameLoading,
    isError: frameError,
  } = useQuery({
    queryKey: ["flow-replay-flows", at],
    queryFn: () => fetchFlows({ fromTs: at - DEFAULT_WINDOW_SECONDS, toTs: at, limit: 500 }),
    enabled: scrubbing && flowsInWindow,
  });

  const flowPathsState = useMemo(
    () =>
      deriveFlowPathsFrameState({
        clusterHasAnyFlows: clusterProbe.hasAny,
        clusterProbeLoading: clusterProbe.isLoading,
        clusterProbeError: clusterProbe.isError,
        inWindow: flowsInWindow,
        retentionMinutes: flowRetentionMinutes,
        frameLoading,
        frameError,
        frameRecords: historicalFlows?.items,
      }),
    [clusterProbe.hasAny, clusterProbe.isLoading, clusterProbe.isError, flowsInWindow, flowRetentionMinutes, frameLoading, frameError, historicalFlows],
  );

  const lastEmitted = useRef<ReplayFrameState | undefined>(undefined);
  useEffect(() => {
    const next: ReplayFrameState = scrubbing
      ? {
          scrubbing: true,
          at,
          utilizationByRef: historicalUtilization,
          flowRecords: flowPathsState.kind === "data" ? flowPathsState.records : NO_FLOW_RECORDS,
          flowsAvailable: flowsInWindow,
        }
      : {
          scrubbing: false,
          at: undefined,
          utilizationByRef: liveUtilizationByRef,
          flowRecords: liveFlowRecords,
          flowsAvailable: true,
        };
    if (sameFrame(lastEmitted.current, next)) return;
    lastEmitted.current = next;
    onReplayChange(next);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scrubbing, at, historicalUtilization, flowPathsState, flowsInWindow, liveUtilizationByRef, liveFlowRecords]);

  function stepBack() {
    setPlaying(false);
    setScrubAt(stepReplayInstant(at, range, -1));
  }

  function stepForward() {
    setPlaying(false);
    const next = stepReplayInstant(at, range, 1);
    setScrubAt(next >= range.nowAt ? undefined : next);
  }

  function togglePlay() {
    if (reducedMotion) return;
    if (!scrubbing) setScrubAt(range.earliestAt);
    setPlaying((p) => !p);
  }

  function backToLive() {
    setPlaying(false);
    setScrubAt(undefined);
  }

  return (
    <div
      className="flex flex-col gap-1.5 rounded-md border border-lime-300 bg-lime-50/70 p-2 shadow-sm dark:border-lime-800 dark:bg-lime-950/30"
      data-testid="flow-replay-panel"
    >
      <div className="flex flex-wrap items-center gap-2">
        <span className="flex items-center gap-1.5 text-xs font-medium text-lime-800 dark:text-lime-300">
          Traffic replay
          <HelpAnchor topic="flow-replay" />
        </span>
        <span className="text-xs text-slate-600 dark:text-slate-400">
          Heat: last {formatReplayDuration(METRICS_RETENTION_SECONDS)} · Flow paths: last {formatReplayDuration(flowRetentionMinutes * 60)}
        </span>
      </div>

      <div className="flex items-center gap-2">
        <button
          type="button"
          onClick={togglePlay}
          disabled={reducedMotion}
          aria-pressed={playing}
          title={reducedMotion ? "Autoplay is off (prefers-reduced-motion) — use Step or drag the scrubber instead" : undefined}
          className="rounded bg-lime-600 px-2 py-0.5 text-xs font-medium text-white disabled:cursor-not-allowed disabled:bg-slate-400 dark:disabled:bg-slate-600"
        >
          {playing ? "Pause" : "Play"}
        </button>
        <button
          type="button"
          onClick={stepBack}
          aria-label="Step back one frame"
          className="rounded border border-lime-600 px-2 py-0.5 text-xs font-medium text-lime-800 dark:text-lime-300"
        >
          ◀ Step
        </button>
        <button
          type="button"
          onClick={stepForward}
          aria-label="Step forward one frame"
          className="rounded border border-lime-600 px-2 py-0.5 text-xs font-medium text-lime-800 dark:text-lime-300"
        >
          Step ▶
        </button>
        <input
          type="range"
          aria-label="Scrub traffic replay"
          min={range.earliestAt}
          max={range.nowAt}
          step={REPLAY_STEP_SECONDS}
          value={at}
          onChange={(e) => {
            setPlaying(false);
            const v = Number(e.target.value);
            setScrubAt(v >= range.nowAt ? undefined : clampReplayInstant(v, range));
          }}
          className="h-1 flex-1 accent-lime-600"
        />
        <span className="whitespace-nowrap text-xs text-slate-600 dark:text-slate-400" aria-live="polite">
          {scrubbing ? formatAt(at) : "Live"}
        </span>
        {scrubbing && (
          <button type="button" onClick={backToLive} className="rounded bg-lime-600 px-2 py-0.5 text-xs font-medium text-white">
            Exit replay
          </button>
        )}
      </div>

      {reducedMotion && (
        <p className="text-xs text-slate-600 dark:text-slate-400">
          Autoplay is off because this browser prefers reduced motion — Step and the scrubber above still reach every frame.
        </p>
      )}

      {scrubbing && (
        <p className="text-xs text-slate-600 dark:text-slate-400" aria-live="polite">
          {flowPathsFrameMessage(flowPathsState)}
        </p>
      )}
    </div>
  );
}
