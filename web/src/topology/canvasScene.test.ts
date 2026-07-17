// Unit coverage for the v2 canvas scene geometry (canvasScene.ts): the
// viewport transforms, fit-to-view, zoom-at-cursor, and — the load-bearing
// one for T-901's "hit-testing at all zoom levels, incl. overlapping chips"
// acceptance — hitTest resolving a screen point to the correct entity
// regardless of zoom.
import { describe, expect, it } from "vitest";
import {
  fitViewport,
  graphToScreen,
  hitTest,
  nodeScreenRect,
  panBy,
  screenToGraph,
  zoomAt,
  clampZoom,
  MAX_ZOOM,
  MIN_ZOOM,
  type SceneNode,
  type Viewport,
} from "./canvasScene";

const NODES: SceneNode[] = [
  { id: "a", position: { x: 0, y: 0 } },
  { id: "b", position: { x: 400, y: 0 } },
  { id: "c", position: { x: 0, y: 300 } },
];

describe("graph<->screen transforms are inverses", () => {
  it("round-trips a point through screenToGraph(graphToScreen())", () => {
    const vp: Viewport = { x: 37, y: -12, zoom: 1.75 };
    const p = { x: 123, y: 456 };
    const back = screenToGraph(graphToScreen(p, vp), vp);
    expect(back.x).toBeCloseTo(p.x, 6);
    expect(back.y).toBeCloseTo(p.y, 6);
  });
});

describe("clampZoom", () => {
  it("clamps to [MIN_ZOOM, MAX_ZOOM]", () => {
    expect(clampZoom(1000)).toBe(MAX_ZOOM);
    expect(clampZoom(0)).toBe(MIN_ZOOM);
    expect(clampZoom(1)).toBe(1);
  });
});

describe("fitViewport", () => {
  it("frames every node's box inside the view", () => {
    const vp = fitViewport(NODES, { width: 800, height: 600 });
    for (const n of NODES) {
      const r = nodeScreenRect(n.position, vp);
      expect(r.x).toBeGreaterThanOrEqual(-1);
      expect(r.y).toBeGreaterThanOrEqual(-1);
      expect(r.x + r.width).toBeLessThanOrEqual(801);
      expect(r.y + r.height).toBeLessThanOrEqual(601);
    }
  });

  it("returns a finite identity-ish viewport for an empty/zero-size input", () => {
    const empty = fitViewport([], { width: 800, height: 600 });
    expect(Number.isFinite(empty.x)).toBe(true);
    expect(Number.isFinite(empty.zoom)).toBe(true);
    const zero = fitViewport(NODES, { width: 0, height: 0 });
    expect(Number.isFinite(zero.zoom)).toBe(true);
  });
});

describe("hitTest resolves the correct entity at any zoom", () => {
  for (const zoom of [MIN_ZOOM, 0.25, 1, 2]) {
    it(`resolves a node center at zoom=${String(zoom)}`, () => {
      const vp: Viewport = { x: 100, y: 80, zoom };
      for (const n of NODES) {
        // A point at the node's graph-space center -> its screen point.
        const center = graphToScreen({ x: n.position.x + 20, y: n.position.y + 20 }, vp);
        expect(hitTest(NODES, center, vp)).toBe(n.id);
      }
    });
  }

  it("returns undefined for an empty-canvas (pane) hit", () => {
    const vp: Viewport = { x: 0, y: 0, zoom: 1 };
    expect(hitTest(NODES, { x: 5000, y: 5000 }, vp)).toBeUndefined();
  });

  it("picks the topmost (last-drawn) node when boxes overlap", () => {
    const overlapping: SceneNode[] = [
      { id: "under", position: { x: 100, y: 100 } },
      { id: "over", position: { x: 110, y: 110 } }, // drawn later => on top
    ];
    const vp: Viewport = { x: 0, y: 0, zoom: 1 };
    const pt = graphToScreen({ x: 120, y: 120 }, vp); // inside both boxes
    expect(hitTest(overlapping, pt, vp)).toBe("over");
  });

  it("excludeId skips a node (so a drag never targets itself)", () => {
    const vp: Viewport = { x: 0, y: 0, zoom: 1 };
    const pt = graphToScreen({ x: 20, y: 20 }, vp);
    expect(hitTest(NODES, pt, vp)).toBe("a");
    expect(hitTest(NODES, pt, vp, undefined, "a")).toBeUndefined();
  });
});

describe("zoomAt keeps the point under the cursor fixed", () => {
  it("pins the graph coord under the cursor across a zoom step", () => {
    const vp: Viewport = { x: 50, y: 50, zoom: 1 };
    const cursor = { x: 300, y: 200 };
    const before = screenToGraph(cursor, vp);
    const zoomed = zoomAt(vp, cursor, 1.5);
    const after = screenToGraph(cursor, zoomed);
    expect(after.x).toBeCloseTo(before.x, 4);
    expect(after.y).toBeCloseTo(before.y, 4);
    expect(zoomed.zoom).toBeCloseTo(1.5, 6);
  });

  it("does not exceed zoom bounds", () => {
    const vp: Viewport = { x: 0, y: 0, zoom: MAX_ZOOM };
    expect(zoomAt(vp, { x: 0, y: 0 }, 2).zoom).toBe(MAX_ZOOM);
  });
});

describe("panBy", () => {
  it("shifts the viewport translation, not the zoom", () => {
    const vp: Viewport = { x: 10, y: 20, zoom: 1.3 };
    const p = panBy(vp, 5, -7);
    expect(p).toEqual({ x: 15, y: 13, zoom: 1.3 });
  });
});
