// SPDX-License-Identifier: Apache-2.0

// The v2 canvas draw pass (TopologyCanvasV2.tsx). Kept in its own module so
// the component file stays about interaction/state and this stays about
// pixels. It is a pure function of (FlowElements, viewport, theme): it reads
// the same EntityNodeData/EntityEdgeData the v1 DOM renderer reads, and
// paints the same visual vocabulary — status colors, kind pictograms, VLAN-dim,
// hover-chain highlight, selection, the amber mgmt/corosync/mgmt-path badge
// trio, the drift dashed outline, and the path-simulator overlay — so v2 is a
// faithful rendering swap, not a different picture (T-901 parity).
//
// Not unit-tested directly (it needs a real CanvasRenderingContext2D, which
// jsdom lacks); its correctness is covered by the golden-parity render tests
// asserting the *data* both renderers consume is identical, plus the e2e
// perf/scale run that exercises the real draw path in headless Chromium.
import type { Edge as FlowEdge, Node as FlowNode } from "@xyflow/react";
import type { EntityStatus, Severity, SimVerdict } from "../api/types";
import type { EntityEdgeData } from "./EntityEdge";
import type { EntityNodeData } from "./EntityNode";
import type { Size, Viewport } from "./canvasScene";
import { DEFAULT_NODE_SIZE, graphToScreen } from "./canvasScene";
import type { LatencyOverlayEdge } from "./latencyMode";
import type { BlastRadiusRole } from "./blastRadiusFocus";
import { diffGlyphColor, diffMarkColor, diffMarkGlyph, type DiffMark } from "./diffOverlay";
import { findingChipText, hasOpenFinding, parseFindingBadge, shouldPulse } from "./findingBadges";
import { formatMTUBadgeLabel, type MTUOverlayBadge } from "./mtuOverlay";
import { drawGlyph, glyphOps } from "./canvasGlyphs";
import { jackKindForEntity, speedMarking, type PortBodyKind } from "./portMedia";
import { SIM_INDETERMINATE_COLOR, simVerdictTone } from "./simVerdict";
import { recencyGlyphColor, recencyMarkColor, recencyMarkGlyph, type RecencyMark } from "./recencyOverlay";
import { trafficEdgeStyle, type UtilizationTone } from "./trafficMode";

// T-3501/T-4204: severity fill/text colours for a "finding:<source>:<severity>"
// badge chip on the canvas — the same soft-wash/fg pairing
// findingBadges.ts's findingBadgeClass now draws from the T-4204 semantic
// status scale (index.css's --color-status-critical/-degraded/-info, bare
// and `-soft`) in the DOM renderers, as literal per-theme hex here since a
// <canvas> has no Tailwind classes (and no `dark:` variant) to defer to.
// These live on `theme` (populated by TopologyCanvasV2.tsx's themeColors,
// matching mgmtBadgeBg/mgmtBadgeText's existing per-theme-pair convention)
// rather than as a flat theme-blind constant, because a flat constant is
// exactly the bug this fixed: the v2 canvas badge used to paint the same
// light-mode pastel fill regardless of theme, which is real v1/v2 drift on
// this file's own "T-901 parity" requirement, not merely a missed style.
function findingSeverityFill(theme: SceneTheme, severity: Severity): string {
  switch (severity) {
    case "error":
      return theme.findingErrorFill;
    case "warning":
      return theme.findingWarningFill;
    case "info":
    default:
      return theme.findingInfoFill;
  }
}
function findingSeverityText(theme: SceneTheme, severity: Severity): string {
  switch (severity) {
    case "error":
      return theme.findingErrorText;
    case "warning":
      return theme.findingWarningText;
    case "info":
    default:
      return theme.findingInfoText;
  }
}

export interface SceneTheme {
  background: string;
  nodeFill: string;
  nodeText: string;
  kindText: string;
  nodeBorderOk: string;
  /** T-4302: the status scale, for the three states that are not `ok`.
   * These existed twice as literals — `STATUS_STROKE` for edges and
   * `statusBorder`'s switch for nodes — a second and third status palette
   * beside the real one, neither re-pointing per theme. With the kind accent
   * gone, colour on a node means status alone, so it had better BE the status
   * scale. `unknown` takes `--color-status-unknown` rather than the outline
   * token: it is a state the product names, and the dash is what separates it
   * from `ok` at a glance. */
  statusDown: string;
  statusDegraded: string;
  statusUnknown: string;
  /** T-4306: `--color-status-ok`, the GREEN one — deliberately not the same
   * as `nodeBorderOk`, which is `--color-outline` because a healthy node is
   * the absence of a signal (StatusDot's convention). The path simulator's
   * `allow` verdict is the opposite case: it is an answer to a question the
   * operator asked, so it says yes in green. */
  statusOk: string;
  badgeBg: string;
  badgeText: string;
  /** T-4301 remainder: the minimap's own two colours. It is a second
   * `<canvas>` inside the same component, and it was carrying the exact
   * `dark ? "#0f172a" : "#f1f5f9"` shape this module's palette exists to
   * delete — so it resolves through the same one pass rather than growing a
   * second resolver.
   *
   * Measured before choosing: the dots were `#94a3b8` on `#f1f5f9` (2.34) and
   * `#475569` on `#0f172a` (2.36), against WCAG 1.4.11's 3:1 — failing in
   * both themes, symmetrically, which is what picking a light value and a
   * dark value by eye produces. They are the minimap's ONLY content; the
   * viewport rectangle just says where you are. `--color-fg-subtle` measures
   * 4.64 / 7.47 on `--color-surface-sunken`, and the extra headroom over the
   * 3:1 floor is deliberate for a 2x2px mark. */
  minimapBg: string;
  minimapDot: string;
  mgmtBadgeBg: string;
  mgmtBadgeText: string;
  edgeDefault: string;
  /** T-4204: the semantic status scale's `-soft`/bare pairing for a
   * "finding:<source>:error" badge chip, per theme — see findingSeverityFill
   * above for why these are theme fields rather than a flat constant. */
  findingErrorFill: string;
  findingErrorText: string;
  findingWarningFill: string;
  findingWarningText: string;
  findingInfoFill: string;
  findingInfoText: string;
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

/** Below this the kind pictogram is skipped rather than drawn illegibly —
 * see the draw site's comment. 7px, because the smallest interior the icon
 * set draws is designed against a 16px floor and half of that is already
 * generous; the node's label has stopped rendering well before it. */
const GLYPH_MIN_PX = 7;

/** T-4306: was a fourth copy of the verdict palette, three of whose four
 * values sat 0.2-17deg from the status hue they were mirroring on purpose.
 * The mapping now lives in simVerdict.ts and this resolves it against the
 * already-resolved scene palette, the way TRAFFIC_TONE does for T-4303's
 * bands. `indeterminate` keeps a literal because "could not decide" is not a
 * health state — see simVerdict.ts. */
function simStroke(verdict: string, theme: SceneTheme): string | undefined {
  if (!isSimVerdict(verdict)) return undefined;
  switch (simVerdictTone(verdict)) {
    case "status-ok":
      return theme.statusOk;
    case "status-critical":
      return theme.statusDown;
    case "status-degraded":
      return theme.statusDegraded;
    case "sim-indeterminate":
      return SIM_INDETERMINATE_COLOR;
  }
}

const SIM_VERDICTS = new Set(["allow", "deny", "unreachable", "indeterminate"]);
function isSimVerdict(v: string): v is SimVerdict {
  return SIM_VERDICTS.has(v);
}

// T-4302: `KIND_ACCENT` used to live here — fourteen kinds, each with its own
// hue, painted as a 3-4px rail down the node's left edge. It is deleted, not
// re-pointed at tokens, because the scale itself was the defect: four of its
// six distinct hues sat closer to a status colour than the status colours sit
// to each other, and the two worst (bond 13deg from the accent, guest 17deg
// from `ok`) are the two commonest kinds in any cluster. At a 3px width hue
// is the only channel available, so the rail was competing for exactly the
// channel status needs.
//
// Kind is now drawn as the T-4205 pictogram (canvasGlyphs.ts). Shape is a
// categorical channel with no capacity limit and no collision with status,
// and those glyphs were drawn to work small. Colour on a node now means
// status, and nothing else.

/** Resolves a T-4303 utilization tone against the already-resolved scene
 * palette. `edgeDefault` IS `--color-outline` (see canvasPalette's ROLE
 * table), so the neutral band reuses the value the map already draws its
 * ordinary edges with rather than naming the token twice. */
const TRAFFIC_TONE: Record<UtilizationTone, (theme: SceneTheme) => string> = {
  outline: (theme) => theme.edgeDefault,
  "status-degraded": (theme) => theme.findingWarningText,
  "status-critical": (theme) => theme.findingErrorText,
};

const MGMT_BADGES = new Set(["mgmt", "corosync", "mgmt-path"]);

function statusBorder(status: EntityStatus, theme: SceneTheme): string {
  switch (status) {
    case "down":
      return theme.statusDown;
    case "degraded":
      return theme.statusDegraded;
    case "unknown":
      return theme.statusUnknown;
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
      stroke = simStroke(sim, theme) ?? theme.edgeDefault;
      width = 3.5;
    } else if (trafficMode) {
      const t = trafficEdgeStyle(data?.utilizationPct);
      // T-4303: the tone names a design token; the canvas cannot use
      // `var()` in `strokeStyle`, so it resolves through the SceneTheme the
      // palette already built. The DOM renderer takes the same tone and
      // writes `var(--color-...)` directly — one name, two resolutions, no
      // third copy of the colours.
      stroke = TRAFFIC_TONE[t.tone](theme);
      width = t.strokeWidth;
    } else {
      const status = data?.status ?? "ok";
      // T-4302: was `STATUS_STROKE`, a second status palette that differed
      // from `statusBorder`'s third one only in what it called `ok`. One
      // scale now, resolved from `--color-status-*` through canvasPalette,
      // so an edge and the node it lands on cannot disagree about a colour.
      stroke = statusBorder(status, theme);
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
    // T-4302: kind, as a shape. Where the accent rail used to be — the node's
    // left edge when there is room for a label beside it, dead centre when
    // there is not (the band the rail used to own alone, and the one AC4 asks
    // about). The glyph is drawn in `kindText`, the same role the uppercase
    // kind word takes, so kind reads at one weight whichever channel carries
    // it at this zoom.
    //
    // `glyphBox` feeds `glyphOps` as a size, not a resolution: it selects the
    // icon set's own simplified interior below INLINE_THRESHOLD, so a
    // shrinking node sheds the same interior lines a shrinking table-row icon
    // does. Below GLYPH_MIN_PX nothing is drawn at all — a 5px pictogram is
    // not a smaller pictogram, it is a smudge, and the rail it replaced was
    // no more legible there.
    const glyphBox = showText
      ? Math.min(18 * vp.zoom, h * 0.42, w * 0.22)
      : Math.min(h * 0.6, w * 0.5);
    // physnic/guest-nic keep T-3505's drawn jack at low zoom instead: copper
    // vs fibre vs virtual is strictly more than "this is a NIC", and it is
    // the same corner.
    const centredGlyph = !showText && !jackKind;
    const drawsGlyph = !isPill && glyphBox >= GLYPH_MIN_PX && (showText || centredGlyph);
    if (drawsGlyph) {
      const gx = showText ? tl.x + 6 : tl.x + (w - glyphBox) / 2;
      drawGlyph(ctx, glyphOps(d.kind, glyphBox), gx, tl.y + (h - glyphBox) / 2, glyphBox, theme.kindText);
    }
    // Status border (+ drift dash / sim ring)
    const sim = d.simVerdict;
    roundRectPath(ctx, tl.x, tl.y, w, h, radius);
    if (sim) {
      ctx.strokeStyle = simStroke(sim, theme) ?? statusBorder(d.status, theme);
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
      // T-4302: the label starts clear of the pictogram. The accent rail this
      // replaced was 3-4px and the label simply cleared it at a flat 8px pad;
      // a glyph is wider, so the pad is computed rather than constant, and
      // falls back to the old 8 exactly when no glyph was drawn.
      const padX = drawsGlyph ? tl.x + 6 + glyphBox + 5 : tl.x + 8;
      ctx.fillStyle = theme.nodeText;
      ctx.font = "600 11px ui-sans-serif, system-ui, sans-serif";
      ctx.textAlign = "left";
      const labelY = showBadges ? tl.y + h * 0.32 : tl.y + h / 2;
      ctx.fillText(d.label, padX, labelY, Math.max(1, tl.x + w - 8 - padX));
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
            ? findingSeverityFill(theme, parsedFinding.severity)
            : isMgmt
              ? theme.mgmtBadgeBg
              : theme.badgeBg;
          ctx.fill();
          ctx.fillStyle = parsedFinding
            ? findingSeverityText(theme, parsedFinding.severity)
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
      ctx.fillStyle = simStroke(sim, theme) ?? SIM_INDETERMINATE_COLOR;
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
// renders in its own fixed cyan accent (never the status scale or
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

const FLOW_EDGE_COLOR = "#06b6d4"; // cyan-500: distinct from every status/SIM_STROKE/heat-scale colour already in use
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
  /** T-4303: the overlay's edges name design tokens rather than carrying
   * colours, so this draw needs the resolved palette like drawScene does. */
  theme: SceneTheme;
  dragTopLeft?: DrawSceneParams["dragTopLeft"];
}

export function drawLatencyOverlay(ctx: CanvasRenderingContext2D, params: DrawLatencyOverlayParams): void {
  const { nodes, edges, viewport: vp, nodeSize, theme, dragTopLeft } = params;
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
    // T-4303: the overlay edge names a token; the canvas resolves it the
    // same way traffic mode does, through the palette already built.
    ctx.strokeStyle = TRAFFIC_TONE[e.tone](theme);
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
    // T-4303: white measured 3.30 on `added` (#16a34a) and 3.96 on `changed`
    // (#a855f7) — below AA for a glyph that exists as this overlay's
    // non-colour channel. Picked per mark; see diffGlyphColor.
    ctx.fillStyle = diffGlyphColor(mark);
    const glyphWidth = ctx.measureText(glyph).width;
    ctx.fillText(glyph, x + w + 2 - glyphWidth / 2, y - 1);
    ctx.restore();
  }
  ctx.restore();
}

// --- T-3908: the "what changed" recency overlay -----------------------------
// A fifth, independent overlay pass: for every entity a config-change diff
// covers, a small filled corner badge naming how long ago it last changed
// (or, for drift, that vnprox does not know when).
//
// DELIBERATELY NOT A RING. drawDiffOverlay above rings the whole node
// border (top-right corner glyph) — this overlay paints only a single
// filled badge, in the OPPOSITE corner (bottom-left), specifically so the
// diff overlay and this one can both be active at once without their marks
// visually stacking into an unreadable double-outline (T-3908 AC2: "composes
// with, does not visually collide with, the diff and Ceph overlays"). An
// entity with no recency mark at all is left with no badge — the visually
// distinct "no change in the lookback window" state this task's card asks
// for, exactly mirroring drawDiffOverlay's own "no ring = no difference"
// convention.
//
// Every badge carries a letter glyph (recencyMarkGlyph) in addition to its
// heat color, so the signal never depends on color alone (T-3908's WCAG
// requirement) — the same non-color-channel precedent diffMarkGlyph already
// established above.

export interface DrawRecencyOverlayParams {
  nodes: FlowNode<EntityNodeData, "entity">[];
  marks: readonly RecencyMark[];
  viewport: Viewport;
  nodeSize: Size;
  dragTopLeft?: DrawSceneParams["dragTopLeft"];
}

export function drawRecencyOverlay(ctx: CanvasRenderingContext2D, params: DrawRecencyOverlayParams): void {
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
    const color = recencyMarkColor(mark.bucket);
    const glyph = recencyMarkGlyph(mark.bucket);

    ctx.save();
    ctx.fillStyle = color;
    ctx.beginPath();
    ctx.arc(x - 2, y + h + 2, 8, 0, Math.PI * 2);
    ctx.fill();
    // T-4303: per-bucket, not flat white — white measured 2.80 and 3.19 on
    // the two warm fills, so the glyph that exists AS the non-colour channel
    // was itself unreadable. See recencyGlyphColor for the table.
    ctx.fillStyle = recencyGlyphColor(mark.bucket);
    const glyphWidth = ctx.measureText(glyph).width;
    ctx.fillText(glyph, x - 2 - glyphWidth / 2, y + h + 3);
    ctx.restore();
  }
  ctx.restore();
}

// --- T-3912: the blast-radius focus lens ------------------------------------
// Unlike the marks-only overlays above (diff's ring, recency's badge), this
// pass also SUBTRACTS prominence from everything outside the focused
// subgraph — the literal "dim/hide everything else" the card asks for. It
// is still an additive pass, drawn on top of drawScene's already-painted
// pixels rather than reaching into drawScene's own dimmed/highlighted alpha
// (which stays reserved for the VLAN filter/hover chain) — so this composes
// with every overlay above it without a special case anywhere else in this
// file, the same reasoning drawRecencyOverlay's own doc comment gives for
// staying a separate pass.
//
// NON-COLLIDING VISUAL CHANNEL (per this task's card). drawDiffOverlay rings
// the top-right corner; drawRecencyOverlay badges the bottom-left corner.
// This overlay's own per-entity mark sits at the BOTTOM-RIGHT corner — a
// small role glyph (T/!/* : target / affected / path-hop) — so a node can
// carry a diff ring, a recency badge, AND a blast-radius badge at once
// without any of the three drawing over another. The scrim (translucent
// theme-background fill over every non-focused node, theme-background
// restroke over every non-focused edge) and the solid accent-colored
// path-edge stroke are the parts nothing else in this file does at all.
const BLAST_RADIUS_COLOR = "#c026d3"; // fuchsia-600 — distinct from every status/SIM_STROKE/FLOW_EDGE_COLOR/diff/recency colour already in use (the KIND_ACCENT it also had to clear is gone, T-4302)
const BLAST_RADIUS_SCRIM_ALPHA = 0.72;
const BLAST_RADIUS_ROLE_GLYPH: Record<BlastRadiusRole, string> = {
  target: "X",
  affected: "!",
  path: "*",
};

export interface DrawBlastRadiusOverlayParams {
  nodes: FlowNode<EntityNodeData, "entity">[];
  edges: FlowEdge<EntityEdgeData, "entity">[];
  /** blastRadiusFocus.ts's `BlastRadiusFocus.focusNodeIds`/`focusEdgeIds`/
   * `roles` — resolved against the CURRENTLY rendered `nodes`/`edges`
   * already, so this function does no further presence-checking of its
   * own. Nothing is drawn (not even a scrim) when `focusNodeIds` is empty —
   * an inactive/degraded focus (blastRadiusFocus.ts's `active: false`) must
   * leave the map exactly as unfocused as it would be with no request at
   * all, never blanked. */
  focusNodeIds: ReadonlySet<string>;
  focusEdgeIds: ReadonlySet<string>;
  roles: ReadonlyMap<string, BlastRadiusRole>;
  viewport: Viewport;
  view: Size;
  theme: SceneTheme;
  nodeSize: Size;
  dragTopLeft?: DrawSceneParams["dragTopLeft"];
}

export function drawBlastRadiusOverlay(ctx: CanvasRenderingContext2D, params: DrawBlastRadiusOverlayParams): void {
  const { nodes, edges, focusNodeIds, focusEdgeIds, roles, viewport: vp, view, theme, nodeSize, dragTopLeft } = params;
  if (focusNodeIds.size === 0) return;
  const size = nodeSize.width > 0 ? nodeSize : DEFAULT_NODE_SIZE;
  const byId = new Map<string, FlowNode<EntityNodeData, "entity">>();
  for (const n of nodes) byId.set(n.id, n);

  // --- Edges: scrim everything off the path, restroke everything on it -----
  ctx.save();
  ctx.lineCap = "round";
  for (const e of edges) {
    const from = byId.get(e.source);
    const to = byId.get(e.target);
    if (!from || !to) continue;
    const a = nodeCenterScreen(from, vp, size, dragTopLeft);
    const b = nodeCenterScreen(to, vp, size, dragTopLeft);
    ctx.beginPath();
    ctx.moveTo(a.x, a.y);
    ctx.lineTo(b.x, b.y);
    if (focusEdgeIds.has(e.id)) {
      ctx.strokeStyle = BLAST_RADIUS_COLOR;
      ctx.lineWidth = 3;
    } else {
      ctx.strokeStyle = theme.background;
      ctx.globalAlpha = BLAST_RADIUS_SCRIM_ALPHA;
      ctx.lineWidth = 4;
    }
    ctx.stroke();
    ctx.globalAlpha = 1;
  }
  ctx.restore();

  // --- Nodes: scrim everything outside the focus set, badge everything in it
  const w = size.width * vp.zoom;
  const h = size.height * vp.zoom;
  ctx.save();
  ctx.font = "700 11px ui-sans-serif, system-ui, sans-serif";
  ctx.textBaseline = "middle";
  for (const n of nodes) {
    const tl = dragTopLeft?.id === n.id ? { x: dragTopLeft.x, y: dragTopLeft.y } : graphToScreen(n.position, vp);
    if (tl.x + w < 0 || tl.y + h < 0 || tl.x > view.width || tl.y > view.height) continue; // cheap cull, mirrors drawScene
    if (!focusNodeIds.has(n.id)) {
      roundRectPath(ctx, tl.x, tl.y, w, h, 6);
      ctx.fillStyle = theme.background;
      ctx.globalAlpha = BLAST_RADIUS_SCRIM_ALPHA;
      ctx.fill();
      ctx.globalAlpha = 1;
      continue;
    }
    const role = roles.get(n.id);
    if (!role) continue;

    // Focus ring, inset from diff's own -4/-4 offset (drawDiffOverlay above)
    // so both rings can be active on the same node without sitting exactly
    // on top of one another.
    roundRectPath(ctx, tl.x - 2, tl.y - 2, w + 4, h + 4, 7);
    ctx.strokeStyle = BLAST_RADIUS_COLOR;
    ctx.lineWidth = role === "path" ? 1.5 : 2.5;
    ctx.setLineDash(role === "path" ? [3, 2] : []);
    ctx.stroke();
    ctx.setLineDash([]);

    // Bottom-right corner glyph badge — the non-color channel (this task's
    // WCAG requirement): X for the failed/targeted entity, ! for a
    // named-affected one, * for a hop the path walk passed through that
    // neither source named directly.
    const glyph = BLAST_RADIUS_ROLE_GLYPH[role];
    ctx.fillStyle = BLAST_RADIUS_COLOR;
    ctx.beginPath();
    ctx.arc(tl.x + w + 2, tl.y + h + 2, 8, 0, Math.PI * 2);
    ctx.fill();
    // White is correct here and measured: 4.71 on fuchsia-600, which clears
    // AA. Stated rather than assumed, because the sibling badges above did
    // not and looked identical.
    ctx.fillStyle = "#ffffff";
    const glyphWidth = ctx.measureText(glyph).width;
    ctx.fillText(glyph, tl.x + w + 2 - glyphWidth / 2, tl.y + h + 3);
  }
  ctx.restore();
}
