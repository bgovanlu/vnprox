// SPDX-License-Identifier: Apache-2.0

// T-3909: a deterministic, dependency-free layout for the global map's
// hub-and-spoke shape — the local vnprox instance (the hub every WireGuard
// interconnect tunnel actually originates from — see interconnects.ts's own
// doc comment on why `Cluster.wgTunnelId` always names a LOCAL tunnel) at
// the centre, one attached cluster capsule per spoke, evenly spaced on a
// circle around it.
//
// No graph-layout package (CLAUDE.md: no new major dependency without a
// note) and no DOM measurement (`getBoundingClientRect` reads 0 in jsdom,
// and a reflow-dependent position isn't deterministic across renders or
// test runs anyway) — purely a function of how many clusters are attached,
// so it Vitest-asserts as plain arithmetic and never needs a real browser
// layout pass to verify.
export interface LayoutPoint {
  readonly x: number; // percent of the container box, 0-100
  readonly y: number; // percent of the container box, 0-100
}

/** The hub's fixed position — dead centre of the layout box. */
export const HUB_POSITION: LayoutPoint = { x: 50, y: 50 };

/** One point per cluster, evenly spaced on a circle around HUB_POSITION,
 * starting straight up (12 o'clock) and proceeding clockwise — a fixed,
 * content-independent start angle so the layout reads the same way on every
 * render instead of depending on attach order or a random seed. `radius` is
 * a percent of the container's own box; the default keeps every point
 * comfortably inside the 0-100 square a capsule's own footprint still needs
 * room around. Returns `[]` for a non-positive count. */
export function radialClusterLayout(count: number, radius = 32): LayoutPoint[] {
  if (count <= 0) return [];
  const points: LayoutPoint[] = [];
  for (let i = 0; i < count; i++) {
    const angle = -Math.PI / 2 + (2 * Math.PI * i) / count;
    points.push({
      x: HUB_POSITION.x + radius * Math.cos(angle),
      y: HUB_POSITION.y + radius * Math.sin(angle),
    });
  }
  return points;
}
