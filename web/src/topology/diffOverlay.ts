// T-2704's map diff overlay: resolves a point-in-time topology diff to the
// per-map-node marks the canvas paints for a selected range.
//
// A pure mapping function, kept out of canvasDraw.ts so it is unit-testable
// without a real CanvasRenderingContext2D (jsdom has none) — the same split
// mtuOverlay.ts/latencyMode.ts already establish for every other overlay.
//
// THE ATTRIBUTION IS THE POINT. A mark is not just "this changed": it is
// "this changed, and vnprox did / did not do it". An unattributed change is
// an out-of-band change, so it gets its own visually distinct treatment
// rather than being folded in with the ones a changeset explains. Collapsing
// the two would throw away the only thing the overlay is for.

import type { TopologyDiffResponse, TopologyEntityDiff } from "../api/topologyDiff";
import { allTopologyDiffRows } from "../api/topologyDiff";

export type DiffMarkChange = "added" | "removed" | "modified";

/** One map node's diff mark. `nodeId` is the entity's own `inventory.Ref`
 * string, which is already the map's node-id convention
 * (docs/features/topology.md §3), so no id resolution step is needed the way
 * `computeMTUOverlayEdges`' `nodeIdForName` requires for node-name-keyed
 * link endpoints. */
export interface DiffMark {
  nodeId: string;
  change: DiffMarkChange;
  /** False when no changeset explains this difference — an out-of-band
   * change. Drawn distinctly; see this file's doc comment. */
  attributed: boolean;
  /** The explaining changeset, when there is one. */
  changesetId?: string;
  /** Short human label for the legend/tooltip. */
  label: string;
}

/** A removed entity is no longer on the map at all, so its mark can never be
 * painted onto a node — but it still has to be COUNTED, or a range whose only
 * difference was a deletion would render as "no changes". Callers surface
 * this alongside the painted marks. */
export interface DiffOverlay {
  marks: DiffMark[];
  /** Marks whose entity is not currently on the map (removals, and anything
   * on a node the current filters hide). */
  offMap: DiffMark[];
  attributedCount: number;
  unattributedCount: number;
}

function labelFor(row: TopologyEntityDiff): string {
  const what = row.name ?? row.ref;
  const verb = row.change === "added" ? "added" : row.change === "removed" ? "removed" : "changed";
  if (row.attribution.attributed) {
    const who = row.attribution.actor ?? "vnprox";
    return `${what} ${verb} by ${who}`;
  }
  return `${what} ${verb} outside vnprox`;
}

/** Resolves a diff to its map marks.
 *
 * `isOnMap` reports whether an entity ref is currently rendered — a mark for
 * something the map is not showing (a deleted bridge, or one hidden by the
 * active layer/VLAN filters) goes to `offMap` instead of being silently
 * dropped, because "nothing is highlighted" and "nothing changed" must not
 * look the same.
 *
 * Deterministic: rows arrive already ref-ordered from the server and this
 * function never iterates a map into its output. */
export function computeDiffOverlay(
  diff: TopologyDiffResponse | undefined,
  isOnMap: (ref: string) => boolean,
): DiffOverlay {
  const out: DiffOverlay = { marks: [], offMap: [], attributedCount: 0, unattributedCount: 0 };
  if (!diff) return out;

  for (const row of allTopologyDiffRows(diff)) {
    const mark: DiffMark = {
      nodeId: row.ref,
      change: row.change,
      attributed: row.attribution.attributed,
      changesetId: row.attribution.changesetId,
      label: labelFor(row),
    };
    if (mark.attributed) {
      out.attributedCount += 1;
    } else {
      out.unattributedCount += 1;
    }
    if (isOnMap(row.ref)) {
      out.marks.push(mark);
    } else {
      out.offMap.push(mark);
    }
  }
  return out;
}

/** The stroke color for a mark. Unattributed changes get one shared, alarming
 * color regardless of what kind of change they are: "vnprox did not do this"
 * is the distinction that matters, and it must not be readable only by
 * squinting at three shades of the same hue. */
export function diffMarkColor(mark: Pick<DiffMark, "change" | "attributed">): string {
  if (!mark.attributed) return "#dc2626"; // red-600
  switch (mark.change) {
    case "added":
      return "#16a34a"; // green-600
    case "removed":
      return "#a855f7"; // purple-500
    case "modified":
      return "#2563eb"; // blue-600
  }
}

/** The one-character glyph drawn in a mark's corner badge. */
export function diffMarkGlyph(change: DiffMarkChange): string {
  switch (change) {
    case "added":
      return "+";
    case "removed":
      return "−"; // minus sign, not a hyphen
    case "modified":
      return "~";
  }
}

/** A short sentence for the overlay's own status line, so the map states what
 * it is showing rather than leaving a ring of colored halos unexplained. */
export function summarizeDiffOverlay(overlay: DiffOverlay): string {
  const total = overlay.attributedCount + overlay.unattributedCount;
  if (total === 0) return "No differences in this range.";
  const parts = [`${String(total)} ${total === 1 ? "difference" : "differences"}`];
  if (overlay.unattributedCount > 0) {
    parts.push(`${String(overlay.unattributedCount)} made outside vnprox`);
  }
  if (overlay.offMap.length > 0) {
    parts.push(`${String(overlay.offMap.length)} not on the current map`);
  }
  return `${parts.join(" · ")}.`;
}
