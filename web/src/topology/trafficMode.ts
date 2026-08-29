// SPDX-License-Identifier: Apache-2.0

// Traffic paint mode: edge thickness/heat by utilization % of link speed
// (docs/features/monitoring.md §1: "map integration: optional 'traffic'
// paint mode — edge thickness/heat by current utilization %"). Pure
// mapping functions, kept separate from EntityEdge.tsx's rendering so the
// heat/thickness curve is unit-testable without React/xyflow.

/** Client-side mirror of internal/metrics.UtilizationPct (Go): expresses
 * bps (bits/sec, one direction) as a percentage of speedMbps
 * (megabits/sec). Needed because the `metrics.sample` WS push carries only
 * `{ref, at, rates}` (docs/api.md) — no speed/utilization — so a live
 * traffic-mode update recomputes utilization client-side from the
 * link speed the last GET /metrics/live fetch reported, exactly the way
 * the daemon computes it from the same two numbers. undefined speedMbps
 * (not yet known — before the first /metrics/live response for this ref)
 * reports 0, matching the daemon's "unknown link speed -> no heat data"
 * treatment rather than an error/NaN. */
export function computeUtilizationPct(bps: number, speedMbps: number | undefined): number {
  if (speedMbps === undefined || speedMbps <= 0 || bps <= 0) return 0;
  return (bps / (speedMbps * 1_000_000)) * 100;
}

/** The severity a utilization reading crosses into, as a design-language
 * token NAME rather than a colour — the two renderers consume colour
 * differently (v1's `EntityEdge` sets an SVG `stroke`, which can take
 * `var(--color-*)`; v2's canvas needs a resolved value) and neither should
 * be handed a literal.
 *
 * T-4303 asked for this ramp to be made monotonic. Measuring it is what
 * changed the answer.
 *
 * The old scale was five hues — slate, blue, green, amber, red — whose
 * OKLCH lightness ran 0.711, 0.754, 0.723, 0.769, 0.637. Up, down, up,
 * down. Utilization is a quantity, and an encoding of a quantity has to be
 * orderable by eye or it is a lookup table rendered in colour.
 *
 * But a monotonic COLOUR ramp turns out to be the wrong repair, for two
 * reasons found by trying to build one.
 *
 * First, there is nowhere on the hue circle to put it. A ramp from a cool
 * neutral to red must pass through either green and amber — which are `ok`
 * and `degraded` — or through violet and magenta, where the derived
 * midpoints landed 3deg from `BLAST_RADIUS_COLOR` and 5deg from
 * `SIM_STROKE.indeterminate`. Holding the hue constant instead avoids every
 * collision and tints every IDLE link faintly red, which is worse: idle is
 * the majority state on any healthy map.
 *
 * Second, and decisively: `utilizationStrokeWidth` below ALREADY encodes
 * this quantity, continuously and monotonically, 1.5px to 6px linear in
 * percent. Width is a better channel for magnitude than hue is, it was
 * already correct, and the colour ramp was a redundant second encoding of
 * the same number — the one that had no order.
 *
 * So width keeps the quantity and colour stops competing for it. Colour
 * now says only which severity band the link has crossed into, which is
 * what the status scale is for, and it uses that scale rather than a
 * palette invented here.
 *
 * The 75 boundary is the one the previous scale already drew (its
 * busy/saturated split); 90 is the one addition. Neither is invented from
 * scratch and neither claims to be an operational standard. */
export type UtilizationTone = "outline" | "status-degraded" | "status-critical";

export function utilizationTone(utilizationPct: number): UtilizationTone {
  const pct = Number.isFinite(utilizationPct) ? Math.max(0, utilizationPct) : 0;
  if (pct > 90) return "status-critical";
  if (pct > 75) return "status-degraded";
  return "outline";
}

const MIN_STROKE_WIDTH = 1.5;
const MAX_STROKE_WIDTH = 6;

/** Maps utilization to an edge stroke width: idle edges stay at the
 * existing default (1.5, matching EntityEdge's non-traffic-mode width) and
 * a fully-saturated link renders at MAX_STROKE_WIDTH, linear in between,
 * clamped at both ends (a >100% reading — bursty/measurement noise, see
 * UtilizationPct — still renders at the cap, not thicker still). */
export function utilizationStrokeWidth(utilizationPct: number): number {
  const pct = Number.isFinite(utilizationPct) ? Math.max(0, Math.min(100, utilizationPct)) : 0;
  return MIN_STROKE_WIDTH + (pct / 100) * (MAX_STROKE_WIDTH - MIN_STROKE_WIDTH);
}

/** Combines the two mappings above into the style traffic mode overrides
 * an edge's normal status-driven stroke with — the single call site
 * EntityEdge.tsx needs. undefined utilizationPct (no live data yet for
 * this ref) reports the idle look, not an error/blank edge, so a map
 * freshly switched into traffic mode before the first WS tick arrives
 * still renders sensibly. */
export function trafficEdgeStyle(utilizationPct: number | undefined): {
  tone: UtilizationTone;
  strokeWidth: number;
} {
  const pct = utilizationPct ?? 0;
  return { tone: utilizationTone(pct), strokeWidth: utilizationStrokeWidth(pct) };
}

/** The CSS custom property a tone names. Used by the DOM renderer, which can
 * put `var(...)` straight into an SVG `stroke`; the canvas renderer resolves
 * the same token through `canvasPalette` instead, because `ctx.strokeStyle`
 * cannot take a `var()`. Two consumers, one token name, no third copy of the
 * colours. */
export function toneVar(tone: UtilizationTone): string {
  return `var(--color-${tone})`;
}

/** An edge connects two inventory entities, but utilization is a property
 * of the physical/aggregate link, not every entity kind — a bridge or VLAN
 * sub-interface's own utilization is less meaningful for "which wire is
 * hot" than the Bond/PhysNic at the other end. This ranks kinds so the
 * more link-like endpoint is preferred when both ends happen to carry
 * live data (lower rank = preferred). */
function linkKindRank(kind: string | undefined): number {
  switch (kind) {
    case "bond":
      return 0;
    case "physnic":
      return 1;
    case "vlan":
      return 2;
    case "bridge":
      return 3;
    default:
      return 4;
  }
}

/** Picks which of an edge's two endpoint refs to read utilization from —
 * whichever is more "link-like" (see linkKindRank) among the ones
 * utilizationByRef actually has live data for; undefined if neither does.
 * kindOf resolves a ref to its inventory kind (e.g. via a node-id -> kind
 * lookup already built for the topology projection). */
export function resolveEdgeUtilizationRef(
  fromRef: string,
  toRef: string,
  kindOf: (ref: string) => string | undefined,
  utilizationByRef: ReadonlyMap<string, number>,
): string | undefined {
  const candidates = [fromRef, toRef].filter((ref) => utilizationByRef.has(ref));
  if (candidates.length === 0) return undefined;
  candidates.sort((a, b) => linkKindRank(kindOf(a)) - linkKindRank(kindOf(b)));
  return candidates[0];
}
