import { describe, expect, it } from "vitest";
import type { LatMeshLink } from "../api/types";
import {
  computeLatencyOverlayEdges,
  LATENCY_WARN_MS,
  latencyColor,
  latencyEdgeColor,
  latencyStrokeWidth,
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

describe("latencyColor", () => {
  it.each([
    [0, "#c4b5fd"],
    [20, "#c4b5fd"], // 25% of 80ms
    [21, "#a78bfa"],
    [50, "#a78bfa"], // 62.5% of 80ms
    [51, "#c026d3"],
    [80, "#c026d3"], // exactly the warn threshold
    [81, "#9d174d"],
    [500, "#9d174d"],
  ])("latencyColor(%d) = %s", (rttMs, want) => {
    expect(latencyColor(rttMs)).toBe(want);
  });

  it("treats negative/NaN as the best (0ms) case rather than throwing or returning a bogus color", () => {
    expect(latencyColor(-5)).toBe("#c4b5fd");
    expect(latencyColor(Number.NaN)).toBe("#c4b5fd");
  });
});

describe("latencyEdgeColor", () => {
  it("a fast-but-lossy link always reads as degraded, regardless of RTT", () => {
    expect(latencyEdgeColor(5, LOSS_WARN_PCT + 0.1)).toBe("#9d174d");
    expect(latencyEdgeColor(0, 50)).toBe("#9d174d");
  });

  it("loss at or under the threshold falls back to the pure RTT scale", () => {
    expect(latencyEdgeColor(10, LOSS_WARN_PCT)).toBe(latencyColor(10));
    expect(latencyEdgeColor(10, 0)).toBe(latencyColor(10));
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
      const color = latencyColor(ms);
      expect(EXISTING_MAP_COLORS.has(color)).toBe(false);
    }
  });

  it("the degraded-loss color also never collides", () => {
    expect(EXISTING_MAP_COLORS.has(latencyEdgeColor(0, 100))).toBe(false);
  });

  it("the palette has no internal duplicate colors either (four visually distinct stops)", () => {
    const colors = [0, 21, 51, 81].map(latencyColor);
    expect(new Set(colors).size).toBe(colors.length);
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

  it("resolves fromNode/toNode to on-canvas node ids and computes color/width", () => {
    const edges = computeLatencyOverlayEdges([link({})], nodeIdForName);
    expect(edges).toHaveLength(1);
    expect(edges[0]).toMatchObject({
      id: "corosync:ring0|pve1->pve2",
      from: "node:pve1",
      to: "node:pve2",
      color: latencyColor(10),
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
