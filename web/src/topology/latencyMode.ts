// SPDX-License-Identifier: Apache-2.0

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

/** Historical note, kept because the reasoning it records was overturned
 * twice: this was a four-stop violet/fuchsia/pink scale chosen so no hex was
 * shared with trafficMode.ts's HEAT_STOPS or canvasDraw.ts's cyan flow
 * literals. T-4303 replaced the ramp with status-token NAMES (see
 * latencyTone below), and T-4306 deleted the flow literals it was avoiding —
 * a flow edge resolves `--color-status-info` now. Avoiding every other
 * colour on the map was the right contract for a private ramp and is the
 * wrong one for a scale that should SHARE the status vocabulary, since both
 * answer the same question. Stops are keyed on rttMs as a fraction of
 * LATENCY_WARN_MS (25%/
 * 62.5%/100%), not fixed ms breakpoints, so the scale re-centers cleanly if
 * the server threshold constant is ever tuned without this file drifting
 * out of sync (a future improvement could fetch the live threshold; today
 * both sides simply agree on the same documented constant, mirroring how
 * trafficMode.ts's own stops are this module's own reasoned choice, not a
 * server-fetched value either). */
/** The severity band a latency reading falls into, as a design-token NAME.
 *
 * T-4303, applied a second time. This module's ramp was violet-300 ->
 * violet-400 -> fuchsia-600 -> pink-800, and measuring it first is what the
 * card asked for, because it is NOT the same defect trafficMode had:
 *
 *   - It was already monotonic in lightness (0.811, 0.709, 0.591, 0.459),
 *     steadily darkening as latency rose. The rainbow problem was not here.
 *   - It failed on CONTRAST instead, and it failed at both ends, because one
 *     ramp served both themes. `excellent` measured 1.78:1 against the light
 *     page — invisible on white. `degraded` measured 2.26:1 against the dark
 *     page, so the state most needing to be seen was the least visible in
 *     dark mode. A ramp that does not re-point per theme cannot clear a floor
 *     at both ends, whatever its hues.
 *
 * The repair is the one trafficMode arrived at. `latencyStrokeWidth` below
 * already encodes the magnitude, linearly and monotonically, so colour was a
 * redundant second encoding of the same number; it now names a severity band
 * instead, from the status scale, which re-points per theme and therefore
 * clears its floor at both ends by construction.
 *
 * The bands are this module's existing thresholds, not new ones: everything
 * below 0.625x the warn threshold reads as fine, up to the warn threshold is
 * `degraded`, over it is `critical`. Loss over its own threshold maps to the
 * same top band, preserving the property the old DEGRADED_LOSS_COLOR comment
 * established — "this link has a problem" looks the same regardless of which
 * threshold tripped. */
export type LatencyTone = "outline" | "status-degraded" | "status-critical";

export function latencyTone(rollingRttMs: number): LatencyTone {
  const ms = Number.isFinite(rollingRttMs) ? Math.max(0, rollingRttMs) : 0;
  if (ms > LATENCY_WARN_MS) return "status-critical";
  if (ms > LATENCY_WARN_MS * 0.625) return "status-degraded";
  return "outline";
}

/** Combines rolling RTT and rolling loss% into one severity band: a link over
 * the loss threshold always reads as critical regardless of its RTT (a
 * lossy-but-fast path is not "good" just because the packets that do
 * arrive are quick), otherwise falls back to the pure RTT bands. */
export function latencyEdgeTone(rollingRttMs: number, rollingLossPct: number): LatencyTone {
  const loss = Number.isFinite(rollingLossPct) ? rollingLossPct : 0;
  if (loss > LOSS_WARN_PCT) return "status-critical";
  return latencyTone(rollingRttMs);
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
  tone: LatencyTone;
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
      tone: latencyEdgeTone(l.rollingRttMs, l.rollingLossPct),
      strokeWidth: latencyStrokeWidth(l.rollingRttMs),
    });
  }
  return out.sort((a, b) => a.id.localeCompare(b.id));
}
