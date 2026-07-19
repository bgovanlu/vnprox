// Verified-MTU map-edge annotation (docs/features/monitoring.md §6, T-1306):
// a second, independent overlay from latencyMode.ts's "Latency" heatmap
// layer (T-1303) — this one renders each probed link's measured path MTU
// as its own badge, distinct from wherever that link's *configured* MTU is
// already shown elsewhere (a bridge/SDN-zone editor's own MTU field), never
// merging the two. A pure mapping function, kept separate from
// canvasDraw.ts's drawMTUOverlay so the resolution logic is unit-testable
// without a real CanvasRenderingContext2D (jsdom has none — see
// canvasDraw.test.ts's own doc comment), mirroring latencyMode.ts's
// computeLatencyOverlayEdges precedent exactly.

import type { MTUProbeResult } from "../api/types";

/** One GET /mtuprobe/results reading, resolved to the canvas-edge badge
 * canvasDraw.ts's drawMTUOverlay draws — that function itself has no
 * opinion on which links have a reading, only on drawing the resulting
 * label at the edge midpoint (the same division of labor
 * drawLatencyOverlay/LatencyOverlayEdge already establish for the Latency
 * layer). */
export interface MTUOverlayBadge {
  id: string;
  from: string;
  to: string;
  /** The verified (measured) MTU value this badge displays — always the
   * probed reading, never a configured value; see this file's doc comment. */
  mtu: number;
  at: number;
}

/** Resolves results to their from/to on-canvas node ids and formats each
 * as a badge — a link with no probe result yet simply isn't present in
 * `results` at all (Service.Results' own "no synthesized zero/stale entry"
 * contract), so it produces no badge here either, never a placeholder
 * (docs/features/monitoring.md §6's explicit "no verified badge" case).
 * `nodeIdForName` mirrors computeLatencyOverlayEdges' identical seam:
 * `fromNode`/`toNode` are PVE node *names*, not inventory Refs, so they
 * need resolving against a node-name -> map-node-id lookup before they're
 * paintable (undefined when that node isn't currently rendered). */
export function computeMTUOverlayEdges(
  results: readonly MTUProbeResult[],
  nodeIdForName: (nodeName: string) => string | undefined,
): MTUOverlayBadge[] {
  const out: MTUOverlayBadge[] = [];
  for (const r of results) {
    const from = nodeIdForName(r.fromNode);
    const to = nodeIdForName(r.toNode);
    if (!from || !to || from === to) continue;
    out.push({ id: r.linkId, from, to, mtu: r.mtu, at: r.at });
  }
  return out.sort((a, b) => a.id.localeCompare(b.id));
}

/** Formats a badge's label text — "MTU 1450", always the verified reading,
 * always visually distinct (a separate word/element) from wherever a
 * configured MTU value is shown, never a bare number that could be
 * mistaken for one. */
export function formatMTUBadgeLabel(mtu: number): string {
  return `MTU ${String(mtu)}`;
}
