// Pure scene geometry for the v2 canvas renderer (TopologyCanvasV2.tsx).
// Deliberately framework-free — no React, no @xyflow/react, no canvas
// context — so the viewport math, fit-to-view, hit-testing, and zoom-at-
// cursor logic that determines "what did the user click on, at any zoom
// level" is exhaustively Vitest-able without a browser or a real
// CanvasRenderingContext2D (see canvasScene.test.ts). The component in
// TopologyCanvasV2.tsx is then a thin shell: it owns the <canvas> element,
// pointer events, and the actual draw calls, and delegates every "where is
// this in graph/screen space" question here.
//
// Coordinate spaces:
//   - GRAPH space: the {x,y} the layout (layout.ts) and toFlowElements emit,
//     the same numbers the v1 React Flow renderer positions nodes at. A node
//     occupies a NODE box (V2_NODE_WIDTH x V2_NODE_HEIGHT) with its position
//     at the box's top-left, matching @xyflow/react's node-position
//     convention — so a layout saved/positioned under v1 lands pixel-identical
//     under v2 (T-901 AC4).
//   - SCREEN space: CSS pixels relative to the canvas container's top-left.
// The transform between them is an affine pan+uniform-zoom:
//     screen = graph * zoom + {x,y}
//     graph  = (screen - {x,y}) / zoom
import type { XYPosition } from "./layout";

export interface Viewport {
  /** Screen-space translation (pan), CSS px. */
  x: number;
  y: number;
  /** Uniform scale factor (1 = 100%). */
  zoom: number;
}

export interface Rect {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface Size {
  width: number;
  height: number;
}

/** A drawn node's minimal geometry input — the subset of a FlowElements node
 * the scene layer needs (its id and graph-space top-left). */
export interface SceneNode {
  id: string;
  position: XYPosition;
}

// The v2 node box. Slightly smaller than layout.ts's NODE_WIDTH/NODE_HEIGHT
// (180x56) cell so a box never visually collides with its neighbor's cell,
// while still sitting at the same top-left the layout computed.
export const V2_NODE_WIDTH = 172;
export const V2_NODE_HEIGHT = 52;

export const DEFAULT_NODE_SIZE: Size = { width: V2_NODE_WIDTH, height: V2_NODE_HEIGHT };

export const MIN_ZOOM = 0.05;
export const MAX_ZOOM = 2;

export function clampZoom(zoom: number): number {
  return Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, zoom));
}

/** GRAPH -> SCREEN. */
export function graphToScreen(p: XYPosition, vp: Viewport): XYPosition {
  return { x: p.x * vp.zoom + vp.x, y: p.y * vp.zoom + vp.y };
}

/** SCREEN -> GRAPH. */
export function screenToGraph(p: XYPosition, vp: Viewport): XYPosition {
  return { x: (p.x - vp.x) / vp.zoom, y: (p.y - vp.y) / vp.zoom };
}

/** A node's graph-space box. */
export function nodeRect(position: XYPosition, size: Size = DEFAULT_NODE_SIZE): Rect {
  return { x: position.x, y: position.y, width: size.width, height: size.height };
}

/** A node's screen-space box (top-left + scaled dimensions). */
export function nodeScreenRect(position: XYPosition, vp: Viewport, size: Size = DEFAULT_NODE_SIZE): Rect {
  const tl = graphToScreen(position, vp);
  return { x: tl.x, y: tl.y, width: size.width * vp.zoom, height: size.height * vp.zoom };
}

function graphBounds(nodes: readonly SceneNode[], size: Size): Rect | undefined {
  if (nodes.length === 0) return undefined;
  let minX = Infinity;
  let minY = Infinity;
  let maxX = -Infinity;
  let maxY = -Infinity;
  for (const n of nodes) {
    minX = Math.min(minX, n.position.x);
    minY = Math.min(minY, n.position.y);
    maxX = Math.max(maxX, n.position.x + size.width);
    maxY = Math.max(maxY, n.position.y + size.height);
  }
  return { x: minX, y: minY, width: maxX - minX, height: maxY - minY };
}

/**
 * Computes a viewport that fits every node's box within `view` with `padding`
 * CSS px of margin — the v2 equivalent of React Flow's `fitView`, so the
 * initial render frames the whole cluster the same way v1 does. Empty node
 * set (or a zero-size container, e.g. pre-layout) returns an identity
 * viewport rather than a NaN/Infinity one.
 */
export function fitViewport(
  nodes: readonly SceneNode[],
  view: Size,
  size: Size = DEFAULT_NODE_SIZE,
  padding = 48,
): Viewport {
  const bounds = graphBounds(nodes, size);
  if (!bounds || view.width <= 0 || view.height <= 0 || bounds.width <= 0 || bounds.height <= 0) {
    return { x: padding, y: padding, zoom: 1 };
  }
  const zoom = clampZoom(
    Math.min((view.width - padding * 2) / bounds.width, (view.height - padding * 2) / bounds.height),
  );
  // Center the scaled bounds within the view.
  const contentW = bounds.width * zoom;
  const contentH = bounds.height * zoom;
  const x = (view.width - contentW) / 2 - bounds.x * zoom;
  const y = (view.height - contentH) / 2 - bounds.y * zoom;
  return { x, y, zoom };
}

function rectContains(r: Rect, p: XYPosition): boolean {
  return p.x >= r.x && p.x <= r.x + r.width && p.y >= r.y && p.y <= r.y + r.height;
}

/**
 * Resolves a screen point to the node under it, at any zoom level, returning
 * the *topmost* (last in `nodes` draw order — z-order matches array order,
 * later drawn on top) node whose box contains the point, or undefined for an
 * empty-canvas ("pane") hit. Works purely in graph space (converting the
 * screen point once) so it is scale-invariant — a click resolves to the same
 * entity whether zoomed to 5% or 200%, including where boxes overlap
 * (T-901's "hit-testing at all zoom levels, incl. overlapping chips").
 * `excludeId` skips a node (used during drag, so a node never counts as its
 * own drop target).
 */
export function hitTest(
  nodes: readonly SceneNode[],
  screenPt: XYPosition,
  vp: Viewport,
  size: Size = DEFAULT_NODE_SIZE,
  excludeId?: string,
): string | undefined {
  const g = screenToGraph(screenPt, vp);
  for (let i = nodes.length - 1; i >= 0; i--) {
    const n = nodes[i];
    if (n === undefined || n.id === excludeId) continue;
    if (rectContains(nodeRect(n.position, size), g)) return n.id;
  }
  return undefined;
}

/**
 * Zooms by `factor` while keeping the graph point currently under `screenPt`
 * pinned to `screenPt` (zoom-to-cursor / zoom-to-wheel-position), the natural
 * pan/zoom gesture. Clamped to [MIN_ZOOM, MAX_ZOOM].
 */
export function zoomAt(vp: Viewport, screenPt: XYPosition, factor: number): Viewport {
  const nextZoom = clampZoom(vp.zoom * factor);
  if (nextZoom === vp.zoom) return vp;
  // Keep the graph coord under the cursor fixed: solve for the new pan.
  const g = screenToGraph(screenPt, vp);
  return {
    zoom: nextZoom,
    x: screenPt.x - g.x * nextZoom,
    y: screenPt.y - g.y * nextZoom,
  };
}

/** Applies a screen-space pan delta (drag-to-pan). */
export function panBy(vp: Viewport, dx: number, dy: number): Viewport {
  return { ...vp, x: vp.x + dx, y: vp.y + dy };
}
