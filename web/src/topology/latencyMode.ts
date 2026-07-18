// Latency heatmap paint mode (docs/features/monitoring.md §1, T-1303): a
// second, independent map overlay from trafficMode.ts's "traffic" paint
// mode — edge color-scaled by a node-to-node link's *rolling* RTT/loss
// (GET /latmesh/heatmap, `LatMeshLink.rollingRttMs`/`rollingLossPct` —
// never the single noisy `rttMs`/`lossPct` reading, same rationale
// internal/findings/health_latmesh.go's doc comment gives for comparing
// the rolling figure server-side). Pure mapping functions, kept separate
// from canvasDraw.ts's drawLatencyOverlay so the color/width curve is
// unit-testable without a real CanvasRenderingContext2D (jsdom has none —
// see canvasDraw.test.ts's own doc comment) and, per this task's AC4, so
// its palette's distinctness from trafficMode.ts's own HEAT_STOPS is
// assertable in isolation.

import type { LatMeshLink } from "../api/types";

/** LATENCY_WARN_MS/LOSS_WARN_PCT mirror internal/findings.DefaultThresholds'
 * LatRttWarnMs/LatLossWarnPct (80ms/2%) — the exact line the server's own
 * path_latency_degraded/path_loss findings fire at, so the map's "this
 * link is degraded" color reads consistently with the findings stream. */
export const LATENCY_WARN_MS = 80;
export const LOSS_WARN_PCT = 2;

/** A four-stop scale, deliberately a violet/fuchsia/pink family — chosen so
 * no hex value is shared with trafficMode.ts's blue/green/amber/red
 * HEAT_STOPS or canvasDraw.ts's FLOW_EDGE_COLOR/FLOW_EDGE_SELECTED_COLOR
 * cyan family (asserted directly in latencyMode.test.ts), so a map with
 * both a legend and this heatmap active never reads as "the same scale
 * twice." Stops are keyed on rttMs as a fraction of LATENCY_WARN_MS (25%/
 * 62.5%/100%), not fixed ms breakpoints, so the scale re-centers cleanly if
 * the server threshold constant is ever tuned without this file drifting
 * out of sync (a future improvement could fetch the live threshold; today
 * both sides simply agree on the same documented constant, mirroring how
 * trafficMode.ts's own stops are this module's own reasoned choice, not a
 * server-fetched value either). */
const LATENCY_HEAT_STOPS: readonly { maxMs: number; color: string }[] = [
  { maxMs: LATENCY_WARN_MS * 0.25, color: "#c4b5fd" }, // excellent: violet-300
  { maxMs: LATENCY_WARN_MS * 0.625, color: "#a78bfa" }, // good: violet-400
  { maxMs: LATENCY_WARN_MS, color: "#c026d3" }, // borderline (at the warn threshold): fuchsia-600
  { maxMs: Infinity, color: "#9d174d" }, // degraded (over threshold): pink-800
];

/** DEGRADED_LOSS_COLOR is LATENCY_HEAT_STOPS' own "degraded" color — a
 * link over the loss threshold reads exactly as degraded as one over the
 * RTT threshold, not a fifth, different color, so "this link has a
 * problem" always means the same thing at a glance regardless of which
 * threshold tripped. */
const DEGRADED_LOSS_COLOR = "#9d174d";

/** Maps a rolling RTT (ms) to a heat color, ignoring loss — see
 * latencyEdgeColor for the combined mapping drawLatencyOverlay actually
 * uses. Values are looked up by their first matching (inclusive) upper
 * bound, mirroring trafficMode.ts's utilizationColor. */
export function latencyColor(rollingRttMs: number): string {
  const ms = Number.isFinite(rollingRttMs) ? Math.max(0, rollingRttMs) : 0;
  for (const stop of LATENCY_HEAT_STOPS) {
    if (ms <= stop.maxMs) return stop.color;
  }
  return DEGRADED_LOSS_COLOR; // unreachable (last stop is Infinity), defensible fallback
}

/** Combines rolling RTT and rolling loss% into one heat color: a link over
 * the loss threshold always reads as degraded regardless of its RTT (a
 * lossy-but-fast path is not "good" just because the packets that do
 * arrive are quick), otherwise falls back to the pure RTT scale. */
export function latencyEdgeColor(rollingRttMs: number, rollingLossPct: number): string {
  const loss = Number.isFinite(rollingLossPct) ? rollingLossPct : 0;
  if (loss > LOSS_WARN_PCT) return DEGRADED_LOSS_COLOR;
  return latencyColor(rollingRttMs);
}

const MIN_LATENCY_STROKE_WIDTH = 1.5;
const MAX_LATENCY_STROKE_WIDTH = 6;

/** Maps rolling RTT to an edge stroke width, linear against LATENCY_WARN_MS
 * (0ms -> MIN, >=LATENCY_WARN_MS -> MAX, clamped) — mirrors trafficMode.ts's
 * utilizationStrokeWidth shape for a visually consistent "thicker = worse"
 * read across both paint modes. */
export function latencyStrokeWidth(rollingRttMs: number): number {
  const ms = Number.isFinite(rollingRttMs) ? Math.max(0, rollingRttMs) : 0;
  const pct = Math.min(1, ms / LATENCY_WARN_MS);
  return MIN_LATENCY_STROKE_WIDTH + pct * (MAX_LATENCY_STROKE_WIDTH - MIN_LATENCY_STROKE_WIDTH);
}

/** One GET /latmesh/heatmap link, pre-resolved to the color/width
 * canvasDraw.ts's drawLatencyOverlay draws — that function itself has no
 * opinion on the rtt/loss -> color/width mapping, only on drawing the
 * resulting line (the exact same division of labor drawFlowOverlay/
 * FlowOverlayEdge already establish for the Flows layer). */
export interface LatencyOverlayEdge {
  id: string;
  from: string;
  to: string;
  color: string;
  strokeWidth: number;
}

/** Resolves a LatMeshLink to its from/to on-canvas node ids: `fromNode`/
 * `toNode` are PVE node *names*, not inventory Refs, so they need
 * resolving against a node-name -> map-node-id lookup (the physical Node
 * entity's own Ref string) before they're paintable — nodeIdForName does
 * that (undefined when the named node isn't currently rendered, e.g.
 * filtered out of view). */
export function computeLatencyOverlayEdges(
  links: readonly LatMeshLink[],
  nodeIdForName: (nodeName: string) => string | undefined,
): LatencyOverlayEdge[] {
  const out: LatencyOverlayEdge[] = [];
  for (const l of links) {
    const from = nodeIdForName(l.fromNode);
    const to = nodeIdForName(l.toNode);
    if (!from || !to || from === to) continue;
    out.push({
      id: l.linkId,
      from,
      to,
      color: latencyEdgeColor(l.rollingRttMs, l.rollingLossPct),
      strokeWidth: latencyStrokeWidth(l.rollingRttMs),
    });
  }
  return out.sort((a, b) => a.id.localeCompare(b.id));
}
