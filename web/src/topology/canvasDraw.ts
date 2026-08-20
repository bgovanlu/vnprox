// The v2 canvas draw pass (TopologyCanvasV2.tsx). Kept in its own module so
// the component file stays about interaction/state and this stays about
// pixels. It is a pure function of (FlowElements, viewport, theme): it reads
// the same EntityNodeData/EntityEdgeData the v1 DOM renderer reads, and
// paints the same visual vocabulary — status colors, kind accents, VLAN-dim,
// hover-chain highlight, selection, the amber mgmt/corosync/mgmt-path badge
// trio, the drift dashed outline, and the path-simulator overlay — so v2 is a
// faithful rendering swap, not a different picture (T-901 parity).
//
// Not unit-tested directly (it needs a real CanvasRenderingContext2D, which
// jsdom lacks); its correctness is covered by the golden-parity render tests
// asserting the *data* both renderers consume is identical, plus the e2e
// perf/scale run that exercises the real draw path in headless Chromium.
import type { Edge as FlowEdge, Node as FlowNode } from "@xyflow/react";
import type { EntityStatus, Severity } from "../api/types";
import type { EntityEdgeData } from "./EntityEdge";
import type { EntityNodeData } from "./EntityNode";
import type { Size, Viewport } from "./canvasScene";
import { DEFAULT_NODE_SIZE, graphToScreen } from "./canvasScene";
import type { LatencyOverlayEdge } from "./latencyMode";
import { diffMarkColor, diffMarkGlyph, type DiffMark } from "./diffOverlay";
import { findingChipText, hasOpenFinding, parseFindingBadge, shouldPulse } from "./findingBadges";
import { formatMTUBadgeLabel, type MTUOverlayBadge } from "./mtuOverlay";
import { jackKindForEntity, speedMarking, type PortBodyKind } from "./portMedia";
import { trafficEdgeStyle } from "./trafficMode";

// T-3501: severity fill/text colours for a "finding:<source>:<severity>"
// badge chip on the canvas — the same red/amber/slate hue steps
// findingBadges.ts's findingBadgeClass uses in the DOM renderers
// (red-200/red-800, amber-200/amber-800, slate-200/slate-600), as literal
// hex since a <canvas> has no Tailwind classes to defer to (matching this
// file's existing STATUS_STROKE/SIM_STROKE convention).
const FINDING_SEVERITY_FILL: Record<Severity, string> = {
  error: "#fecaca",
  warning: "#fde68a",
  info: "#e2e8f0",
};
const FINDING_SEVERITY_TEXT: Record<Severity, string> = {
  error: "#991b1b",
  warning: "#92400e",
  info: "#475569",
};

export interface SceneTheme {
  background: string;
  nodeFill: string;
  nodeText: string;
  kindText: string;
  nodeBorderOk: string;
  badgeBg: string;
  badgeText: string;
  mgmtBadgeBg: string;
  mgmtBadgeText: string;
  edgeDefault: string;
}

export interface DrawSceneParams {
  nodes: FlowNode<EntityNodeData, "entity">[];
  edges: FlowEdge<EntityEdgeData, "entity">[];
  viewport: Viewport;
  view: Size;
  theme: SceneTheme;
  /** Screen-space top-left of the node currently being dragged, if any. */
  dragTopLeft?: { id: string; x: number; y: number };
  nodeSize: Size;
  /** T-905's drift "pulse" alpha multiplier (1 = the plain static look every
   * other node/edge always gets), applied only to drift-badged nodes/edges
   * — the canvas's own equivalent of EntityNode.tsx's `animate-pulse` CSS
   * class, since a `<canvas>` has no CSS animations to defer to. Callers
   * (TopologyCanvasV2.tsx) compute this via `pulseAlphaForPhase` and drive
   * it from a low-frequency interval that never runs at all when
   * `prefers-reduced-motion: reduce` is set — the default `1` here is
   * exactly that reduced-motion fallback (a plain static drift dash),
   * so this parameter is optional and existing callers/tests are
   * unaffected. */
  pulseAlpha?: number;
}

// T-905: the drift/mgmt "pulse" — a slow, low-contrast breathing alpha
// (0.55..1.0, never fully transparent so the entity stays legible) driven by
// a wall-clock phase in milliseconds. Pure and deterministic (same phase in
// => same alpha out) so it's directly Vitest-able without a canvas context,
// unlike the rest of this module.
const PULSE_PERIOD_MS = 1400;

export function pulseAlphaForPhase(phaseMs: number, periodMs: number = PULSE_PERIOD_MS): number {
  const t = ((phaseMs % periodMs) + periodMs) % periodMs / periodMs;
  return 0.55 + 0.45 * Math.abs(Math.sin(t * Math.PI));
}

const STATUS_STROKE: Record<EntityStatus, string> = {
  ok: "#94a3b8",
  down: "#ef4444",
  degraded: "#f59e0b",
  unknown: "#94a3b8",
};

const SIM_STROKE: Record<string, string> = {
  allow: "#10b981",
  deny: "#ef4444",
  unreachable: "#f59e0b",
  indeterminate: "#8b5cf6",
};

const KIND_ACCENT: Record<string, string> = {
  physnic: "#64748b",
  bond: "#0ea5e9",
  "ovs-bond": "#0ea5e9",
  bridge: "#6366f1",
  "ovs-bridge": "#6366f1",
  vlan: "#8b5cf6",
  "sdn-zone": "#14b8a6",
  "sdn-vnet": "#14b8a6",
  "sdn-subnet": "#14b8a6",
  guest: "#10b981",
  "guest-nic": "#10b981",
  "guest-group": "#10b981",
  "phys-group": "#64748b",
  "lldp-neighbor": "#64748b",
};

const MGMT_BADGES = new Set(["mgmt", "corosync", "mgmt-path"]);

function statusBorder(status: EntityStatus, theme: SceneTheme): string {
  switch (status) {
    case "down":
      return "#ef4444";
    case "degraded":
      return "#f59e0b";
    case "unknown":
      return "#94a3b8";
    default:
      return theme.nodeBorderOk;
  }
}

function roundRectPath(ctx: CanvasRenderingContext2D, x: number, y: number, w: number, h: number, r: number): void {
  const rr = Math.min(r, w / 2, h / 2);
  ctx.beginPath();
  ctx.moveTo(x + rr, y);
  ctx.arcTo(x + w, y, x + w, y + h, rr);
  ctx.arcTo(x + w, y + h, x, y + h, rr);
  ctx.arcTo(x, y + h, x, y, rr);
  ctx.arcTo(x, y, x + w, y, rr);
  ctx.closePath();
}

/**
 * T-3505: the canvas equivalent of PortBody.tsx's `<PortJack>` — a handful
 * of `ctx` primitives standing in for that component's SVG `<path>`s, since
 * this runs in the per-frame node loop rather than mounting once. Shape,
 * not just color, is what has to survive here: WCAG 1.4.1 (T-905's "no
 * status conveyed by colour alone") applies to a canvas glyph exactly as it
 * does to a DOM one, and the whole point of this task is that a physnic/
 * guest-nic node stop rendering as the same undifferentiated rounded rect
 * every other kind gets.
 *
 * `detailed` picks between two fidelities of the SAME silhouette (never a
 * different shape) — the RJ45 notch, SFP cage, unknown dashed gap, and
 * virtual dashed-RJ45 all keep their identity at both sizes; `detailed`
 * only adds the finer marks (contacts / cage slot / gap bar) that a coarse,
 * zoomed-out box has no legible room for. Gated by the caller on
 * `showText` — canvasDraw.ts's own existing LOD signal — never a second
 * zoom scheme of this function's own.
 */
function drawJack(
  ctx: CanvasRenderingContext2D,
  x: number,
  y: number,
  w: number,
  h: number,
  kind: PortBodyKind,
  color: string,
  detailed: boolean,
): void {
  ctx.save();
  ctx.strokeStyle = color;
  ctx.lineWidth = detailed ? 1 : 1.25;
  if (kind === "sfp") {
    // The cage: a plain rectangle — distinct in silhouette from the RJ45's
    // notched bottom edge even with the inner slot omitted at coarse
    // fidelity.
    roundRectPath(ctx, x, y, w, h, Math.min(1.5, h / 6));
    ctx.stroke();
    if (detailed) ctx.strokeRect(x + w * 0.18, y + h * 0.25, w * 0.64, h * 0.5);
  } else if (kind === "unknown") {
    // "No reading": a dashed outline — never a confident solid shape for a
    // media type we do not have (portMedia.ts's own rule, applied here in
    // pixels instead of an SVG dasharray).
    ctx.setLineDash([2, 1.5]);
    roundRectPath(ctx, x, y, w, h, Math.min(1.5, h / 6));
    ctx.stroke();
    ctx.setLineDash([]);
    if (detailed) {
      ctx.beginPath();
      ctx.moveTo(x + w * 0.32, y + h * 0.5);
      ctx.lineTo(x + w * 0.68, y + h * 0.5);
      ctx.stroke();
    }
  } else {
    // rj45 / virtual: the keyed body with its latch-tab notch cut from the
    // bottom edge (PortBody.tsx's Rj45Body path, in primitives). `virtual`
    // dashes the outline — same convention as the SVG version: a guest
    // NIC's access port keeps the jack shape (this is still a switch) but
    // the dash says there is no physical socket behind it.
    if (kind === "virtual") ctx.setLineDash([2, 1.5]);
    const notch = h * 0.32;
    ctx.beginPath();
    ctx.moveTo(x, y);
    ctx.lineTo(x + w, y);
    ctx.lineTo(x + w, y + h - notch);
    ctx.lineTo(x + w * 0.7, y + h - notch);
    ctx.lineTo(x + w * 0.7, y + h);
    ctx.lineTo(x + w * 0.3, y + h);
    ctx.lineTo(x + w * 0.3, y + h - notch);
    ctx.lineTo(x, y + h - notch);
    ctx.closePath();
    ctx.stroke();
    ctx.setLineDash([]);
    if (detailed) {
      ctx.beginPath();
      for (let i = 0; i < 4; i++) {
        const cx = x + w * (0.16 + i * 0.22);
        ctx.moveTo(cx, y + h * 0.18);
        ctx.lineTo(cx, y + h * 0.42);
      }
      ctx.lineWidth = 0.75;
      ctx.stroke();
    }
  }
  ctx.restore();
}

function nodeCenterScreen(
  node: FlowNode<EntityNodeData, "entity">,
  vp: Viewport,
  size: Size,
  dragTopLeft: DrawSceneParams["dragTopLeft"],
): { x: number; y: number } {
  if (dragTopLeft?.id === node.id) {
    return { x: dragTopLeft.x + (size.width * vp.zoom) / 2, y: dragTopLeft.y + (size.height * vp.zoom) / 2 };
  }
  const tl = graphToScreen(node.position, vp);
  return { x: tl.x + (size.width * vp.zoom) / 2, y: tl.y + (size.height * vp.zoom) / 2 };
}

export function drawScene(ctx: CanvasRenderingContext2D, params: DrawSceneParams): void {
  const { nodes, edges, viewport: vp, view, theme, dragTopLeft, nodeSize, pulseAlpha = 1 } = params;
  const size = nodeSize.width > 0 ? nodeSize : DEFAULT_NODE_SIZE;

  ctx.clearRect(0, 0, view.width, view.height);
  ctx.fillStyle = theme.background;
  ctx.fillRect(0, 0, view.width, view.height);

  const byId = new Map<string, FlowNode<EntityNodeData, "entity">>();
  for (const n of nodes) byId.set(n.id, n);

  // --- Edges ---------------------------------------------------------------
  ctx.lineCap = "round";
  for (const e of edges) {
    const from = byId.get(e.source);
    const to = byId.get(e.target);
    if (!from || !to) continue;
    const a = nodeCenterScreen(from, vp, size, dragTopLeft);
    const b = nodeCenterScreen(to, vp, size, dragTopLeft);
    const data = e.data;
    const dimmed = (data?.dimmed ?? false) && !(data?.highlighted ?? false);
    const sim = data?.simVerdict;
    const trafficMode = data?.trafficMode ?? false;
    let stroke: string;
    let width = data?.highlighted ? 2.5 : 1.5;
    let dashed = false;
    if (sim) {
      stroke = SIM_STROKE[sim] ?? theme.edgeDefault;
      width = 3.5;
    } else if (trafficMode) {
      const t = trafficEdgeStyle(data?.utilizationPct);
      stroke = t.stroke;
      width = t.strokeWidth;
    } else {
      const status = data?.status ?? "ok";
      stroke = STATUS_STROKE[status];
      dashed = status === "unknown" || hasOpenFinding(data?.badges ?? []);
    }
    // T-3501: dashing stays source-agnostic (hasOpenFinding, above — any
    // open finding earns the dash, matching the pre-T-3501 bare "drift"
    // check exactly), but the pulse alpha only applies when the finding's
    // severity warrants it (shouldPulse) — see findingBadges.ts's doc
    // comment for why a legacy-badge-only edge (no richer form yet) still
    // pulses rather than losing motion outright.
    const pulseWorthyEdge = data ? hasOpenFinding(data.badges) && shouldPulse(data.badges) : false;
    ctx.save();
    // T-905: the drift pulse (reduced-motion falls back to pulseAlpha's
    // default of 1, i.e. the plain static dashed look) — never applied to
    // an already-dimmed edge, so the two alpha treatments don't compound.
    ctx.globalAlpha = dimmed ? 0.15 : pulseWorthyEdge ? pulseAlpha : 1;
    ctx.strokeStyle = stroke;
    ctx.lineWidth = width;
    ctx.setLineDash(dashed ? [4, 3] : []);
    ctx.beginPath();
    ctx.moveTo(a.x, a.y);
    ctx.lineTo(b.x, b.y);
    ctx.stroke();
    ctx.restore();
  }

  // --- Nodes ---------------------------------------------------------------
  const w = size.width * vp.zoom;
  const h = size.height * vp.zoom;
  const showText = w > 66;
  const showBadges = h > 34;
  ctx.textBaseline = "middle";
  for (const n of nodes) {
    const tl =
      dragTopLeft?.id === n.id ? { x: dragTopLeft.x, y: dragTopLeft.y } : graphToScreen(n.position, vp);
    // Cull fully off-screen boxes (cheap; keeps large clusters fast).
    if (tl.x + w < 0 || tl.y + h < 0 || tl.x > view.width || tl.y > view.height) continue;

    const d = n.data;
    const dimmed = d.dimmed && !d.highlighted;
    // T-3501: see the edge loop's identical comment above — dashing stays
    // source-agnostic, the pulse alpha is severity-gated.
    const nodeHasFinding = hasOpenFinding(d.badges);
    const pulseWorthyNode = nodeHasFinding && shouldPulse(d.badges);
    ctx.save();
    // T-905: same drift-pulse treatment as the edge loop above — dim/stale
    // both win over it (never compounded), and reduced motion collapses
    // pulseAlpha to its default 1 (a plain static look).
    ctx.globalAlpha = dimmed ? 0.25 : d.stale ? 0.6 : pulseWorthyNode ? pulseAlpha : 1;

    const isPill = d.isGuestGroup || d.isPhysGroup;
    const radius = isPill ? h / 2 : 6;
    const jackKind = jackKindForEntity(d.kind, d.mediaPort);
    // Fill
    roundRectPath(ctx, tl.x, tl.y, w, h, radius);
    ctx.fillStyle = d.stale ? theme.badgeBg : theme.nodeFill;
    ctx.fill();
    // Kind accent bar (left edge)
    if (!isPill && w > 20) {
      ctx.save();
      roundRectPath(ctx, tl.x, tl.y, w, h, radius);
      ctx.clip();
      ctx.fillStyle = KIND_ACCENT[d.kind] ?? "#64748b";
      ctx.fillRect(tl.x, tl.y, Math.max(3, 4 * vp.zoom), h);
      ctx.restore();
    }
    // Status border (+ drift dash / sim ring)
    const sim = d.simVerdict;
    roundRectPath(ctx, tl.x, tl.y, w, h, radius);
    if (sim) {
      ctx.strokeStyle = SIM_STROKE[sim] ?? statusBorder(d.status, theme);
      ctx.lineWidth = 2.5;
      ctx.setLineDash(d.simRole === "missing" ? [5, 3] : []);
    } else {
      ctx.strokeStyle = statusBorder(d.status, theme);
      ctx.lineWidth = d.status === "down" || d.status === "degraded" ? 1.75 : 1;
      ctx.setLineDash(nodeHasFinding ? [4, 3] : []);
    }
    ctx.stroke();
    ctx.setLineDash([]);
    // Highlight ring (hover chain)
    if (d.highlighted && !sim) {
      roundRectPath(ctx, tl.x - 1.5, tl.y - 1.5, w + 3, h + 3, radius + 1.5);
      ctx.strokeStyle = "#3b82f6";
      ctx.lineWidth = 2;
      ctx.stroke();
    }
    // Selection outline
    if (n.selected) {
      roundRectPath(ctx, tl.x - 3, tl.y - 3, w + 6, h + 6, radius + 3);
      ctx.strokeStyle = "#2563eb";
      ctx.lineWidth = 2;
      ctx.stroke();
    }

    // T-3505: below the zoom at which text is legible (showText false —
    // canvasDraw.ts's existing LOD signal, no parallel scheme of this
    // task's own), a physnic/guest-nic node still gets a coarse jack mark
    // instead of quietly reverting to the same accent-barred rectangle
    // every other kind draws at this zoom — "distinguishes the kinds" per
    // the card, not just by the accent bar's color. Proportionally sized so
    // it never outgrows a shrunk box, and skipped below the accent bar's
    // own w > 20 "too small to bother" gate.
    if (!showText && jackKind && w > 20) {
      const jw = Math.min(14, w * 0.35);
      const jh = jw * (11 / 15);
      drawJack(ctx, tl.x + (w - jw) / 2, tl.y + (h - jh) / 2, jw, jh, jackKind, statusBorder(d.status, theme), false);
    }

    // Text
    if (showText) {
      ctx.save();
      roundRectPath(ctx, tl.x, tl.y, w, h, radius);
      ctx.clip();
      const padX = tl.x + 8;
      ctx.fillStyle = theme.nodeText;
      ctx.font = "600 11px ui-sans-serif, system-ui, sans-serif";
      ctx.textAlign = "left";
      const labelY = showBadges ? tl.y + h * 0.32 : tl.y + h / 2;
      ctx.fillText(d.label, padX, labelY, w - 16);
      if (!isPill && showBadges && jackKind) {
        // T-3505: physnic/guest-nic swap the generic uppercase kind word
        // for their drawn jack — strictly more information (copper vs
        // fibre vs unknown vs virtual, the same distinction
        // SwitchFaceplate.tsx's NicPort/AccessPort draw) in the same corner
        // the word occupied. Screen-reader parity is unaffected by this
        // swap: entityAriaLabel (a11yBridge.ts) still speaks the kind, plus
        // the media/speed phrase, independently of what gets drawn here.
        const jw = 15;
        const jh = 11;
        const jx = tl.x + w - 8 - jw;
        const jy = labelY - jh / 2;
        drawJack(ctx, jx, jy, jw, jh, jackKind, theme.kindText, true);
        const speed = speedMarking(d.speedMbps);
        if (speed) {
          ctx.fillStyle = theme.kindText;
          ctx.font = "8px ui-sans-serif, system-ui, sans-serif";
          ctx.textAlign = "right";
          ctx.fillText(speed, jx - 3, labelY);
        }
      } else if (!isPill && showBadges) {
        ctx.fillStyle = theme.kindText;
        ctx.font = "9px ui-sans-serif, system-ui, sans-serif";
        ctx.textAlign = "right";
        ctx.fillText(d.kind.toUpperCase(), tl.x + w - 8, labelY, w * 0.45);
      }
      // Badges row
      // T-3501: the legacy bare "drift" token stays wire-present (see
      // findingBadges.ts) but is never drawn as its own pill any more — a
      // "finding:<source>:<severity>" token draws as its source name,
      // severity-coloured (FINDING_SEVERITY_FILL/TEXT), instead of the
      // literal word "drift" every open finding used to paint regardless
      // of what actually fired.
      const drawableBadges = d.badges.filter((b) => b !== "drift");
      if (showBadges && drawableBadges.length > 0) {
        ctx.textAlign = "left";
        ctx.font = "9px ui-sans-serif, system-ui, sans-serif";
        let bx = padX;
        const by = tl.y + h * 0.72;
        for (const badge of drawableBadges.slice(0, 3)) {
          const parsedFinding = parseFindingBadge(badge);
          const text = parsedFinding
            ? findingChipText(parsedFinding)
            : badge.length > 12
              ? `${badge.slice(0, 11)}…`
              : badge;
          const tw = ctx.measureText(text).width + 8;
          if (bx + tw > tl.x + w - 4) break;
          const isMgmt = MGMT_BADGES.has(badge);
          roundRectPath(ctx, bx, by - 7, tw, 13, 3);
          ctx.fillStyle = parsedFinding
            ? FINDING_SEVERITY_FILL[parsedFinding.severity]
            : isMgmt
              ? theme.mgmtBadgeBg
              : theme.badgeBg;
          ctx.fill();
          ctx.fillStyle = parsedFinding
            ? FINDING_SEVERITY_TEXT[parsedFinding.severity]
            : isMgmt
              ? theme.mgmtBadgeText
              : theme.badgeText;
          ctx.fillText(text, bx + 4, by);
          bx += tw + 3;
        }
      }
      ctx.restore();
    }

    // Path-simulator marker (small dot, top-right corner)
    if (sim && d.simRole) {
      ctx.beginPath();
      ctx.arc(tl.x + w, tl.y, 4, 0, Math.PI * 2);
      ctx.fillStyle = SIM_STROKE[sim] ?? "#8b5cf6";
      ctx.fill();
      ctx.strokeStyle = theme.background;
      ctx.lineWidth = 1.5;
      ctx.stroke();
    }
    ctx.restore();
  }
}

// --- T-1003: the "Flows" layer overlay ------------------------------------
// Drawn as a wholly separate pass, after drawScene's normal edges/nodes, so
// it never participates in the base scene's status/traffic-mode styling —
// the card's "visually distinct from trafficMode... so the two don't
// collide" requirement is satisfied by construction: a flow edge always
// renders in its own fixed cyan accent (never STATUS_STROKE or
// trafficEdgeStyle's heat palette), animated via a dashed stroke whose
// offset marches from source to destination.

/** A flowEdges.ts FlowEdge, pre-resolved to a stroke width (flowEdges.ts's
 * flowEdgeStrokeWidth) — canvasDraw.ts itself has no opinion on the
 * bytes/sec -> width mapping, only on drawing the resulting line. */
export interface FlowOverlayEdge {
  id: string;
  from: string;
  to: string;
  strokeWidth: number;
}

export interface DrawFlowOverlayParams {
  nodes: FlowNode<EntityNodeData, "entity">[];
  edges: readonly FlowOverlayEdge[];
  viewport: Viewport;
  nodeSize: Size;
  dragTopLeft?: DrawSceneParams["dragTopLeft"];
  /** Animation phase in px — the dash pattern's lineDashOffset, so a
   * caller ticking this over time gets a "flowing toward the destination"
   * look. 0 (reduced-motion callers) renders a static dashed line. */
  dashOffset?: number;
  /** The currently-selected flow edge (drill-down panel open for it), if
   * any — rendered thicker/brighter than the rest. */
  selectedId?: string;
}

const FLOW_EDGE_COLOR = "#06b6d4"; // cyan-500: distinct from every STATUS_STROKE/SIM_STROKE/heat-scale color already in use
const FLOW_EDGE_SELECTED_COLOR = "#0e7490"; // cyan-700

export function drawFlowOverlay(ctx: CanvasRenderingContext2D, params: DrawFlowOverlayParams): void {
  const { nodes, edges, viewport: vp, nodeSize, dragTopLeft, dashOffset = 0, selectedId } = params;
  if (edges.length === 0) return;
  const size = nodeSize.width > 0 ? nodeSize : DEFAULT_NODE_SIZE;
  const byId = new Map<string, FlowNode<EntityNodeData, "entity">>();
  for (const n of nodes) byId.set(n.id, n);

  ctx.save();
  ctx.lineCap = "round";
  for (const e of edges) {
    const from = byId.get(e.from);
    const to = byId.get(e.to);
    if (!from || !to) continue;
    const a = nodeCenterScreen(from, vp, size, dragTopLeft);
    const b = nodeCenterScreen(to, vp, size, dragTopLeft);
    const selected = e.id === selectedId;
    ctx.save();
    ctx.strokeStyle = selected ? FLOW_EDGE_SELECTED_COLOR : FLOW_EDGE_COLOR;
    ctx.lineWidth = selected ? e.strokeWidth + 1.5 : e.strokeWidth;
    ctx.setLineDash([8, 6]);
    ctx.lineDashOffset = -dashOffset;
    ctx.globalAlpha = 0.9;
    ctx.beginPath();
    ctx.moveTo(a.x, a.y);
    ctx.lineTo(b.x, b.y);
    ctx.stroke();
    ctx.restore();
  }
  ctx.restore();
}

// --- T-1303: the "Latency" heatmap layer overlay ---------------------------
// A second, independent overlay pass, drawn after drawScene (and after
// drawFlowOverlay, when both happen to be active) — a solid, color-scaled
// line per link (latencyMode.ts's own doc comment explains why its palette
// never collides with drawFlowOverlay's fixed cyan or drawScene's own
// status/traffic-mode colors). Unlike drawFlowOverlay this has no dash
// animation: a heatmap communicates "current condition of this path", not
// "traffic flowing in this direction".

export interface DrawLatencyOverlayParams {
  nodes: FlowNode<EntityNodeData, "entity">[];
  edges: readonly LatencyOverlayEdge[];
  viewport: Viewport;
  nodeSize: Size;
  dragTopLeft?: DrawSceneParams["dragTopLeft"];
}

export function drawLatencyOverlay(ctx: CanvasRenderingContext2D, params: DrawLatencyOverlayParams): void {
  const { nodes, edges, viewport: vp, nodeSize, dragTopLeft } = params;
  if (edges.length === 0) return;
  const size = nodeSize.width > 0 ? nodeSize : DEFAULT_NODE_SIZE;
  const byId = new Map<string, FlowNode<EntityNodeData, "entity">>();
  for (const n of nodes) byId.set(n.id, n);

  ctx.save();
  ctx.lineCap = "round";
  for (const e of edges) {
    const from = byId.get(e.from);
    const to = byId.get(e.to);
    if (!from || !to) continue;
    const a = nodeCenterScreen(from, vp, size, dragTopLeft);
    const b = nodeCenterScreen(to, vp, size, dragTopLeft);
    ctx.save();
    ctx.strokeStyle = e.color;
    ctx.lineWidth = e.strokeWidth;
    ctx.globalAlpha = 0.9;
    ctx.beginPath();
    ctx.moveTo(a.x, a.y);
    ctx.lineTo(b.x, b.y);
    ctx.stroke();
    ctx.restore();
  }
  ctx.restore();
}

// --- T-1306: the "Verified MTU" badge layer overlay -------------------------
// A third, independent overlay pass — unlike drawLatencyOverlay's colored
// line, this one draws a small text badge ("MTU 1450") at each probed
// link's midpoint, distinct from wherever that link's *configured* MTU is
// shown elsewhere (mtuOverlay.ts's own doc comment). A link with no probe
// result simply isn't in `badges` at all (mtuOverlay.computeMTUOverlayEdges'
// own contract), so nothing is drawn for it — never a stale/zero label.

export interface DrawMTUOverlayParams {
  nodes: FlowNode<EntityNodeData, "entity">[];
  badges: readonly MTUOverlayBadge[];
  viewport: Viewport;
  nodeSize: Size;
  theme: Pick<SceneTheme, "badgeBg" | "badgeText">;
  dragTopLeft?: DrawSceneParams["dragTopLeft"];
}

export function drawMTUOverlay(ctx: CanvasRenderingContext2D, params: DrawMTUOverlayParams): void {
  const { nodes, badges, viewport: vp, nodeSize, theme, dragTopLeft } = params;
  if (badges.length === 0) return;
  const size = nodeSize.width > 0 ? nodeSize : DEFAULT_NODE_SIZE;
  const byId = new Map<string, FlowNode<EntityNodeData, "entity">>();
  for (const n of nodes) byId.set(n.id, n);

  ctx.save();
  ctx.font = "600 9px ui-sans-serif, system-ui, sans-serif";
  ctx.textBaseline = "middle";
  for (const b of badges) {
    const from = byId.get(b.from);
    const to = byId.get(b.to);
    if (!from || !to) continue;
    const a = nodeCenterScreen(from, vp, size, dragTopLeft);
    const c = nodeCenterScreen(to, vp, size, dragTopLeft);
    const midX = (a.x + c.x) / 2;
    const midY = (a.y + c.y) / 2;

    const label = formatMTUBadgeLabel(b.mtu);
    const textWidth = ctx.measureText(label).width;
    const padX = 4;
    const boxW = textWidth + padX * 2;
    const boxH = 14;

    ctx.save();
    ctx.fillStyle = theme.badgeBg;
    roundRectPath(ctx, midX - boxW / 2, midY - boxH / 2, boxW, boxH, 4);
    ctx.fill();
    ctx.fillStyle = theme.badgeText;
    ctx.fillText(label, midX - textWidth / 2, midY + 0.5);
    ctx.restore();
  }
  ctx.restore();
}

// --- T-2704: the point-in-time diff overlay ---------------------------------
// A fourth, independent overlay pass: for a selected historical range, ring
// every map node whose entity differs from what it was at the `from` point,
// with a corner glyph naming the kind of difference.
//
// The ring color carries the attribution (topology/diffOverlay.ts's
// diffMarkColor): a change no changeset explains — an edit made outside
// vnprox — is painted in its own color regardless of what KIND of change it
// is, because "vnprox did not do this" is the distinction an operator is
// scanning the map for. An unattributed mark is additionally drawn with a
// solid, thicker ring where an attributed one is dashed and thin, so the two
// stay distinguishable without relying on color alone.
//
// Entities not currently on the map (a deleted bridge, or one the active
// layer/VLAN filters hide) are not in `marks` at all — computeDiffOverlay
// routes them to its `offMap` list, which the page surfaces as text. Silently
// dropping them would make "nothing is highlighted" and "nothing changed"
// look identical.

export interface DrawDiffOverlayParams {
  nodes: FlowNode<EntityNodeData, "entity">[];
  marks: readonly DiffMark[];
  viewport: Viewport;
  nodeSize: Size;
  dragTopLeft?: DrawSceneParams["dragTopLeft"];
}

export function drawDiffOverlay(ctx: CanvasRenderingContext2D, params: DrawDiffOverlayParams): void {
  const { nodes, marks, viewport: vp, nodeSize, dragTopLeft } = params;
  if (marks.length === 0) return;
  const size = nodeSize.width > 0 ? nodeSize : DEFAULT_NODE_SIZE;
  const byId = new Map<string, FlowNode<EntityNodeData, "entity">>();
  for (const n of nodes) byId.set(n.id, n);

  ctx.save();
  ctx.font = "700 11px ui-sans-serif, system-ui, sans-serif";
  ctx.textBaseline = "middle";
  for (const mark of marks) {
    const node = byId.get(mark.nodeId);
    if (!node) continue;
    const center = nodeCenterScreen(node, vp, size, dragTopLeft);
    const w = size.width * vp.zoom;
    const h = size.height * vp.zoom;
    const x = center.x - w / 2;
    const y = center.y - h / 2;
    const color = diffMarkColor(mark);

    ctx.save();
    ctx.strokeStyle = color;
    ctx.lineWidth = mark.attributed ? 2 : 3.5;
    ctx.setLineDash(mark.attributed ? [5, 4] : []);
    roundRectPath(ctx, x - 4, y - 4, w + 8, h + 8, 10);
    ctx.stroke();
    ctx.restore();

    // Corner glyph: +, −, ~ — the change kind, readable without color.
    const glyph = diffMarkGlyph(mark.change);
    ctx.save();
    ctx.fillStyle = color;
    ctx.beginPath();
    ctx.arc(x + w + 2, y - 2, 8, 0, Math.PI * 2);
    ctx.fill();
    ctx.fillStyle = "#ffffff";
    const glyphWidth = ctx.measureText(glyph).width;
    ctx.fillText(glyph, x + w + 2 - glyphWidth / 2, y - 1);
    ctx.restore();
  }
  ctx.restore();
}
