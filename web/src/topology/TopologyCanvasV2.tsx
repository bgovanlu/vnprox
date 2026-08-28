// SPDX-License-Identifier: Apache-2.0

// Topology renderer v2 (T-901): a canvas engine for the Graph view, behind
// the `rendererVersion` feature flag, replacing the v1 React Flow DOM/SVG
// node-link canvas. It consumes the *exact same* FlowElements
// (toFlowElements.ts) the v1 renderer does — no new projection or shape — so
// this is a pure rendering-layer swap. The v1 renderer stays selectable as
// the fallback for one release.
//
// Design choices (flagged in planning/reports/T-901.md):
//  - 2D canvas, not WebGL: at the documented scale target (~200 post-collapse
//    elements) a 2D context clears the frame budget with wide margin and adds
//    ZERO new dependency (docs/development.md's "prefer stdlib / justify any
//    new dep"). "canvas/WebGL" in the card is satisfied by the canvas path;
//    a WebGL upgrade can drop in behind the same props/scene seam later if a
//    materially larger scale ever needs it.
//  - The component is thin: all geometry (viewport math, fit, hit-testing,
//    zoom-at-cursor) lives in canvasScene.ts, and the accessibility surface
//    in a11yBridge.ts + TopologyA11yLayer.tsx — both framework-free and
//    unit-tested. This file owns the <canvas>, the pointer gestures, and the
//    draw calls.
//  - Accessibility is first-class, not an afterthought: every visible entity
//    gets a focusable, labeled DOM proxy kept in sync with the canvas
//    (buildA11yProxies + TopologyA11yLayer). Canvas v2 is never a pixel blob
//    with no DOM a11y surface. This is the seam T-905/T-903 build on.
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useThemeStore, type Theme } from "../store/theme";
import { useReducedMotion } from "../lib/useReducedMotion";
import type { FlowElements } from "./toFlowElements";
import type { XYPosition } from "./layout";
import {
  DEFAULT_NODE_SIZE,
  fitViewport,
  hitTest,
  hitTestFlowEdge,
  panBy,
  screenToGraph,
  zoomAt,
  type Viewport,
  type SceneNode,
} from "./canvasScene";
import { buildA11yProxies } from "./a11yBridge";
import { TopologyA11yLayer } from "./TopologyA11yLayer";
import { AnnotationLayer, type AnnotationAnchor } from "./AnnotationLayer";
import type { Annotation, MapRegion } from "../api/types";
import { drawScene, drawFlowOverlay, drawLatencyOverlay, drawMTUOverlay, drawDiffOverlay, drawRecencyOverlay, drawBlastRadiusOverlay, pulseAlphaForPhase, type FlowOverlayEdge, type SceneTheme } from "./canvasDraw";
import type { LatencyOverlayEdge } from "./latencyMode";
import type { DiffMark } from "./diffOverlay";
import type { RecencyMark } from "./recencyOverlay";
import type { BlastRadiusFocus } from "./blastRadiusFocus";
import type { MTUOverlayBadge } from "./mtuOverlay";
import { applyLod, parseLodId, zoomBandFor } from "./lod";
import { Minimap } from "./Minimap";

// T-905: how often the drift "pulse" phase advances while motion is
// allowed — a plain interval (not a 60fps rAF loop): the pulse is a slow,
// low-contrast breathing effect, not something that needs frame-perfect
// smoothness, and an interval this coarse keeps the redraw cost of
// entities that never move or change bounded and cheap even on a
// drift-heavy cluster (T-901's perf budget is about pan/zoom/hover
// interaction frames, which this never blocks — it only ever *adds* an
// occasional extra redraw when nothing else is happening).
const PULSE_TICK_MS = 120;

export interface TopologyCanvasV2Props {
  elements: FlowElements;
  onNodeClick: (id: string) => void;
  onNodeHover: (id: string | undefined) => void;
  /** Drag-drop finished: the dragged entity, the entity it was dropped onto
   * (undefined = empty canvas), and the drop's graph-space top-left. The v2
   * equivalent of v1's onNodeDragStop, but with the drop *target* already
   * resolved by the canvas's own hit-testing — so TopologyPage feeds the same
   * shared computeDragOp path both renderers use (identical draft op, T-901
   * AC3), and a target-less drop falls through to a plain reposition. */
  onNodeDrop: (draggedId: string, targetId: string | undefined, position: XYPosition) => void;
  onPaneClick: () => void;
  onNodeContextMenu?: (id: string, clientX: number, clientY: number) => void;
  /** Currently-selected entity — roving focus follows it. */
  selectedId?: string;
  /** Override the canvas draw theme (defaults to the app theme store — the
   * seam T-905 drives dark/light map colors through). */
  theme?: Theme;
  /** T-907: seeds the canvas's pan/zoom at mount (restoring a saved view or
   * a shareable-URL view) instead of the default fit-to-view-on-load
   * behavior. Read once, at mount, like React's own `defaultValue`
   * convention — this is an *uncontrolled* seed, not a controlled prop
   * (TopologyPage still owns which viewport to seed with, but the canvas
   * owns pan/zoom gestures afterward exactly as before). Omitted (every
   * pre-T-907 call site): unchanged fit-to-view-on-load behavior. */
  initialViewport?: Viewport;
  /** T-907: notified on every pan/zoom change, so a caller can capture
   * "the current viewport" for a saved view or shareable link without the
   * canvas becoming a fully controlled component (see initialViewport's
   * doc comment — the two together are the minimal seam T-907 needs). */
  onViewportChange?: (viewport: Viewport) => void;
  /** T-906: fires whenever the LOD-transformed scene this canvas is actually
   * drawing changes (zoom band crossing a threshold, a capsule/bundle
   * expand/collapse) — lets the page-level "Export map" control export
   * exactly what's on screen under the v2 renderer, not the pre-LOD
   * FlowElements `elements` prop above. Pass a stable callback (e.g. a raw
   * setState function) to avoid re-subscribing every render. undefined (the
   * default) is a no-op. */
  onSceneChange?: (elements: FlowElements) => void;
  /** T-1003 "Flows" layer overlay edges (topology/flowEdges.ts, resolved to
   * a stroke width by the caller) — drawn as a distinct animated overlay
   * after the normal scene. undefined/empty (the default) draws nothing
   * extra, so every pre-T-1003 call site is unaffected. */
  flowEdges?: readonly FlowOverlayEdge[];
  /** The currently-selected flow edge (drill-down panel open for it). */
  selectedFlowEdgeId?: string;
  /** T-1303 "Latency" heatmap layer overlay edges (topology/latencyMode.ts,
   * pre-resolved to a color/stroke width by the caller) — drawn as a
   * distinct, static (non-animated) color-scaled overlay after the normal
   * scene and the Flows overlay. undefined/empty (the default) draws
   * nothing extra, so every pre-T-1303 call site is unaffected. */
  latencyEdges?: readonly LatencyOverlayEdge[];
  /** T-1306 "Verified MTU" badge layer (topology/mtuOverlay.ts) — drawn as a
   * distinct, static text-badge overlay after the Latency overlay.
   * undefined/empty (the default) draws nothing extra, so every pre-T-1306
   * call site is unaffected. */
  mtuBadges?: readonly MTUOverlayBadge[];
  /** T-2704 point-in-time diff overlay marks (topology/diffOverlay.ts) —
   * drawn last of all, ringing every entity that differs from the selected
   * historical point, colored by whether a changeset explains it.
   * undefined/empty (the default) draws nothing extra, so every call site
   * that has not selected a range is unaffected. */
  diffMarks?: readonly DiffMark[];
  /** T-3908 "what changed" recency overlay marks (topology/recencyOverlay.ts)
   * — drawn after the diff overlay as a small filled corner badge (opposite
   * corner from the diff ring's own glyph, so both can be active at once
   * without visually colliding), colored by elapsed time since each
   * entity's last recorded change. undefined/empty (the default) draws
   * nothing extra, so every pre-T-3908 call site is unaffected. */
  recencyMarks?: readonly RecencyMark[];
  /** T-3912 blast-radius lens (topology/blastRadiusFocus.ts) — drawn last of
   * all: a translucent scrim over every node/edge NOT in the focused
   * subgraph, plus a bottom-right role-glyph badge and an accent-colored
   * path stroke over everything that IS (a different corner from the diff
   * ring's and the recency badge's own marks, so all three can be active
   * without colliding — see drawBlastRadiusOverlay's doc comment).
   * `undefined`, or a focus whose `focusNodeIds` is empty (an inactive/
   * degraded request — blastRadiusFocus.ts's `active: false`), draws
   * nothing extra, so every pre-T-3912 call site is unaffected and a stale
   * focus request never blanks the map. */
  blastRadiusFocus?: BlastRadiusFocus;
  /** Fires when a Flows-layer overlay edge is clicked — takes priority
   * over the plain-pane click (onPaneClick) when both could apply, so a
   * click that lands on a flow edge always opens its drill-down rather
   * than deselecting. */
  onFlowEdgeClick?: (edgeId: string) => void;
  /** T-2806 map annotation layer: labelled canvas regions and the pinned
   * notes to render over the graph. Both are the LIVE (non-expired) sets
   * the daemon returned — this canvas never judges expiry itself.
   * undefined/empty (the default) renders nothing extra, so every
   * pre-T-2806 call site is unaffected. */
  regions?: readonly MapRegion[];
  notes?: readonly Annotation[];
}

// Distance (screen px) a pointer must travel after press before a gesture
// counts as a drag rather than a click.
const DRAG_THRESHOLD = 4;

// T-2806: how far above an entity's top-left its note marker sits, in graph
// space, plus the frozen empty defaults so an un-annotated canvas allocates
// nothing and re-renders identically to the pre-T-2806 one.
const ANNOTATION_MARKER_OFFSET = 26;
const EMPTY_REGIONS: readonly MapRegion[] = [];
const EMPTY_NOTES: readonly Annotation[] = [];
const EMPTY_ANCHORS: readonly AnnotationAnchor[] = [];

interface PointerGesture {
  kind: "pan" | "node";
  /** Entity id for a "node" gesture. */
  id?: string;
  startX: number;
  startY: number;
  startViewport: Viewport;
  /** For a node gesture: screen offset from the node's top-left to the press
   * point, so the node keeps its grab point under the cursor while dragging. */
  grabDX?: number;
  grabDY?: number;
  moved: boolean;
}

// T-4204: the canvas v2 renderer's own literal copy of index.css's semantic
// status scale — `-soft`/bare pairing for the "finding:" badge chip, per
// theme (see canvasDraw.ts's findingSeverityFill/findingSeverityText doc
// comment for why a <canvas> needs these as literal hex rather than
// `text-status-*`/`dark:` Tailwind classes). Kept in sync by hand with
// index.css's `--color-status-critical/-degraded/-info` (bare) and
// `-soft` values, same as this file already hand-syncs mgmtBadgeBg/Text.
function themeColors(theme: Theme): SceneTheme {
  return theme === "dark"
    ? {
        background: "#0f172a",
        nodeFill: "#1e293b",
        nodeText: "#e2e8f0",
        kindText: "#64748b",
        nodeBorderOk: "#475569",
        badgeBg: "#334155",
        badgeText: "#cbd5e1",
        mgmtBadgeBg: "#78350f",
        mgmtBadgeText: "#fde68a",
        edgeDefault: "#475569",
        findingErrorFill: "#3a2837",
        findingErrorText: "#ffa098",
        findingWarningFill: "#2f3023",
        findingWarningText: "#dcbc33",
        findingInfoFill: "#16334b",
        findingInfoText: "#57cef7",
      }
    : {
        background: "#f8fafc",
        nodeFill: "#ffffff",
        nodeText: "#1e293b",
        kindText: "#94a3b8",
        nodeBorderOk: "#cbd5e1",
        badgeBg: "#e2e8f0",
        badgeText: "#475569",
        mgmtBadgeBg: "#fde68a",
        mgmtBadgeText: "#92400e",
        edgeDefault: "#94a3b8",
        findingErrorFill: "#f7e7e7",
        findingErrorText: "#b12c2e",
        findingWarningFill: "#f0ede1",
        findingWarningText: "#776300",
        findingInfoFill: "#e0eff2",
        findingInfoText: "#036f8c",
      };
}

export function TopologyCanvasV2({
  elements,
  onNodeClick,
  onNodeHover,
  onNodeDrop,
  onPaneClick,
  onNodeContextMenu,
  selectedId,
  theme,
  initialViewport,
  onViewportChange,
  onSceneChange,
  flowEdges,
  selectedFlowEdgeId,
  latencyEdges,
  mtuBadges,
  diffMarks,
  recencyMarks,
  blastRadiusFocus,
  onFlowEdgeClick,
  regions,
  notes,
}: TopologyCanvasV2Props) {
  const storeTheme = useThemeStore((s) => s.theme);
  const effectiveTheme = theme ?? storeTheme;
  const reducedMotion = useReducedMotion();

  const containerRef = useRef<HTMLDivElement>(null);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const [size, setSize] = useState<{ width: number; height: number }>({ width: 0, height: 0 });
  const [viewport, setViewport] = useState<Viewport>(() => initialViewport ?? { x: 48, y: 48, zoom: 1 });
  // Transient drag position (screen top-left of the dragged node) so the
  // canvas draws it following the cursor without committing to the store.
  const [dragTopLeft, setDragTopLeft] = useState<{ id: string; x: number; y: number } | undefined>(undefined);
  const [rovingId, setRovingId] = useState<string | undefined>(undefined);

  const gesture = useRef<PointerGesture | undefined>(undefined);
  // A caller-supplied initialViewport (T-907: restoring a saved view or a
  // shareable-URL view) means "don't auto-fit on load, I already know what
  // viewport I want" — so the fit-to-view effect below is pre-satisfied
  // rather than firing once more and clobbering the restored viewport.
  const fittedRef = useRef(initialViewport !== undefined);
  const fitSignatureRef = useRef("");

  // T-902 level-of-detail: fit-to-view stays keyed off the *raw* elements
  // (below) so the initial framing never depends on which band happens to be
  // active — only the drawn/interactive scene (lodSceneNodes/lodElements)
  // reflects the current zoom band's collapse/bundle state.

  // T-907: report every viewport change upward (see onViewportChange's doc
  // comment) — an uncontrolled seed/notify seam, not a controlled prop.
  useEffect(() => {
    onViewportChange?.(viewport);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- onViewportChange is expected to be a stable callback (a ref-writer); re-running only on viewport change is intentional.
  }, [viewport]);

  // Scene nodes (id + graph position) for hit-testing — derived from the same
  // FlowElements the canvas draws.
  const sceneNodes = useMemo<SceneNode[]>(
    () => elements.nodes.map((n) => ({ id: n.id, position: n.position })),
    [elements.nodes],
  );

  const band = useMemo(() => zoomBandFor(viewport.zoom), [viewport.zoom]);
  // Bundle/capsule groups the user clicked to force-expand, independent of
  // the current band ("unbundles on zoom-in or click" — click here). Reset
  // whenever the underlying element set changes (see the effect below).
  const [unbundledGroups, setUnbundledGroups] = useState<ReadonlySet<string>>(new Set());
  const [expandedCapsules, setExpandedCapsules] = useState<ReadonlySet<string>>(new Set());
  const elementSignature = useMemo(
    () => `${String(elements.nodes.length)}:${elements.nodes.map((n) => n.id).join(",")}`,
    [elements.nodes],
  );
  const lodSignatureRef = useRef("");
  useEffect(() => {
    if (lodSignatureRef.current === elementSignature) return;
    lodSignatureRef.current = elementSignature;
    setUnbundledGroups(new Set());
    setExpandedCapsules(new Set());
  }, [elementSignature]);

  const lodElements = useMemo(
    () => applyLod(elements, band, { unbundledGroups, expandedCapsules }),
    [elements, band, unbundledGroups, expandedCapsules],
  );

  // The scene the canvas actually draws/hit-tests/exposes via a11y — the
  // LOD-transformed set, distinct from `sceneNodes` (fit-to-view only, above).
  const lodSceneNodes = useMemo<SceneNode[]>(
    () => lodElements.nodes.map((n) => ({ id: n.id, position: n.position })),
    [lodElements.nodes],
  );

  const proxies = useMemo(() => buildA11yProxies(lodElements.nodes), [lodElements.nodes]);

  // T-3912: while a blast-radius lens is active, roving keyboard focus
  // (Tab/arrow-key navigation, TopologyA11yLayer) stays WITHIN the focused
  // subgraph — this task's card's "keep keyboard navigation working within
  // the focused set". A filter over `proxies`, not a change to
  // a11yBridge.ts's rovingOrder/nextRovingId themselves (which stay generic
  // over whatever proxy array they're given). Falls back to the full
  // `proxies` list if the filter would leave nothing focusable at all (a
  // defensive floor — a request whose refs are all off-map is already
  // `active: false` upstream and produces an empty `focusNodeIds`, but this
  // never leaves a keyboard user with zero reachable entities regardless).
  const a11yProxies = useMemo(() => {
    if (!blastRadiusFocus || blastRadiusFocus.focusNodeIds.size === 0) return proxies;
    const focused = proxies.filter((p) => blastRadiusFocus.focusNodeIds.has(p.id));
    return focused.length > 0 ? focused : proxies;
  }, [proxies, blastRadiusFocus]);

  // T-2806: where each annotated entity currently sits, in graph space, so
  // the annotation overlay can pin a note marker to it. Derived from the
  // SAME LOD-transformed node set the canvas draws, so a note follows its
  // entity through zoom bands and capsule expansion rather than floating
  // at a stale position. A note whose ref is not in this set is not
  // dropped — AnnotationLayer lists it as an orphan (T-2806 AC2).
  const annotationAnchors = useMemo(() => {
    if (notes === undefined || notes.length === 0) return EMPTY_ANCHORS;
    const wanted = new Set(notes.map((n) => n.ref));
    return lodElements.nodes
      .filter((n) => wanted.has(n.id))
      .map((n) => ({ ref: n.id, x: n.position.x, y: n.position.y - ANNOTATION_MARKER_OFFSET }));
  }, [notes, lodElements.nodes]);

  // T-906: report the LOD-transformed scene up to the page level on every
  // change, so "Export map" (which lives outside this component, in the
  // shared toolbar) can export what this canvas is actually drawing.
  useEffect(() => {
    onSceneChange?.(lodElements);
  }, [lodElements, onSceneChange]);

  // A click on a LOD-synthetic capsule/bundle toggles its manual-expand
  // override instead of forwarding to the parent's onNodeClick (which
  // expects a real inventory ref or guest-group id — capsule/bundle ids are
  // neither, and have no inspector/expand-query counterpart).
  const handleEntityActivate = useCallback(
    (id: string) => {
      const parsed = parseLodId(id);
      if (!parsed) {
        onNodeClick(id);
        return;
      }
      if (parsed.kind === "capsule") {
        setExpandedCapsules((prev) => {
          const next = new Set(prev);
          if (next.has(parsed.key)) next.delete(parsed.key);
          else next.add(parsed.key);
          return next;
        });
      } else {
        setUnbundledGroups((prev) => {
          const next = new Set(prev);
          if (next.has(parsed.key)) next.delete(parsed.key);
          else next.add(parsed.key);
          return next;
        });
      }
    },
    [onNodeClick],
  );

  // --- Container sizing (ResizeObserver, guarded for jsdom) ----------------
  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const measure = () => {
      setSize({ width: el.clientWidth, height: el.clientHeight });
    };
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => {
      ro.disconnect();
    };
  }, []);

  // --- Fit-to-view once per distinct element set (mirrors v1's `fitView`) --
  // (elementSignature is computed above, alongside the LOD manual-override
  // reset, which shares the exact same "did the underlying data change"
  // signature.)
  useEffect(() => {
    if (size.width <= 0 || size.height <= 0 || elements.nodes.length === 0) return;
    if (fittedRef.current && fitSignatureRef.current === elementSignature) return;
    fittedRef.current = true;
    fitSignatureRef.current = elementSignature;
    setViewport(fitViewport(sceneNodes, size));
  }, [size, elements.nodes.length, elementSignature, sceneNodes]);

  // --- Selection drives roving focus (kept in sync per T-901's a11y spec) --
  useEffect(() => {
    if (selectedId !== undefined) setRovingId(selectedId);
  }, [selectedId]);

  // --- T-905: drift "pulse" phase — only ticks when motion is allowed AND
  // at least one visible entity would actually animate, so a cluster with
  // no drift findings (the common case) never starts an interval at all.
  const hasDriftEntity = useMemo(
    () =>
      elements.nodes.some((n) => n.data.badges.includes("drift")) ||
      elements.edges.some((e) => e.data?.badges.includes("drift") ?? false),
    [elements.nodes, elements.edges],
  );
  const [pulsePhase, setPulsePhase] = useState(0);
  useEffect(() => {
    if (reducedMotion || !hasDriftEntity) {
      setPulsePhase(0);
      return;
    }
    const start = Date.now();
    const id = setInterval(() => {
      setPulsePhase(Date.now() - start);
    }, PULSE_TICK_MS);
    return () => {
      clearInterval(id);
    };
  }, [reducedMotion, hasDriftEntity]);

  // T-1003: the Flows-layer overlay's "animated dash-flow direction by
  // src->dst" — a marching lineDashOffset, ticking only while there's at
  // least one overlay edge to animate and motion is allowed (mirrors the
  // drift pulse above: reduced-motion collapses to a static dashed line,
  // offset 0, rather than skipping the dash pattern entirely).
  const hasFlowEdges = (flowEdges?.length ?? 0) > 0;
  const hasLatencyEdges = (latencyEdges?.length ?? 0) > 0;
  const hasMTUBadges = (mtuBadges?.length ?? 0) > 0;
  const hasDiffMarks = (diffMarks?.length ?? 0) > 0;
  const hasRecencyMarks = (recencyMarks?.length ?? 0) > 0;
  const hasBlastRadiusFocus = (blastRadiusFocus?.focusNodeIds.size ?? 0) > 0;
  const [flowDashOffset, setFlowDashOffset] = useState(0);
  useEffect(() => {
    if (reducedMotion || !hasFlowEdges) {
      setFlowDashOffset(0);
      return;
    }
    const id = setInterval(() => {
      setFlowDashOffset((prev) => (prev + 1.5) % 14); // 14 = the [8,6] dash pattern's period
    }, PULSE_TICK_MS / 4);
    return () => {
      clearInterval(id);
    };
  }, [reducedMotion, hasFlowEdges]);

  // --- Draw ----------------------------------------------------------------
  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas || size.width <= 0 || size.height <= 0) return;
    let ctx: CanvasRenderingContext2D | null;
    try {
      ctx = canvas.getContext("2d");
    } catch {
      ctx = null; // headless/jsdom: getContext can throw
    }
    if (!ctx) return; // no 2d context — a11y DOM still renders.

    const dpr = globalThis.devicePixelRatio > 0 ? globalThis.devicePixelRatio : 1;
    if (canvas.width !== Math.round(size.width * dpr) || canvas.height !== Math.round(size.height * dpr)) {
      canvas.width = Math.round(size.width * dpr);
      canvas.height = Math.round(size.height * dpr);
    }
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);

    drawScene(ctx, {
      nodes: lodElements.nodes,
      edges: lodElements.edges,
      viewport,
      view: size,
      theme: themeColors(effectiveTheme),
      dragTopLeft,
      nodeSize: DEFAULT_NODE_SIZE,
      // Static (1, the plain look) unless motion is allowed AND something
      // would actually animate — never runs the breathing formula only to
      // immediately pin it back to a look-alike of 1 (pulseAlphaForPhase(0)
      // is 0.55, not 1: reduced-motion must skip the formula entirely, not
      // just freeze its phase at 0).
      pulseAlpha: reducedMotion || !hasDriftEntity ? 1 : pulseAlphaForPhase(pulsePhase),
    });

    // T-1003: the Flows layer overlay, drawn as its own pass on top of the
    // normal scene — see drawFlowOverlay's doc comment for why this is
    // never merged into drawScene's own edge loop.
    if (hasFlowEdges && flowEdges) {
      drawFlowOverlay(ctx, {
        nodes: lodElements.nodes,
        edges: flowEdges,
        viewport,
        nodeSize: DEFAULT_NODE_SIZE,
        dragTopLeft,
        dashOffset: flowDashOffset,
        selectedId: selectedFlowEdgeId,
      });
    }

    // T-1303: the Latency heatmap layer overlay, drawn last so an active
    // Flows overlay's animated edges stay visually on top of the static
    // latency color-scale (see drawLatencyOverlay's doc comment).
    if (hasLatencyEdges && latencyEdges) {
      drawLatencyOverlay(ctx, {
        nodes: lodElements.nodes,
        edges: latencyEdges,
        viewport,
        nodeSize: DEFAULT_NODE_SIZE,
        dragTopLeft,
      });
    }

    // T-1306: the Verified MTU badge layer, drawn last of all so its
    // per-link text badges stay legible on top of every other overlay.
    if (hasMTUBadges && mtuBadges) {
      drawMTUOverlay(ctx, {
        nodes: lodElements.nodes,
        badges: mtuBadges,
        viewport,
        nodeSize: DEFAULT_NODE_SIZE,
        theme: themeColors(effectiveTheme),
        dragTopLeft,
      });
    }

    // T-2704: the point-in-time diff overlay, drawn before the recency
    // overlay — its rings surround the node box, so nothing except the
    // recency badge (a different corner, see drawRecencyOverlay's doc
    // comment) may paint over them.
    if (hasDiffMarks && diffMarks) {
      drawDiffOverlay(ctx, {
        nodes: lodElements.nodes,
        marks: diffMarks,
        viewport,
        nodeSize: DEFAULT_NODE_SIZE,
        dragTopLeft,
      });
    }

    // T-3908: the "what changed" recency overlay, drawn last of all — a
    // small corner badge, deliberately in the opposite corner from the diff
    // ring's own glyph so the two can both be active without colliding.
    if (hasRecencyMarks && recencyMarks) {
      drawRecencyOverlay(ctx, {
        nodes: lodElements.nodes,
        marks: recencyMarks,
        viewport,
        nodeSize: DEFAULT_NODE_SIZE,
        dragTopLeft,
      });
    }

    // T-3912: the blast-radius lens, drawn absolutely last — it scrims
    // everything not in the focused subgraph, which must happen after every
    // other overlay's own marks have already painted (a scrimmed node still
    // shows a MUTED diff ring/recency badge underneath, never a fully
    // stripped one) and its own bottom-right badge/path-stroke need to sit
    // on top of everything else too.
    if (hasBlastRadiusFocus && blastRadiusFocus) {
      drawBlastRadiusOverlay(ctx, {
        nodes: lodElements.nodes,
        edges: lodElements.edges,
        focusNodeIds: blastRadiusFocus.focusNodeIds,
        focusEdgeIds: blastRadiusFocus.focusEdgeIds,
        roles: blastRadiusFocus.roles,
        viewport,
        view: size,
        theme: themeColors(effectiveTheme),
        nodeSize: DEFAULT_NODE_SIZE,
        dragTopLeft,
      });
    }
  }, [
    lodElements.nodes,
    lodElements.edges,
    viewport,
    size,
    effectiveTheme,
    dragTopLeft,
    pulsePhase,
    reducedMotion,
    hasDriftEntity,
    hasFlowEdges,
    flowEdges,
    flowDashOffset,
    selectedFlowEdgeId,
    hasLatencyEdges,
    latencyEdges,
    hasMTUBadges,
    mtuBadges,
    hasDiffMarks,
    diffMarks,
    hasRecencyMarks,
    recencyMarks,
    hasBlastRadiusFocus,
    blastRadiusFocus,
  ]);

  // --- Pointer helpers -----------------------------------------------------
  const localPoint = useCallback((evt: { clientX: number; clientY: number }): XYPosition => {
    const rect = containerRef.current?.getBoundingClientRect();
    return { x: evt.clientX - (rect?.left ?? 0), y: evt.clientY - (rect?.top ?? 0) };
  }, []);

  // T-3204/T-2505-followup-01: a native pointermove fires far faster than the
  // display can paint (Chromium can dispatch dozens per animation frame, and
  // Playwright's own synthetic gesture drives one CDP round trip per step).
  // Before this, every single pointermove committed its own React state
  // update (setViewport/setDragTopLeft/onNodeHover) synchronously, which
  // scheduled its own render + effect + full drawScene() pass — so a fast
  // gesture could queue many redundant redraw passes for points that were
  // already stale by the time they ran, competing with the browser's own
  // input-dispatch/compositor bookkeeping for the same main thread. Coalescing
  // to "at most one state commit per animation frame, using the latest
  // pointer position" is the standard fix for this class of pointer-handler
  // overload (mirrors how a scroll/resize handler is rAF-throttled) and cuts
  // real redraw work during a pan/zoom by up to an order of magnitude at this
  // fixture's scale — not just a workaround for the e2e hang this was found
  // chasing (see quarantine.json's T-2505-followup-01 entry).
  const pendingMoveRef = useRef<
    | { kind: "hover"; hitId: string | undefined }
    | { kind: "pan"; viewport: Viewport }
    | { kind: "node"; id: string; x: number; y: number }
    | undefined
  >(undefined);
  const moveRafRef = useRef<number | undefined>(undefined);

  const flushPointerMove = useCallback(() => {
    moveRafRef.current = undefined;
    const pending = pendingMoveRef.current;
    pendingMoveRef.current = undefined;
    if (!pending) return;
    switch (pending.kind) {
      case "hover":
        onNodeHover(pending.hitId);
        break;
      case "pan":
        setViewport(pending.viewport);
        break;
      case "node":
        setDragTopLeft({ id: pending.id, x: pending.x, y: pending.y });
        break;
    }
  }, [onNodeHover]);

  const schedulePointerMove = useCallback(() => {
    if (moveRafRef.current !== undefined) return;
    moveRafRef.current = requestAnimationFrame(flushPointerMove);
  }, [flushPointerMove]);

  // Drop any coalesced-but-not-yet-applied update on unmount, so a
  // late-firing rAF never calls setState on an unmounted component.
  useEffect(() => {
    return () => {
      if (moveRafRef.current !== undefined) cancelAnimationFrame(moveRafRef.current);
    };
  }, []);

  const handlePointerDown = useCallback(
    (evt: React.PointerEvent<HTMLDivElement>) => {
      if (evt.button !== 0) return; // left button only; context menu handled separately
      const p = localPoint(evt);
      const hitId = hitTest(lodSceneNodes, p, viewport);
      try {
        containerRef.current?.setPointerCapture(evt.pointerId);
      } catch {
        /* pointer capture may be unavailable (jsdom/tests); non-essential. */
      }
      if (hitId !== undefined) {
        const node = lodElements.nodes.find((n) => n.id === hitId);
        const screenTL = node ? { x: node.position.x * viewport.zoom + viewport.x, y: node.position.y * viewport.zoom + viewport.y } : p;
        gesture.current = {
          kind: "node",
          id: hitId,
          startX: p.x,
          startY: p.y,
          startViewport: viewport,
          grabDX: p.x - screenTL.x,
          grabDY: p.y - screenTL.y,
          moved: false,
        };
      } else {
        gesture.current = { kind: "pan", startX: p.x, startY: p.y, startViewport: viewport, moved: false };
      }
    },
    [localPoint, lodSceneNodes, viewport, lodElements.nodes],
  );

  const handlePointerMove = useCallback(
    (evt: React.PointerEvent<HTMLDivElement>) => {
      const g = gesture.current;
      const p = localPoint(evt);
      if (!g) {
        // Plain hover: hit-test now (cheap), but coalesce the resulting
        // state commit to the next animation frame (see schedulePointerMove's
        // doc comment above).
        pendingMoveRef.current = { kind: "hover", hitId: hitTest(lodSceneNodes, p, viewport) };
        schedulePointerMove();
        return;
      }
      const dx = p.x - g.startX;
      const dy = p.y - g.startY;
      if (!g.moved && Math.hypot(dx, dy) < DRAG_THRESHOLD) return;
      // The drag-threshold flag is set synchronously (endGesture's click-vs-
      // drag branch needs it immediately at pointerup, which may land before
      // a coalesced update ever flushes) — only the resulting viewport/
      // drag-position COMMIT is throttled.
      g.moved = true;
      if (g.kind === "pan") {
        pendingMoveRef.current = { kind: "pan", viewport: panBy(g.startViewport, dx, dy) };
      } else if (g.id !== undefined) {
        pendingMoveRef.current = { kind: "node", id: g.id, x: p.x - (g.grabDX ?? 0), y: p.y - (g.grabDY ?? 0) };
      }
      schedulePointerMove();
    },
    [localPoint, lodSceneNodes, viewport, schedulePointerMove],
  );

  const endGesture = useCallback(
    (evt: React.PointerEvent<HTMLDivElement>) => {
      const g = gesture.current;
      gesture.current = undefined;
      // Drop any move this gesture queued but that hasn't reached a frame
      // yet — otherwise a coalesced update (e.g. a stale drag position) could
      // flush a moment after this function's own setDragTopLeft(undefined)
      // below and resurrect the just-dropped node's ghost position.
      if (moveRafRef.current !== undefined) {
        cancelAnimationFrame(moveRafRef.current);
        moveRafRef.current = undefined;
      }
      pendingMoveRef.current = undefined;
      try {
        containerRef.current?.releasePointerCapture(evt.pointerId);
      } catch {
        /* pointer may already be released */
      }
      if (!g) return;
      const p = localPoint(evt);
      if (g.kind === "node" && g.id !== undefined) {
        if (!g.moved) {
          handleEntityActivate(g.id);
        } else if (parseLodId(g.id) === undefined) {
          // Resolve the drop target by hit-testing under the pointer (not the
          // dragged node itself), and report the drop's graph-space top-left.
          // LOD-synthetic capsule/bundle nodes (parseLodId defined) have no
          // real drop semantics — they snap back instead (below).
          const targetId = hitTest(lodSceneNodes, p, viewport, DEFAULT_NODE_SIZE, g.id);
          const topLeftScreen = { x: p.x - (g.grabDX ?? 0), y: p.y - (g.grabDY ?? 0) };
          const graphPos = screenToGraph(topLeftScreen, viewport);
          onNodeDrop(g.id, targetId, graphPos);
        }
      } else if (g.kind === "pan" && !g.moved) {
        // T-1003: a click that lands on nothing (no node hit — that's how
        // this branch is reached in the first place) may still land on a
        // Flows-layer overlay edge, which is invisible to hitTest (it only
        // knows about scene *nodes*). Check that before falling through to
        // a plain deselecting pane click.
        const hitEdge =
          hasFlowEdges && flowEdges && onFlowEdgeClick
            ? hitTestFlowEdge(flowEdges, lodSceneNodes, p, viewport)
            : undefined;
        if (hitEdge !== undefined) {
          onFlowEdgeClick?.(hitEdge);
        } else {
          onPaneClick();
        }
      }
      setDragTopLeft(undefined);
    },
    [localPoint, lodSceneNodes, viewport, handleEntityActivate, onNodeDrop, onPaneClick, hasFlowEdges, flowEdges, onFlowEdgeClick],
  );

  const handleWheel = useCallback(
    (evt: React.WheelEvent<HTMLDivElement>) => {
      const p = localPoint(evt);
      const factor = evt.deltaY < 0 ? 1.1 : 1 / 1.1;
      setViewport((vp) => zoomAt(vp, p, factor));
    },
    [localPoint],
  );

  const handleContextMenu = useCallback(
    (evt: React.MouseEvent<HTMLDivElement>) => {
      if (!onNodeContextMenu) return;
      const p = localPoint(evt);
      const hitId = hitTest(lodSceneNodes, p, viewport);
      // LOD-synthetic capsule/bundle entities have no real inventory ref, so
      // no trace/edit context-menu items apply to them.
      if (hitId === undefined || parseLodId(hitId) !== undefined) return;
      evt.preventDefault();
      onNodeContextMenu(hitId, evt.clientX, evt.clientY);
    },
    [onNodeContextMenu, localPoint, lodSceneNodes, viewport],
  );

  return (
    <div
      ref={containerRef}
      data-testid="topology-canvas-v2"
      className="relative h-full w-full touch-none overflow-hidden"
      onPointerDown={handlePointerDown}
      onPointerMove={handlePointerMove}
      onPointerUp={endGesture}
      onPointerCancel={endGesture}
      onWheel={handleWheel}
      onContextMenu={handleContextMenu}
      onPointerLeave={() => {
        if (!gesture.current) onNodeHover(undefined);
      }}
    >
      <canvas ref={canvasRef} className="block h-full w-full" style={{ width: "100%", height: "100%" }} />
      <AnnotationLayer regions={regions ?? EMPTY_REGIONS} notes={notes ?? EMPTY_NOTES} anchors={annotationAnchors} viewport={viewport} />
      <TopologyA11yLayer
        proxies={a11yProxies}
        viewport={viewport}
        activeId={rovingId}
        onActiveChange={setRovingId}
        onActivate={handleEntityActivate}
      />
      {size.width > 0 && size.height > 0 && lodSceneNodes.length > 0 && (
        <Minimap
          sceneNodes={lodSceneNodes}
          mainViewport={viewport}
          mainView={size}
          onPan={setViewport}
          dark={effectiveTheme === "dark"}
        />
      )}
    </div>
  );
}
