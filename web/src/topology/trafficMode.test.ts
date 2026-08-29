// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import {
  computeUtilizationPct,
  resolveEdgeUtilizationRef,
  toneVar,
  trafficEdgeStyle,
  utilizationStrokeWidth,
  utilizationTone,
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

describe("utilizationTone (T-4303)", () => {
  // Colour names a severity BAND now, not a magnitude. Magnitude is
  // `utilizationStrokeWidth` below, which was always continuous and
  // monotonic and is a better channel for a quantity than hue is.
  it.each([
    [0, "outline"],
    [1, "outline"],
    [50, "outline"],
    [75, "outline"],
    [75.1, "status-degraded"],
    [90, "status-degraded"],
    [90.1, "status-critical"],
    [150, "status-critical"], // over 100% (bursty/measurement noise) still reads hot
  ] as const)("utilizationTone(%d) = %s", (pct, want) => {
    expect(utilizationTone(pct)).toBe(want);
  });

  it("treats negative/NaN as idle rather than throwing", () => {
    expect(utilizationTone(-5)).toBe("outline");
    expect(utilizationTone(Number.NaN)).toBe("outline");
  });

  it("names only design tokens, never a literal colour", () => {
    // The whole point of returning a token name is that neither renderer is
    // handed a hex. If a tone ever resolves to something that is not a
    // custom property, one of them has quietly grown its own palette again.
    for (const pct of [0, 80, 95]) {
      expect(toneVar(utilizationTone(pct))).toMatch(/^var\(--color-[a-z-]+\)$/);
    }
  });

  it("is monotonic in severity as utilization rises", () => {
    // The defect this card was opened for, asserted in the channel that now
    // carries the ordering. The old five-hue scale ran OKLCH lightness
    // 0.711, 0.754, 0.723, 0.769, 0.637 — up, down, up, down — so a viewer
    // could not tell more from less without a legend.
    const rank = { outline: 0, "status-degraded": 1, "status-critical": 2 } as const;
    const series = [0, 10, 40, 70, 75, 80, 90, 95, 150].map((p) => rank[utilizationTone(p)]);
    for (let i = 1; i < series.length; i++) {
      expect(series[i], `severity must not fall as utilization rises`).toBeGreaterThanOrEqual(series[i - 1] ?? 0);
    }
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
    expect(hot.tone).not.toBe(cold.tone);
    expect(hot.strokeWidth).toBeGreaterThan(cold.strokeWidth);
  });

  it("undefined (no live data yet) renders as idle, not blank/erroring", () => {
    expect(trafficEdgeStyle(undefined)).toEqual({ tone: "outline", strokeWidth: 1.5 });
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
