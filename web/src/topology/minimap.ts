// SPDX-License-Identifier: Apache-2.0

// T-902: pure geometry for the topology map's minimap overlay. Framework-
// free (no React, no canvas context) like canvasScene.ts, which this module
// builds directly on rather than reinventing viewport math: the minimap is
// literally a second, small "fit everything" viewport over the exact same
// SceneNode set the main canvas draws, plus a rectangle showing where the
// main canvas's own viewport currently looks within that overview.
import type { Rect, SceneNode, Size, Viewport } from "./canvasScene";
import { DEFAULT_NODE_SIZE, fitViewport, graphToScreen, screenToGraph } from "./canvasScene";
import type { XYPosition } from "./layout";

export const MINIMAP_SIZE: Size = { width: 180, height: 130 };
const MINIMAP_PADDING = 8;

/** The minimap's own "camera": a fit-to-all-nodes viewport, recomputed
 * whenever the node set changes (mirrors canvasScene.ts's fitViewport, used
 * by TopologyCanvasV2 itself for the initial main-canvas fit). */
export function computeMinimapViewport(nodes: readonly SceneNode[]): Viewport {
  return fitViewport(nodes, MINIMAP_SIZE, DEFAULT_NODE_SIZE, MINIMAP_PADDING);
}

/** Projects the main canvas's currently-visible graph rect into minimap
 * screen space — the draggable rectangle a user sees/drags to pan. */
export function viewportRectOnMinimap(mainViewport: Viewport, mainView: Size, minimapViewport: Viewport): Rect {
  const topLeftGraph = screenToGraph({ x: 0, y: 0 }, mainViewport);
  const bottomRightGraph = screenToGraph({ x: mainView.width, y: mainView.height }, mainViewport);
  const topLeft = graphToScreen(topLeftGraph, minimapViewport);
  const bottomRight = graphToScreen(bottomRightGraph, minimapViewport);
  return {
    x: topLeft.x,
    y: topLeft.y,
    width: bottomRight.x - topLeft.x,
    height: bottomRight.y - topLeft.y,
  };
}

/** Recenters the main viewport so the graph point under a minimap-space
 * pointer position becomes the center of the main view — dragging the
 * minimap rectangle pans the main canvas; it never changes zoom. */
export function panFromMinimapPoint(minimapPoint: XYPosition, minimapViewport: Viewport, mainViewport: Viewport, mainView: Size): Viewport {
  const graphPoint = screenToGraph(minimapPoint, minimapViewport);
  return {
    zoom: mainViewport.zoom,
    x: mainView.width / 2 - graphPoint.x * mainViewport.zoom,
    y: mainView.height / 2 - graphPoint.y * mainViewport.zoom,
  };
}
