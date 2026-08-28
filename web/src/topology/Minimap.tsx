// SPDX-License-Identifier: Apache-2.0

// T-902: the v2 canvas's minimap overlay — a small overview canvas plus a
// viewport rectangle that pans the main canvas when dragged. Self-contained
// pointer handling (its own drag state, not TopologyCanvasV2's pan/node
// gesture machinery) so it can sit as a plain absolutely-positioned corner
// child inside TopologyCanvasV2's container without any special-casing in
// that component's own pointer handlers — every pointer event here stops
// propagation so the main canvas's pane-pan/hit-testing never also fires.
//
// Two stacked <canvas> layers, not one, for a perf reason caught during
// T-902's own perf verification (see the report): the main canvas's
// viewport changes on every pan/zoom animation frame, but the node-dot
// overview only needs to change when the entity set itself changes
// (topology refetch, or a LOD band collapsing/expanding entities) — pan/
// zoom never moves a node's own graph position. Splitting them means a
// steady-state pan/zoom frame redraws only the cheap top layer (one clear +
// one strokeRect), not a full re-plot of every node dot every frame.
import { useCallback, useEffect, useMemo, useRef } from "react";
import type { SceneNode, Size, Viewport } from "./canvasScene";
import { MINIMAP_SIZE, computeMinimapViewport, panFromMinimapPoint, viewportRectOnMinimap } from "./minimap";

export interface MinimapProps {
  sceneNodes: readonly SceneNode[];
  mainViewport: Viewport;
  mainView: Size;
  onPan: (viewport: Viewport) => void;
  dark?: boolean;
}

/** Guards a <canvas>'s width/height assignment: reassigning it — even to an
 * unchanged value — forces the browser to reallocate/clear its backing
 * store, which is measurably expensive when it happens every animation
 * frame (mirrors TopologyCanvasV2's own main-canvas draw effect). */
function ensureCanvasSize(canvas: HTMLCanvasElement, dpr: number): void {
  const wantWidth = Math.round(MINIMAP_SIZE.width * dpr);
  const wantHeight = Math.round(MINIMAP_SIZE.height * dpr);
  if (canvas.width !== wantWidth || canvas.height !== wantHeight) {
    canvas.width = wantWidth;
    canvas.height = wantHeight;
  }
}

function getContext2d(canvas: HTMLCanvasElement | null): CanvasRenderingContext2D | null {
  if (!canvas) return null;
  try {
    return canvas.getContext("2d");
  } catch {
    return null; // headless/jsdom: getContext can throw or return null
  }
}

export function Minimap({ sceneNodes, mainViewport, mainView, onPan, dark = false }: MinimapProps) {
  const dotsCanvasRef = useRef<HTMLCanvasElement>(null);
  const rectCanvasRef = useRef<HTMLCanvasElement>(null);
  const draggingRef = useRef(false);

  const minimapViewport = useMemo(() => computeMinimapViewport(sceneNodes), [sceneNodes]);

  // Static layer: background + one dot per node. Only depends on the entity
  // set and the minimap's own fit-to-all camera (itself only a function of
  // that same entity set) — never on the main canvas's viewport.
  useEffect(() => {
    const ctx = getContext2d(dotsCanvasRef.current);
    if (!ctx || !dotsCanvasRef.current) return;
    const dpr = globalThis.devicePixelRatio > 0 ? globalThis.devicePixelRatio : 1;
    ensureCanvasSize(dotsCanvasRef.current, dpr);
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, MINIMAP_SIZE.width, MINIMAP_SIZE.height);
    ctx.fillStyle = dark ? "#0f172a" : "#f1f5f9";
    ctx.fillRect(0, 0, MINIMAP_SIZE.width, MINIMAP_SIZE.height);
    ctx.fillStyle = dark ? "#475569" : "#94a3b8";
    for (const n of sceneNodes) {
      const x = n.position.x * minimapViewport.zoom + minimapViewport.x;
      const y = n.position.y * minimapViewport.zoom + minimapViewport.y;
      ctx.fillRect(x, y, 2, 2);
    }
  }, [sceneNodes, minimapViewport, dark]);

  // Per-frame layer: just the viewport rectangle — cheap regardless of node
  // count, safe to redraw on every pan/zoom frame.
  useEffect(() => {
    const ctx = getContext2d(rectCanvasRef.current);
    if (!ctx || !rectCanvasRef.current) return;
    const dpr = globalThis.devicePixelRatio > 0 ? globalThis.devicePixelRatio : 1;
    ensureCanvasSize(rectCanvasRef.current, dpr);
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, MINIMAP_SIZE.width, MINIMAP_SIZE.height);
    const rect = viewportRectOnMinimap(mainViewport, mainView, minimapViewport);
    ctx.strokeStyle = dark ? "#3b82f6" : "#2563eb";
    ctx.lineWidth = 1.5;
    ctx.strokeRect(rect.x, rect.y, rect.width, rect.height);
  }, [mainViewport, mainView, minimapViewport, dark]);

  const panFromEvent = useCallback(
    (evt: { clientX: number; clientY: number }) => {
      const rect = rectCanvasRef.current?.getBoundingClientRect();
      const p = { x: evt.clientX - (rect?.left ?? 0), y: evt.clientY - (rect?.top ?? 0) };
      onPan(panFromMinimapPoint(p, minimapViewport, mainViewport, mainView));
    },
    [minimapViewport, mainViewport, mainView, onPan],
  );

  return (
    <div
      data-testid="topology-minimap"
      role="img"
      aria-label="Topology minimap: drag to pan the map"
      className="absolute bottom-2 right-2 touch-none cursor-pointer overflow-hidden rounded border border-slate-300 bg-white/80 shadow dark:border-slate-700 dark:bg-slate-950/80"
      style={{ width: MINIMAP_SIZE.width, height: MINIMAP_SIZE.height }}
      onPointerDown={(evt) => {
        evt.stopPropagation();
        draggingRef.current = true;
        try {
          evt.currentTarget.setPointerCapture(evt.pointerId);
        } catch {
          /* pointer capture may be unavailable (jsdom/tests); non-essential. */
        }
        panFromEvent(evt);
      }}
      onPointerMove={(evt) => {
        evt.stopPropagation();
        if (!draggingRef.current) return;
        panFromEvent(evt);
      }}
      onPointerUp={(evt) => {
        evt.stopPropagation();
        draggingRef.current = false;
        try {
          evt.currentTarget.releasePointerCapture(evt.pointerId);
        } catch {
          /* pointer may already be released */
        }
      }}
      onPointerCancel={(evt) => {
        evt.stopPropagation();
        draggingRef.current = false;
      }}
      onPointerLeave={(evt) => {
        evt.stopPropagation();
      }}
      onWheel={(evt) => {
        evt.stopPropagation();
      }}
      onClick={(evt) => {
        evt.stopPropagation();
      }}
    >
      <canvas ref={dotsCanvasRef} className="absolute inset-0 block" style={{ width: "100%", height: "100%" }} />
      <canvas ref={rectCanvasRef} className="absolute inset-0 block" style={{ width: "100%", height: "100%" }} />
    </div>
  );
}
