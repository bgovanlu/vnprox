// SPDX-License-Identifier: Apache-2.0

// T-902 AC4: pure geometry coverage for the minimap overlay (minimap.ts) —
// the viewport-rectangle projection and drag-to-pan math, independent of
// the <canvas> element Minimap.tsx wraps (jsdom has no real 2d context, so
// that part is exercised by the Playwright scenario, web/e2e/lod.spec.ts).
import { describe, expect, it } from "vitest";
import type { SceneNode } from "./canvasScene";
import { MINIMAP_SIZE, computeMinimapViewport, panFromMinimapPoint, viewportRectOnMinimap } from "./minimap";

const NODES: SceneNode[] = [
  { id: "a", position: { x: 0, y: 0 } },
  { id: "b", position: { x: 2000, y: 0 } },
  { id: "c", position: { x: 0, y: 1000 } },
];

describe("computeMinimapViewport", () => {
  it("fits every node within the minimap's fixed pixel size", () => {
    const vp = computeMinimapViewport(NODES);
    expect(vp.zoom).toBeGreaterThan(0);
    expect(vp.zoom).toBeLessThanOrEqual(1);
  });

  it("degrades to an identity viewport for an empty node set (no NaN/Infinity)", () => {
    const vp = computeMinimapViewport([]);
    expect(Number.isFinite(vp.x)).toBe(true);
    expect(Number.isFinite(vp.y)).toBe(true);
    expect(Number.isFinite(vp.zoom)).toBe(true);
  });
});

describe("viewportRectOnMinimap", () => {
  it("projects the main canvas's visible graph rect into minimap screen space", () => {
    const minimapViewport = computeMinimapViewport(NODES);
    const mainView = { width: 1000, height: 800 };
    const mainViewport = { x: 0, y: 0, zoom: 1 };
    const rect = viewportRectOnMinimap(mainViewport, mainView, minimapViewport);
    expect(rect.width).toBeGreaterThan(0);
    expect(rect.height).toBeGreaterThan(0);
    // The rect sits within (or overlapping) the minimap's own pixel bounds
    // for a viewport that's actually looking at part of the content.
    expect(rect.x).toBeLessThan(MINIMAP_SIZE.width);
    expect(rect.y).toBeLessThan(MINIMAP_SIZE.height);
  });

  it("shrinks the rectangle as the main canvas zooms in (less area visible)", () => {
    const minimapViewport = computeMinimapViewport(NODES);
    const mainView = { width: 1000, height: 800 };
    const zoomedOut = viewportRectOnMinimap({ x: 0, y: 0, zoom: 0.5 }, mainView, minimapViewport);
    const zoomedIn = viewportRectOnMinimap({ x: 0, y: 0, zoom: 2 }, mainView, minimapViewport);
    expect(zoomedIn.width).toBeLessThan(zoomedOut.width);
    expect(zoomedIn.height).toBeLessThan(zoomedOut.height);
  });
});

describe("panFromMinimapPoint", () => {
  it("recenters the main viewport on the graph point under the minimap click, preserving zoom", () => {
    const minimapViewport = computeMinimapViewport(NODES);
    const mainView = { width: 1000, height: 800 };
    const mainViewport = { x: 10, y: 20, zoom: 1.5 };
    // Click the minimap point that corresponds to node "b" (2000, 0).
    const minimapPoint = { x: 2000 * minimapViewport.zoom + minimapViewport.x, y: 0 * minimapViewport.zoom + minimapViewport.y };
    const next = panFromMinimapPoint(minimapPoint, minimapViewport, mainViewport, mainView);
    expect(next.zoom).toBe(mainViewport.zoom); // panning never changes zoom
    // Node "b"'s graph point should now project to the center of the main view.
    const screenX = 2000 * next.zoom + next.x;
    const screenY = 0 * next.zoom + next.y;
    expect(screenX).toBeCloseTo(mainView.width / 2, 5);
    expect(screenY).toBeCloseTo(mainView.height / 2, 5);
  });
});
