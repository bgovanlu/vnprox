// T-2806's on-canvas half of the map annotation layer: labelled regions
// drawn behind the graph, and a marker per annotated entity carrying the
// note text.
//
// A DOM overlay rather than canvas painting, for the same reason
// TopologyA11yLayer is one: text an operator typed must be selectable,
// readable by a screen reader, and — the point of AC6 — rendered through
// React's escaping, which canvas fillText would sidestep by making the
// question meaningless while leaving the export paths as the only escaped
// ones. Positioning follows TopologyA11yLayer exactly: children are laid
// out in GRAPH space and pan/zoom is one CSS transform on the container,
// so a pan frame updates one element's transform rather than N nodes'.
//
// Everything here is presentational and props-driven: the layer never
// fetches, so a view switch or a layout save cannot disturb it — the
// regions it renders come from their own query cache
// (annotationsQueries.ts's MAP_REGIONS_QUERY_KEY), which no layout write
// touches.
import { useMemo } from "react";
import type { Viewport } from "./canvasScene";
import type { Annotation, MapRegion } from "../api/types";

/** One annotated entity's on-canvas position, in graph space. */
export interface AnnotationAnchor {
  ref: string;
  x: number;
  y: number;
}

export interface AnnotationLayerProps {
  /** Live (non-expired) regions, straight from GET /map-regions. */
  regions: readonly MapRegion[];
  /** Live notes, straight from GET /annotations. */
  notes: readonly Annotation[];
  /** Where each annotated entity sits on the canvas, in graph space.
   * A note whose ref has no anchor (the entity is not currently rendered,
   * or is gone entirely) is still listed — see orphanNotes below. */
  anchors: readonly AnnotationAnchor[];
  /** Current pan/zoom, applied as one container transform. */
  viewport: Viewport;
}

/** Region fill/stroke per palette key. Kept deliberately small: `color` is
 * a client-chosen key, and an unrecognized one falls back to the default
 * rather than being interpolated into a style string. */
const REGION_PALETTE: Record<string, string> = {
  amber: "border-amber-400/70 bg-amber-200/20 text-amber-800 dark:text-amber-200",
  rose: "border-rose-400/70 bg-rose-200/20 text-rose-800 dark:text-rose-200",
  emerald: "border-emerald-400/70 bg-emerald-200/20 text-emerald-800 dark:text-emerald-200",
};
const REGION_DEFAULT = "border-violet-400/70 bg-violet-200/20 text-violet-800 dark:text-violet-200";

function regionClass(color: string): string {
  return REGION_PALETTE[color] ?? REGION_DEFAULT;
}

/** Groups notes by the ref they are pinned to. */
function groupByRef(notes: readonly Annotation[]): Map<string, Annotation[]> {
  const out = new Map<string, Annotation[]>();
  for (const note of notes) {
    const list = out.get(note.ref);
    if (list) list.push(note);
    else out.set(note.ref, [note]);
  }
  return out;
}

export function AnnotationLayer({ regions, notes, anchors, viewport }: AnnotationLayerProps) {
  const byRef = useMemo(() => groupByRef(notes), [notes]);

  // Notes whose entity is not on the canvas at all. They are rendered in a
  // fixed corner list rather than dropped: an orphaned note is frequently
  // the only record of why the entity was removed (T-2806 AC2), so it must
  // stay visible once its anchor is gone.
  const orphanNotes = useMemo(() => {
    const anchored = new Set(anchors.map((a) => a.ref));
    return notes.filter((n) => !anchored.has(n.ref));
  }, [notes, anchors]);

  return (
    <div className="pointer-events-none absolute inset-0 overflow-hidden" data-testid="annotation-layer">
      <div
        className="pointer-events-none absolute left-0 top-0"
        style={{
          transform: `translate(${String(viewport.x)}px, ${String(viewport.y)}px) scale(${String(viewport.zoom)})`,
          transformOrigin: "0 0",
        }}
      >
        {regions.map((region) => (
          <div
            key={region.id}
            data-testid="map-region"
            className={`absolute rounded border-2 border-dashed ${regionClass(region.color)}`}
            style={{
              left: `${String(region.x)}px`,
              top: `${String(region.y)}px`,
              width: `${String(region.w)}px`,
              height: `${String(region.h)}px`,
            }}
          >
            <span className="absolute left-1 top-1 max-w-full truncate rounded bg-white/80 px-1 text-[10px] font-medium dark:bg-slate-900/80">
              {region.label}
            </span>
          </div>
        ))}
        {anchors.map((anchor) => {
          const anchored = byRef.get(anchor.ref);
          if (anchored === undefined || anchored.length === 0) return null;
          return (
            <div
              key={anchor.ref}
              data-testid="annotation-marker"
              data-entity-ref={anchor.ref}
              className="absolute max-w-[200px] rounded border border-amber-400 bg-amber-50 p-1 text-[10px] text-slate-800 shadow-sm dark:border-amber-500 dark:bg-amber-950 dark:text-amber-100"
              style={{ left: `${String(anchor.x)}px`, top: `${String(anchor.y)}px` }}
            >
              {anchored.map((note) => (
                <p key={note.id} className="truncate" title={note.content}>
                  {note.content}
                </p>
              ))}
            </div>
          );
        })}
      </div>
      {orphanNotes.length > 0 && (
        <div
          data-testid="orphan-notes"
          className="absolute bottom-2 left-2 max-w-[280px] rounded border border-slate-300 bg-white/95 p-2 text-[10px] text-slate-700 shadow dark:border-slate-600 dark:bg-slate-900/95 dark:text-slate-200"
        >
          <p className="mb-1 font-medium">Notes on entities that no longer exist</p>
          <ul className="space-y-1">
            {orphanNotes.map((note) => (
              <li key={note.id}>
                <span className="text-slate-600 dark:text-slate-400">{note.ref}</span> {note.content}
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
