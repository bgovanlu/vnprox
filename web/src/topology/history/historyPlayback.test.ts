import { describe, expect, it } from "vitest";
import {
  DEFAULT_FLOW_RETENTION_MINUTES,
  METRICS_RETENTION_SECONDS,
  computePlaybackRange,
  flowsAvailableAt,
  historicalUtilizationByRef,
  nearestHistoryPoint,
} from "./historyPlayback";
import type { HistoryPoint } from "../../api/types";

const ZERO_RATES = { rxBps: 0, txBps: 0, rxPps: 0, txPps: 0, rxErrsPerSec: 0, txErrsPerSec: 0, rxDropPerSec: 0, txDropPerSec: 0 };

describe("computePlaybackRange", () => {
  it("clamps to 24h metrics window and the (smaller) flow window by default", () => {
    const now = 1_000_000;
    const range = computePlaybackRange(now);
    expect(range.nowAt).toBe(now);
    expect(range.earliestAt).toBe(now - METRICS_RETENTION_SECONDS);
    expect(range.flowsEarliestAt).toBe(now - DEFAULT_FLOW_RETENTION_MINUTES * 60);
    expect(range.flowsEarliestAt).toBeGreaterThan(range.earliestAt);
  });

  it("bounds an oversized flow retention to the 24h metrics window itself", () => {
    const now = 1_000_000;
    const range = computePlaybackRange(now, 48 * 60); // 48h > 24h metrics ceiling
    expect(range.flowsEarliestAt).toBe(range.earliestAt);
  });

  it("degrades a non-positive flow retention to 'no flow history at all'", () => {
    const now = 1_000_000;
    const range = computePlaybackRange(now, 0);
    expect(range.flowsEarliestAt).toBe(now);
    const range2 = computePlaybackRange(now, -5);
    expect(range2.flowsEarliestAt).toBe(now);
  });
});

describe("flowsAvailableAt", () => {
  const range = computePlaybackRange(1_000_000, 60);

  it("is true at or after flowsEarliestAt", () => {
    expect(flowsAvailableAt(range.flowsEarliestAt, range)).toBe(true);
    expect(flowsAvailableAt(range.nowAt, range)).toBe(true);
  });

  it("is false before flowsEarliestAt", () => {
    expect(flowsAvailableAt(range.flowsEarliestAt - 1, range)).toBe(false);
    expect(flowsAvailableAt(range.earliestAt, range)).toBe(false);
  });
});

describe("nearestHistoryPoint", () => {
  const points: HistoryPoint[] = [
    { at: 100, rates: ZERO_RATES },
    { at: 200, rates: ZERO_RATES },
    { at: 400, rates: ZERO_RATES },
  ];

  it("returns undefined for an empty input", () => {
    const empty: HistoryPoint[] = [];
    const result = nearestHistoryPoint(empty, 100);
    expect(result).toBeUndefined();
  });

  it("picks the exact match when present", () => {
    expect(nearestHistoryPoint(points, 200)?.at).toBe(200);
  });

  it("picks the closest point when there is no exact match", () => {
    expect(nearestHistoryPoint(points, 250)?.at).toBe(200);
    expect(nearestHistoryPoint(points, 350)?.at).toBe(400);
  });

  it("breaks exact ties toward the earlier candidate", () => {
    expect(nearestHistoryPoint(points, 150)?.at).toBe(100);
  });
});

describe("historicalUtilizationByRef", () => {
  it("computes utilization from historical rates + a link-speed snapshot, mirroring live utilizationMap math", () => {
    const points = new Map([
      ["bond:pve1:bond0", { at: 100, rates: { ...ZERO_RATES, rxBps: 500_000_000, txBps: 100_000_000 } }],
    ]);
    const speeds = new Map([["bond:pve1:bond0", 1000]]); // 1 Gbps
    const result = historicalUtilizationByRef(points, speeds);
    expect(result.get("bond:pve1:bond0")).toBeCloseTo(50, 5); // 500Mbps / 1000Mbps = 50%
  });

  it("omits a ref with no nearby sample", () => {
    const points = new Map<string, HistoryPoint | undefined>([["bond:pve1:bond0", undefined]]);
    const speeds = new Map([["bond:pve1:bond0", 1000]]);
    expect(historicalUtilizationByRef(points, speeds).has("bond:pve1:bond0")).toBe(false);
  });

  it("omits a ref with unknown link speed (matches computeUtilizationPct's 0-report, filtered by the >0 guard)", () => {
    const points = new Map([["bond:pve1:bond0", { at: 100, rates: { ...ZERO_RATES, rxBps: 500_000_000 } }]]);
    const speeds = new Map<string, number | undefined>([["bond:pve1:bond0", undefined]]);
    expect(historicalUtilizationByRef(points, speeds).has("bond:pve1:bond0")).toBe(false);
  });
});
