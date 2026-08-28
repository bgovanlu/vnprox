// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { HUB_POSITION, radialClusterLayout, type LayoutPoint } from "./clusterLayout";

/** `points[i]` under `noUncheckedIndexedAccess` is `LayoutPoint | undefined`;
 * every test below already asserts the array's length, so a missing index
 * here is a bug this NaN sentinel makes `toBeCloseTo` fail loudly on rather
 * than a non-null assertion (banned by this repo's eslint config) papering
 * over it. */
function at(points: readonly LayoutPoint[], i: number): LayoutPoint {
  return points[i] ?? { x: NaN, y: NaN };
}

describe("radialClusterLayout", () => {
  it("returns an empty array for zero (or negative) clusters", () => {
    expect(radialClusterLayout(0)).toEqual([]);
    expect(radialClusterLayout(-1)).toEqual([]);
  });

  it("places a single cluster straight above the hub (12 o'clock)", () => {
    const points = radialClusterLayout(1, 30);
    expect(points).toHaveLength(1);
    const p = at(points, 0);
    expect(p.x).toBeCloseTo(HUB_POSITION.x, 5);
    expect(p.y).toBeCloseTo(HUB_POSITION.y - 30, 5);
  });

  it("returns exactly one point per cluster", () => {
    expect(radialClusterLayout(5)).toHaveLength(5);
  });

  it("is deterministic — the same count always yields the same points", () => {
    expect(radialClusterLayout(4)).toEqual(radialClusterLayout(4));
  });

  it("spaces points evenly — every point is equidistant from the hub, and consecutive points subtend the same angle", () => {
    const radius = 25;
    const points = radialClusterLayout(6, radius);
    for (const p of points) {
      const dist = Math.hypot(p.x - HUB_POSITION.x, p.y - HUB_POSITION.y);
      expect(dist).toBeCloseTo(radius, 5);
    }
    // Opposite points on a 6-point circle (index 0 and 3) are diametrically
    // opposite the hub.
    const p0 = at(points, 0);
    const p3 = at(points, 3);
    expect(p3.x).toBeCloseTo(2 * HUB_POSITION.x - p0.x, 5);
    expect(p3.y).toBeCloseTo(2 * HUB_POSITION.y - p0.y, 5);
  });

  it("places 4 clusters at the four cardinal points around the hub", () => {
    const points = radialClusterLayout(4, 20);
    expect(points).toHaveLength(4);
    const [top, right, bottom, left] = [at(points, 0), at(points, 1), at(points, 2), at(points, 3)];
    expect(top.x).toBeCloseTo(HUB_POSITION.x, 5);
    expect(top.y).toBeCloseTo(HUB_POSITION.y - 20, 5);
    expect(right.x).toBeCloseTo(HUB_POSITION.x + 20, 5);
    expect(right.y).toBeCloseTo(HUB_POSITION.y, 5);
    expect(bottom.x).toBeCloseTo(HUB_POSITION.x, 5);
    expect(bottom.y).toBeCloseTo(HUB_POSITION.y + 20, 5);
    expect(left.x).toBeCloseTo(HUB_POSITION.x - 20, 5);
    expect(left.y).toBeCloseTo(HUB_POSITION.y, 5);
  });
});
