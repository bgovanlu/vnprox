// SPDX-License-Identifier: Apache-2.0

// T-3910 "Flow replay": pure, framework-free helpers for FlowReplayPanel's
// animate/scrub controls — frame stepping, play-tick advancement, and the
// ingestion-disabled/empty split for the flow-paths half of a replayed
// frame. Kept separate from the component (mirrors history/
// historyPlayback.ts's own "pure logic module, directly Vitest-table-
// testable with no React/canvas involved" split) so this task's card's
// central quality bar — telling "flow ingestion is off" apart from "no
// traffic in this window" — is covered by table-driven tests with no
// component mounted.
//
// TWO RINGS, TWO BOUNDS. The roadmap's shorthand ("the bounded 24h
// flow/metric rings") is wrong for flows: metric_samples is pruned to a
// 24h rolling window (internal/store/metrics.go's MetricRetention), but
// flow_samples defaults to a 60-MINUTE retention window
// (internal/flow/service.go's DefaultRetentionMinutes, docs/data-model.md
// §2's flow_samples entry) — shorter, and configurable via
// `[flows] retention_minutes`. This module reuses history/
// historyPlayback.ts's own computePlaybackRange (already correctly split
// into `earliestAt`, the 24h metrics bound, and `flowsEarliestAt`, the
// tighter of the two) rather than re-deriving the split — the exact
// "assume both rings are the same window" mistake this task's card warns
// the expanding agent not to make.
import type { FlowRecord } from "../../api/types";
import type { PlaybackRange } from "../history/historyPlayback";

/** Scrub/step granularity, seconds — matches history/HistoryTimeline.tsx's
 * own slider step so a frame landed on by either scrubber lines up with
 * the same underlying historical sample. */
export const REPLAY_STEP_SECONDS = 30;

/** How often, in ms, an active "Play" advances one frame. Chosen so even
 * the full 24h metrics window (2,880 30s frames) finishes in a few minutes
 * of real time — this is a review tool over already-recorded rings, not a
 * real-time monitor (trafficMode/the live Flows layer already own "watch
 * it happen now"). */
export const REPLAY_TICK_MS = 250;

export type ReplayDirection = 1 | -1;

/** Clamps a candidate instant to the playback range's [earliestAt, nowAt]
 * bounds. The map's traffic-HEAT half of a replay can reach back the full
 * metrics window even when flow PATHS cannot — flowsAvailableAt
 * (historyPlayback.ts) narrows further, independently, for the flow-paths
 * half; this function only enforces the outer, heat-side bound every
 * instant (scrubbed or stepped) must stay within. */
export function clampReplayInstant(at: number, range: PlaybackRange): number {
  return Math.min(range.nowAt, Math.max(range.earliestAt, at));
}

/** One manual step (Prev/Next buttons, or a keyboard press on the slider)
 * — the non-animated way to move through instants this task's
 * accessibility bar requires, independent of Play. */
export function stepReplayInstant(
  at: number,
  range: PlaybackRange,
  direction: ReplayDirection,
  stepSeconds: number = REPLAY_STEP_SECONDS,
): number {
  return clampReplayInstant(at + direction * stepSeconds, range);
}

/** One Play tick: advances forward and reports whether playback should
 * keep running. Reaching `range.nowAt` stops playback (`playing: false`)
 * rather than looping silently back to the start or running past "now"
 * into a nonsensical future — arriving at "now" is exactly "back to live",
 * the same destination the manual "Exit replay" control reaches. */
export interface ReplayTickResult {
  at: number;
  playing: boolean;
}

export function tickReplay(at: number, range: PlaybackRange, stepSeconds: number = REPLAY_STEP_SECONDS): ReplayTickResult {
  const next = at + stepSeconds;
  if (next >= range.nowAt) return { at: range.nowAt, playing: false };
  return { at: next, playing: true };
}

// ---------------------------------------------------------------------------
// Flow-paths honesty split: "ingestion is off" vs "on, but empty here" —
// this task's card's single most important correctness bar. Mirrors
// guest/guestEgo.ts's deriveFlowsPanelState exactly (same probe shape,
// same distinction, T-3906), scoped to one replayed frame across the whole
// map instead of one guest's bridge/VNet targets. Traffic-HEAT
// (utilization, from interface counters, internal/metrics) is a different,
// always-on ring and is never gated by this — it keeps replaying even
// while flow paths report "ingestion disabled", so the map is never fully
// blank just because flow ingestion happens to be off (opt-in, off by
// default per docs/data-model.md §2's flow_samples entry).
// ---------------------------------------------------------------------------

export type FlowPathsFrameState =
  | { kind: "loading" }
  | { kind: "error" }
  | { kind: "out-of-window"; retentionMinutes: number }
  | { kind: "ingestion-disabled" }
  | { kind: "empty" }
  | { kind: "data"; records: readonly FlowRecord[] };

export interface FlowPathsFrameInput {
  /** True/false once a cheap, unfiltered `GET /flows?limit=1` probe
   * resolves; `undefined` while it hasn't yet. Same probe
   * guest/guestEgoQueries.ts's useClusterHasAnyFlowsProbe already performs
   * for the Guest Ego view — reused directly by the panel rather than
   * re-implemented here, so the two features never disagree about what
   * "ingestion is on" means. */
  clusterHasAnyFlows: boolean | undefined;
  clusterProbeLoading: boolean;
  clusterProbeError: boolean;
  /** False once the scrubbed instant is older than the flows retention
   * window allows. Checked BEFORE the ingestion probe: "this instant
   * predates the ring" is a different fact from "asked, and ingestion is
   * off", and conflating them would blame ingestion for a window the ring
   * was never going to cover regardless. */
  inWindow: boolean;
  retentionMinutes: number;
  frameLoading: boolean;
  frameError: boolean;
  frameRecords: readonly FlowRecord[] | undefined;
}

export function deriveFlowPathsFrameState(input: FlowPathsFrameInput): FlowPathsFrameState {
  if (!input.inWindow) return { kind: "out-of-window", retentionMinutes: input.retentionMinutes };
  if (input.clusterProbeLoading || input.frameLoading) return { kind: "loading" };
  if (input.clusterProbeError || input.frameError) return { kind: "error" };
  if (input.clusterHasAnyFlows === false) return { kind: "ingestion-disabled" };
  if (!input.frameRecords || input.frameRecords.length === 0) return { kind: "empty" };
  return { kind: "data", records: input.frameRecords };
}

/** Human duration for the panel header's real-bounds disclosure — "24h" /
 * "60 min" — never a bare number an operator has to guess the unit of.
 * Kept here (not in the component) so it stays a plain, directly testable
 * function per this file's own "pure logic module" convention. */
export function formatReplayDuration(seconds: number): string {
  if (seconds <= 3600) return `${String(Math.round(seconds / 60))} min`;
  const hours = seconds / 3600;
  return `${hours % 1 === 0 ? String(hours) : hours.toFixed(1)}h`;
}

/** Short, human status line for the panel's flow-paths indicator — kept
 * here so the component and its tests agree on exact wording without
 * re-deriving strings inline. */
export function flowPathsFrameMessage(state: FlowPathsFrameState): string {
  switch (state.kind) {
    case "loading":
      return "Loading flow paths…";
    case "error":
      return "Could not load flow paths for this instant.";
    case "out-of-window":
      return `Flow paths are only retained for the last ${String(state.retentionMinutes)} minutes — outside that, only traffic heat (from interface counters) replays.`;
    case "ingestion-disabled":
      return "Flow ingestion is not enabled on this node — no flow paths were ever recorded to replay. Traffic heat still replays below.";
    case "empty":
      return "No flow paths recorded at this instant — traffic was quiet then, or fell between samples.";
    case "data":
      return `${String(state.records.length)} flow path${state.records.length === 1 ? "" : "s"} at this instant.`;
  }
}
