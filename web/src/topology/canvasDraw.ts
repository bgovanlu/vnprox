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
import type { EntityStatus } from "../api/types";
import type { EntityEdgeData } from "./EntityEdge";
import type { EntityNodeData } from "./EntityNode";
import type { Size, Viewport } from "./canvasScene";
import { DEFAULT_NODE_SIZE, graphToScreen } from "./canvasScene";
import { trafficEdgeStyle } from "./trafficMode";

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
      dashed = status === "unknown" || (data?.badges.includes("drift") ?? false);
    }
    const driftingEdge = data?.badges.includes("drift") ?? false;
    ctx.save();
    // T-905: the drift pulse (reduced-motion falls back to pulseAlpha's
    // default of 1, i.e. the plain static dashed look) — never applied to
    // an already-dimmed edge, so the two alpha treatments don't compound.
    ctx.globalAlpha = dimmed ? 0.15 : driftingEdge ? pulseAlpha : 1;
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
    const driftingNode = d.badges.includes("drift");
    ctx.save();
    // T-905: same drift-pulse treatment as the edge loop above — dim/stale
    // both win over it (never compounded), and reduced motion collapses
    // pulseAlpha to its default 1 (a plain static look).
    ctx.globalAlpha = dimmed ? 0.25 : d.stale ? 0.6 : driftingNode ? pulseAlpha : 1;

    const isPill = d.isGuestGroup;
    const radius = isPill ? h / 2 : 6;
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
      ctx.setLineDash(d.badges.includes("drift") ? [4, 3] : []);
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
      if (!isPill && showBadges) {
        ctx.fillStyle = theme.kindText;
        ctx.font = "9px ui-sans-serif, system-ui, sans-serif";
        ctx.textAlign = "right";
        ctx.fillText(d.kind.toUpperCase(), tl.x + w - 8, labelY, w * 0.45);
      }
      // Badges row
      if (showBadges && d.badges.length > 0) {
        ctx.textAlign = "left";
        ctx.font = "9px ui-sans-serif, system-ui, sans-serif";
        let bx = padX;
        const by = tl.y + h * 0.72;
        for (const badge of d.badges.slice(0, 3)) {
          const text = badge.length > 12 ? `${badge.slice(0, 11)}…` : badge;
          const tw = ctx.measureText(text).width + 8;
          if (bx + tw > tl.x + w - 4) break;
          const isMgmt = MGMT_BADGES.has(badge);
          roundRectPath(ctx, bx, by - 7, tw, 13, 3);
          ctx.fillStyle = isMgmt ? theme.mgmtBadgeBg : theme.badgeBg;
          ctx.fill();
          ctx.fillStyle = isMgmt ? theme.mgmtBadgeText : theme.badgeText;
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
