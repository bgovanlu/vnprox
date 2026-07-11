import { describe, expect, it } from "vitest";
import {
  computeUtilizationPct,
  resolveEdgeUtilizationRef,
  trafficEdgeStyle,
  utilizationColor,
  utilizationStrokeWidth,
} from "./trafficMode";

describe("computeUtilizationPct", () => {
  it.each([
    [500_000_000, 1000, 50],
    [1_000_000_000, 1000, 100],
    [1_000_000, 10, 10],
  ])("computeUtilizationPct(%d, %d) = %d", (bps, speedMbps, want) => {
    expect(computeUtilizationPct(bps, speedMbps)).toBe(want);
  });

  it("unknown speed (undefined) reports 0, not NaN", () => {
    expect(computeUtilizationPct(1_000_000, undefined)).toBe(0);
  });

  it("non-positive speed or bps reports 0", () => {
    expect(computeUtilizationPct(1_000_000, 0)).toBe(0);
    expect(computeUtilizationPct(0, 1000)).toBe(0);
  });
});

describe("utilizationColor", () => {
  it.each([
    [0, "#94a3b8"],
    [1, "#94a3b8"],
    [10, "#38bdf8"],
    [25, "#38bdf8"],
    [40, "#22c55e"],
    [50, "#22c55e"],
    [60, "#f59e0b"],
    [75, "#f59e0b"],
    [90, "#ef4444"],
    [150, "#ef4444"], // over 100% (bursty/measurement noise) still reads "hot", not an error color
  ])("utilizationColor(%d) = %s", (pct, want) => {
    expect(utilizationColor(pct)).toBe(want);
  });

  it("treats negative/NaN as idle rather than throwing or returning a bogus color", () => {
    expect(utilizationColor(-5)).toBe("#94a3b8");
    expect(utilizationColor(Number.NaN)).toBe("#94a3b8");
  });
});

describe("utilizationStrokeWidth", () => {
  it("is monotonically non-decreasing across the visible range, distinguishing hot from cold", () => {
    const cold = utilizationStrokeWidth(2);
    const warm = utilizationStrokeWidth(50);
    const hot = utilizationStrokeWidth(95);
    expect(cold).toBeLessThan(warm);
    expect(warm).toBeLessThan(hot);
  });

  it("clamps at both ends", () => {
    expect(utilizationStrokeWidth(0)).toBe(1.5);
    expect(utilizationStrokeWidth(100)).toBe(6);
    expect(utilizationStrokeWidth(500)).toBe(6); // over 100%, not thicker than the cap
    expect(utilizationStrokeWidth(-10)).toBe(1.5);
  });
});

describe("trafficEdgeStyle", () => {
  it("a hot bond is visibly distinct from a cold one (AC2)", () => {
    const hot = trafficEdgeStyle(92);
    const cold = trafficEdgeStyle(3);
    expect(hot.stroke).not.toBe(cold.stroke);
    expect(hot.strokeWidth).toBeGreaterThan(cold.strokeWidth);
  });

  it("undefined (no live data yet) renders as idle, not blank/erroring", () => {
    expect(trafficEdgeStyle(undefined)).toEqual({ stroke: "#94a3b8", strokeWidth: 1.5 });
  });
});

describe("resolveEdgeUtilizationRef", () => {
  const kindOf = (ref: string): string | undefined => {
    if (ref.startsWith("bond:")) return "bond";
    if (ref.startsWith("physnic:")) return "physnic";
    if (ref.startsWith("bridge:")) return "bridge";
    return undefined;
  };

  it("prefers the bond over the bridge when both have live data", () => {
    const data = new Map([
      ["bond:pve1:bond0", 80],
      ["bridge:pve1:vmbr0", 5],
    ]);
    expect(resolveEdgeUtilizationRef("bond:pve1:bond0", "bridge:pve1:vmbr0", kindOf, data)).toBe("bond:pve1:bond0");
  });

  it("falls back to whichever endpoint has data when only one does", () => {
    const data = new Map([["bridge:pve1:vmbr0", 12]]);
    expect(resolveEdgeUtilizationRef("bond:pve1:bond0", "bridge:pve1:vmbr0", kindOf, data)).toBe("bridge:pve1:vmbr0");
  });

  it("returns undefined when neither endpoint has live data", () => {
    expect(resolveEdgeUtilizationRef("bond:pve1:bond0", "bridge:pve1:vmbr0", kindOf, new Map())).toBeUndefined();
  });
});
