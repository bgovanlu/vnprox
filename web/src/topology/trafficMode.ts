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

/** A five-stop heat scale from idle (cool slate) to saturated (hot red),
 * matching the codebase's existing status-color vocabulary (EntityEdge's
 * STATUS_STROKE uses the same red/amber/slate palette) so "hot" reads
 * consistently with "down"/"degraded" elsewhere on the map. */
const HEAT_STOPS: readonly { max: number; color: string }[] = [
  { max: 1, color: "#94a3b8" }, // idle: same slate as a normal/ok edge
  { max: 25, color: "#38bdf8" }, // light traffic: cool blue
  { max: 50, color: "#22c55e" }, // moderate: green
  { max: 75, color: "#f59e0b" }, // busy: amber
  { max: Infinity, color: "#ef4444" }, // saturated/over: red
];

/** Maps a utilization percentage (0-100+, see UtilizationPct's doc comment
 * for why it isn't clamped server-side either) to a heat color. Values
 * are looked up by their first matching (inclusive) upper bound. */
export function utilizationColor(utilizationPct: number): string {
  const pct = Number.isFinite(utilizationPct) ? Math.max(0, utilizationPct) : 0;
  for (const stop of HEAT_STOPS) {
    if (pct <= stop.max) return stop.color;
  }
  // Unreachable (the last stop's max is Infinity), but a defensible
  // fallback rather than a possibly-undefined index if HEAT_STOPS is ever
  // edited without keeping that invariant.
  return "#ef4444";
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
export function trafficEdgeStyle(utilizationPct: number | undefined): { stroke: string; strokeWidth: number } {
  const pct = utilizationPct ?? 0;
  return { stroke: utilizationColor(pct), strokeWidth: utilizationStrokeWidth(pct) };
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
