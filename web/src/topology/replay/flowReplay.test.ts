// SPDX-License-Identifier: Apache-2.0

// T-3910 acceptance criteria (planning/tasks/phase-39.md):
// 1. Replay window matches each ring's actual documented retention —
//    24h for metrics, [flows] retention_minutes (default 60, shorter) for
//    flows — never a single assumed figure for both.
// 4. Vitest coverage for the scrub/animation state, including both honest
//    empty cases: "flow ingestion is off" vs "no traffic in this window".
import { describe, expect, it } from "vitest";
import type { FlowRecord } from "../../api/types";
import { METRICS_RETENTION_SECONDS, computePlaybackRange } from "../history/historyPlayback";
import {
  REPLAY_STEP_SECONDS,
  clampReplayInstant,
  deriveFlowPathsFrameState,
  flowPathsFrameMessage,
  stepReplayInstant,
  tickReplay,
} from "./flowReplay";

const RECORD: FlowRecord = {
  at: 1_000_000,
  node: "pve1",
  srcIp: "10.0.0.5",
  dstIp: "10.0.0.6",
  proto: 6,
  bytes: 100,
  packets: 1,
  source: "netflow5",
};

describe("clampReplayInstant", () => {
  const range = computePlaybackRange(1_000_000, 60);

  it("passes through an instant already inside the range", () => {
    const mid = range.earliestAt + 100;
    expect(clampReplayInstant(mid, range)).toBe(mid);
  });

  it("clamps below earliestAt up to earliestAt", () => {
    expect(clampReplayInstant(range.earliestAt - 999, range)).toBe(range.earliestAt);
  });

  it("clamps above nowAt down to nowAt", () => {
    expect(clampReplayInstant(range.nowAt + 999, range)).toBe(range.nowAt);
  });
});

describe("stepReplayInstant", () => {
  const range = computePlaybackRange(1_000_000, 60);

  it("steps forward by the default granularity", () => {
    const at = range.earliestAt + 1000;
    expect(stepReplayInstant(at, range, 1)).toBe(at + REPLAY_STEP_SECONDS);
  });

  it("steps backward by the default granularity", () => {
    const at = range.earliestAt + 1000;
    expect(stepReplayInstant(at, range, -1)).toBe(at - REPLAY_STEP_SECONDS);
  });

  it("never steps past nowAt going forward", () => {
    expect(stepReplayInstant(range.nowAt - 5, range, 1, 30)).toBe(range.nowAt);
  });

  it("never steps before earliestAt going backward", () => {
    expect(stepReplayInstant(range.earliestAt + 5, range, -1, 30)).toBe(range.earliestAt);
  });

  it("honors a custom step size", () => {
    const at = range.earliestAt + 1000;
    expect(stepReplayInstant(at, range, 1, 120)).toBe(at + 120);
  });
});

describe("tickReplay", () => {
  const range = computePlaybackRange(1_000_000, 60);

  it("advances one frame and keeps playing while short of nowAt", () => {
    const at = range.nowAt - 1000;
    const result = tickReplay(at, range, 30);
    expect(result).toEqual({ at: at + 30, playing: true });
  });

  it("lands exactly on nowAt and stops when the next tick would reach it", () => {
    const at = range.nowAt - 30;
    expect(tickReplay(at, range, 30)).toEqual({ at: range.nowAt, playing: false });
  });

  it("clamps to nowAt (never overshoots into the future) and stops", () => {
    const at = range.nowAt - 10;
    expect(tickReplay(at, range, 30)).toEqual({ at: range.nowAt, playing: false });
  });
});

describe("computePlaybackRange (retention ground truth this task's card fixes)", () => {
  it("bounds metrics to 24h and flows to the shorter, configurable default of 60 minutes", () => {
    const range = computePlaybackRange(1_000_000, 60);
    expect(range.nowAt - range.earliestAt).toBe(METRICS_RETENTION_SECONDS);
    expect(METRICS_RETENTION_SECONDS).toBe(24 * 3600);
    expect(range.nowAt - range.flowsEarliestAt).toBe(60 * 60);
    expect(range.flowsEarliestAt).toBeGreaterThan(range.earliestAt);
  });
});

describe("deriveFlowPathsFrameState — the two honest empty cases", () => {
  const base = {
    clusterHasAnyFlows: true as boolean | undefined,
    clusterProbeLoading: false,
    clusterProbeError: false,
    inWindow: true,
    retentionMinutes: 60,
    frameLoading: false,
    frameError: false,
    frameRecords: undefined as readonly FlowRecord[] | undefined,
  };

  it("reports ingestion-disabled when the cluster-wide probe finds no flows at all", () => {
    const state = deriveFlowPathsFrameState({ ...base, clusterHasAnyFlows: false, frameRecords: [] });
    expect(state).toEqual({ kind: "ingestion-disabled" });
    expect(flowPathsFrameMessage(state)).toMatch(/not enabled/);
    expect(flowPathsFrameMessage(state)).toMatch(/heat still replays/);
  });

  it("reports empty (not ingestion-disabled) when ingestion is on but this instant has no records", () => {
    const state = deriveFlowPathsFrameState({ ...base, clusterHasAnyFlows: true, frameRecords: [] });
    expect(state).toEqual({ kind: "empty" });
    expect(flowPathsFrameMessage(state)).toMatch(/quiet/);
    expect(flowPathsFrameMessage(state)).not.toMatch(/not enabled/);
  });

  it("never confuses the two: ingestion-disabled and empty are distinct kinds with distinct wording", () => {
    const disabled = deriveFlowPathsFrameState({ ...base, clusterHasAnyFlows: false, frameRecords: [] });
    const empty = deriveFlowPathsFrameState({ ...base, clusterHasAnyFlows: true, frameRecords: [] });
    expect(disabled.kind).not.toBe(empty.kind);
    expect(flowPathsFrameMessage(disabled)).not.toBe(flowPathsFrameMessage(empty));
  });

  it("reports out-of-window before ever consulting the ingestion probe", () => {
    const state = deriveFlowPathsFrameState({ ...base, inWindow: false, clusterHasAnyFlows: undefined });
    expect(state).toEqual({ kind: "out-of-window", retentionMinutes: 60 });
    expect(flowPathsFrameMessage(state)).toMatch(/60 minutes/);
  });

  it("reports loading while either the probe or the frame fetch is in flight", () => {
    expect(deriveFlowPathsFrameState({ ...base, clusterProbeLoading: true }).kind).toBe("loading");
    expect(deriveFlowPathsFrameState({ ...base, frameLoading: true }).kind).toBe("loading");
  });

  it("reports error when either the probe or the frame fetch fails", () => {
    expect(deriveFlowPathsFrameState({ ...base, clusterProbeError: true }).kind).toBe("error");
    expect(deriveFlowPathsFrameState({ ...base, frameError: true }).kind).toBe("error");
  });

  it("reports data with the actual records once ingestion is on and this instant has traffic", () => {
    const state = deriveFlowPathsFrameState({ ...base, clusterHasAnyFlows: true, frameRecords: [RECORD] });
    expect(state).toEqual({ kind: "data", records: [RECORD] });
    expect(flowPathsFrameMessage(state)).toBe("1 flow path at this instant.");
  });
});
