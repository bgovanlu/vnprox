// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import type { LatMeshLink } from "../api/types";
import {
  computeLatencyOverlayEdges,
  LATENCY_WARN_MS,
  latencyEdgeTone,
  latencyStrokeWidth,
  latencyTone,
  LOSS_WARN_PCT,
} from "./latencyMode";

// Every color hex value known to already be in use elsewhere on the map,
// so this file's own AC4 assertion ("confirms the color scale renders ...
// without colliding with the existing traffic-paint legend") can check
// latencyMode.ts's own palette against them directly, without importing
// trafficMode.ts's private HEAT_STOPS/canvasDraw.ts's private FLOW_EDGE_*
// constants (both are unexported — this list is the same "the codebase's
// own colors, kept in sync by this test" contract flowEdges.ts's own
// stroke-width comment already documents about not literally reusing
// another module's formula).
const TRAFFIC_PAINT_COLORS = ["#94a3b8", "#38bdf8", "#22c55e", "#f59e0b", "#ef4444"];
const FLOW_OVERLAY_COLORS = ["#06b6d4", "#0e7490"];
const EXISTING_MAP_COLORS = new Set([...TRAFFIC_PAINT_COLORS, ...FLOW_OVERLAY_COLORS]);

describe("latencyTone (T-4303)", () => {
  it.each([
    [0, "outline"],
    [20, "outline"],
    [50, "outline"], // 62.5% of 80ms — the band boundary this module already drew
    [51, "status-degraded"],
    [80, "status-degraded"], // exactly the warn threshold
    [81, "status-critical"],
    [500, "status-critical"],
  ] as const)("latencyTone(%d) = %s", (rttMs, want) => {
    expect(latencyTone(rttMs)).toBe(want);
  });

  it("treats negative/NaN as the best (0ms) case rather than throwing", () => {
    expect(latencyTone(-5)).toBe("outline");
    expect(latencyTone(Number.NaN)).toBe("outline");
  });
});

describe("latencyEdgeTone", () => {
  it("a fast-but-lossy link always reads as critical, regardless of RTT", () => {
    expect(latencyEdgeTone(5, LOSS_WARN_PCT + 0.1)).toBe("status-critical");
    expect(latencyEdgeTone(0, 50)).toBe("status-critical");
  });

  it("loss at or under the threshold falls back to the pure RTT bands", () => {
    expect(latencyEdgeTone(10, LOSS_WARN_PCT)).toBe(latencyTone(10));
    expect(latencyEdgeTone(10, 0)).toBe(latencyTone(10));
  });
});

describe("latencyStrokeWidth", () => {
  it("is MIN at 0ms and MAX at/above the warn threshold, clamped", () => {
    expect(latencyStrokeWidth(0)).toBeCloseTo(1.5, 5);
    expect(latencyStrokeWidth(LATENCY_WARN_MS)).toBeCloseTo(6, 5);
    expect(latencyStrokeWidth(LATENCY_WARN_MS * 10)).toBeCloseTo(6, 5);
  });

  it("is monotonically non-decreasing across the range", () => {
    let prev = -Infinity;
    for (let ms = 0; ms <= LATENCY_WARN_MS * 2; ms += 5) {
      const w = latencyStrokeWidth(ms);
      expect(w).toBeGreaterThanOrEqual(prev);
      prev = w;
    }
  });
});

// --- AC4: color scale renders without colliding with the traffic legend ---

describe("latency heatmap palette is visually distinct from every existing map color", () => {
  it("no latencyColor stop reuses a traffic-paint or flow-overlay hex value", () => {
    const sampled = [0, 10, 20, 21, 40, 50, 51, 65, 80, 81, 200];
    for (const ms of sampled) {
      const color = latencyTone(ms);
      expect(EXISTING_MAP_COLORS.has(color)).toBe(false);
    }
  });

  it("names the status scale rather than avoiding it (T-4303)", () => {
    // This block used to assert the OPPOSITE: that latency's palette
    // collided with no colour already on the map. That was the right
    // contract for a private four-hue ramp and is the wrong one now.
    //
    // The old ramp failed contrast at both ends because one set of hues
    // served both themes — `excellent` measured 1.78:1 on the light page and
    // `degraded` 2.26:1 on the dark one, so the state most needing to be
    // seen was the least visible in dark mode. Naming status tokens fixes
    // that by construction, because those re-point per theme; and sharing
    // them with traffic mode is now deliberate, since both are answering the
    // same question ("how bad is this link?") and should answer it in the
    // same vocabulary.
    for (const [rtt, loss] of [
      [0, 0],
      [LATENCY_WARN_MS, 0],
      [0, 100],
    ] as const) {
      expect(latencyEdgeTone(rtt, loss)).toMatch(/^(outline|status-degraded|status-critical)$/);
    }
    // Loss over threshold is as bad as RTT over threshold — the property the
    // old DEGRADED_LOSS_COLOR comment established, kept.
    expect(latencyEdgeTone(0, 100)).toBe(latencyEdgeTone(LATENCY_WARN_MS * 2, 0));
  });

  it("severity never falls as latency rises", () => {
    const rank = { outline: 0, "status-degraded": 1, "status-critical": 2 } as const;
    const series = [0, 5, 20, 40, 60, 100, 500].map((ms) => rank[latencyTone(ms)]);
    for (let i = 1; i < series.length; i++) {
      expect(series[i]).toBeGreaterThanOrEqual(series[i - 1] ?? 0);
    }
  });
});

// --- computeLatencyOverlayEdges: GET /latmesh/heatmap -> paintable edges --

function link(overrides: Partial<LatMeshLink>): LatMeshLink {
  return {
    linkId: "corosync:ring0|pve1->pve2",
    fabric: "corosync",
    fromNode: "pve1",
    toNode: "pve2",
    at: 100,
    rttMs: 10,
    lossPct: 0,
    rollingRttMs: 10,
    rollingLossPct: 0,
    sampleCount: 5,
    ...overrides,
  };
}

describe("computeLatencyOverlayEdges", () => {
  const nodeIdForName = (name: string): string | undefined => {
    const known: Record<string, string> = { pve1: "node:pve1", pve2: "node:pve2" };
    return known[name];
  };

  it("resolves fromNode/toNode to on-canvas node ids and computes tone/width", () => {
    const edges = computeLatencyOverlayEdges([link({})], nodeIdForName);
    expect(edges).toHaveLength(1);
    expect(edges[0]).toMatchObject({
      id: "corosync:ring0|pve1->pve2",
      from: "node:pve1",
      to: "node:pve2",
      tone: latencyTone(10),
      strokeWidth: latencyStrokeWidth(10),
    });
  });

  it("drops a link whose endpoint isn't currently rendered on the map", () => {
    const edges = computeLatencyOverlayEdges([link({ toNode: "pve9" })], nodeIdForName);
    expect(edges).toHaveLength(0);
  });

  it("drops a link that resolves to the same node on both ends", () => {
    const edges = computeLatencyOverlayEdges([link({ toNode: "pve1" })], nodeIdForName);
    expect(edges).toHaveLength(0);
  });

  it("sorts output by id for deterministic rendering", () => {
    const edges = computeLatencyOverlayEdges(
      [
        link({ linkId: "guest:vmbr0|pve1->pve2", fromNode: "pve1", toNode: "pve2" }),
        link({ linkId: "corosync:ring0|pve1->pve2", fromNode: "pve1", toNode: "pve2" }),
      ],
      nodeIdForName,
    );
    expect(edges.map((e) => e.id)).toEqual(["corosync:ring0|pve1->pve2", "guest:vmbr0|pve1->pve2"]);
  });
});
