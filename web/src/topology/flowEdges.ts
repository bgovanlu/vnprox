// SPDX-License-Identifier: Apache-2.0

// T-1003's map-painting glue: aggregates a set of ingested flow.Records
// (docs/api.md's Flows section, T-1002) into weighted, directed
// conversation edges the v2 canvas renderer overlays on top of the normal
// topology edges — the "Flows" layer's data half, kept pure/framework-free
// exactly like toFlowElements.ts's own projection so it's directly
// Vitest-table-testable with no canvas/React involved. TopologyPage feeds
// this the live flow buffer (flows/flowsQueries.ts + flows/reducer.ts) and
// hands the result to TopologyCanvasV2/canvasDraw.ts's drawFlowOverlay.
//
// A record only ever contributes an edge when BOTH srcRef and dstRef
// resolved (docs/api.md: "never guessed" — a record with either endpoint
// unresolved carries no paintable identity on the map) and they resolved
// to two *different* entities: a same-ref conversation (both endpoints
// within the same bridge/subnet) has no distinct second endpoint to draw a
// line to, so it never produces an edge here — it's still fully visible in
// the Flow Explorer, just not as a map overlay edge.
import type { FlowRecord } from "../api/types";

export interface FlowEdge {
  id: string;
  from: string;
  to: string;
  bytes: number;
  packets: number;
  recordCount: number;
  /** Most recent record's `at` (unix seconds) contributing to this edge. */
  lastAt: number;
  /** bytes / windowSeconds — the "current bytes/sec" the card's edge-
   * thickness requirement is keyed on. */
  bytesPerSec: number;
}

export interface ComputeFlowEdgesParams {
  records: readonly FlowRecord[];
  /** Only records at/after `now - windowSeconds` count as part of an
   * "active" conversation. Default 60s: long enough that a normal
   * multi-second WS batch cadence still shows a steady edge, short enough
   * that a conversation that has genuinely stopped fades from the map
   * within about a minute rather than lingering indefinitely. */
  windowSeconds?: number;
  /** Injectable "now" (unix seconds) for deterministic tests; defaults to
   * the real wall clock. */
  now?: number;
  /** Restrict edges to endpoints currently present on the canvas (both
   * `from` and `to` must be members) — undefined disables the check. A
   * flow record can resolve against an entity that's collapsed/filtered
   * out of the current view (VLAN filter, layer toggle, T-902 LOD
   * capsule/bundle), and painting an edge to a node id the canvas isn't
   * currently drawing would either silently vanish or, worse, resolve to
   * the wrong on-screen point. */
  nodeIds?: ReadonlySet<string>;
}

// Exported (T-1007) so HistoryTimeline.tsx's historical flow query window
// can mirror this exact "active conversation" window when anchored at a
// scrubbed instant instead of real now — the same constant, not a second
// one that could drift out of sync with it.
export const DEFAULT_WINDOW_SECONDS = 60;

export function flowEdgeId(from: string, to: string): string {
  return `${from}=>${to}::flow`;
}

/** Aggregates `records` into the currently-active per-(srcRef,dstRef)
 * conversation edge set. Deterministic: same input (including `now`)
 * always produces the same output, sorted by id, regardless of the
 * records' original order. Returns `[]` for "no active flows" (the empty
 * case the map's empty-state hint and this module's own test both key
 * on) — never throws on malformed input, since a FlowRecord always comes
 * from the typed GET /flows/`flow.batch` decode already validated
 * upstream. */
export function computeFlowEdges(params: ComputeFlowEdgesParams): FlowEdge[] {
  const { records, windowSeconds = DEFAULT_WINDOW_SECONDS, nodeIds } = params;
  const now = params.now ?? Math.floor(Date.now() / 1000);
  const cutoff = now - windowSeconds;

  const byPair = new Map<string, Omit<FlowEdge, "bytesPerSec">>();
  for (const r of records) {
    if (!r.srcRef || !r.dstRef || r.srcRef === r.dstRef) continue;
    if (r.at < cutoff) continue;
    if (nodeIds && (!nodeIds.has(r.srcRef) || !nodeIds.has(r.dstRef))) continue;

    const id = flowEdgeId(r.srcRef, r.dstRef);
    const existing = byPair.get(id);
    if (existing) {
      existing.bytes += r.bytes;
      existing.packets += r.packets;
      existing.recordCount += 1;
      existing.lastAt = Math.max(existing.lastAt, r.at);
    } else {
      byPair.set(id, {
        id,
        from: r.srcRef,
        to: r.dstRef,
        bytes: r.bytes,
        packets: r.packets,
        recordCount: 1,
        lastAt: r.at,
      });
    }
  }

  return Array.from(byPair.values())
    .map((e) => ({ ...e, bytesPerSec: windowSeconds > 0 ? e.bytes / windowSeconds : e.bytes }))
    .sort((a, b) => a.id.localeCompare(b.id));
}

const MIN_FLOW_STROKE_WIDTH = 1.5;
const MAX_FLOW_STROKE_WIDTH = 5;
// bytesPerSec at/above this reads as "fully saturated" for stroke-width
// purposes — 1 MB/s, comfortably above a chatty single TCP conversation
// but well below what would make every edge look maxed out.
const SATURATION_BYTES_PER_SEC = 1_000_000;

/** Maps a conversation's bytes/sec to a stroke width — log-scaled (network
 * traffic spans many orders of magnitude, unlike trafficMode's bounded 0-
 * 100% utilization) and clamped at both ends, mirroring
 * trafficMode.ts's utilizationStrokeWidth shape/spirit for a visually
 * consistent "thicker = busier" read without literally reusing its linear
 * 0-100% formula (which doesn't fit an unbounded bytes/sec input). */
export function flowEdgeStrokeWidth(bytesPerSec: number): number {
  if (!Number.isFinite(bytesPerSec) || bytesPerSec <= 0) return MIN_FLOW_STROKE_WIDTH;
  const t = Math.log10(bytesPerSec + 1) / Math.log10(SATURATION_BYTES_PER_SEC + 1);
  const clamped = Math.max(0, Math.min(1, t));
  return MIN_FLOW_STROKE_WIDTH + clamped * (MAX_FLOW_STROKE_WIDTH - MIN_FLOW_STROKE_WIDTH);
}
