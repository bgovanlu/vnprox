// T-2605's post-apply preview mode: turns a projected topology plus its change
// list into the scene the canvas paints and the marks it rings entities with.
//
// A pure mapping function, kept out of canvasDraw.ts so it is unit-testable
// without a real CanvasRenderingContext2D — the same split diffOverlay.ts and
// every other overlay already establish.
//
// WHY REMOVED ENTITIES ARE ADDED BACK. The projected topology is the post-apply
// map, so a deleted bridge is simply not in it. Rendering that directly would
// show the deletion as an absence — and an absence is exactly what an operator
// scanning a map does not notice. So this file re-introduces every removed
// entity from the LIVE map, marked `removed`, and keeps the edges that reach it
// when both ends are still on screen. The preview then shows a bridge crossed
// out with its ports still drawn to it, which is what "this is what you are
// deleting" has to look like.
//
// It reuses diffOverlay.ts's DiffMark rather than defining a parallel mark
// type: the canvas already paints that shape, with the same added/removed/
// modified vocabulary, and a second mark type would mean a second painter.
// `attributed` is true on every preview mark — this changeset is precisely what
// would cause each of them.

import type { ChangesetPreviewResponse, PreviewChange } from "../api/changesetPreview";
import type { TopologyEdge, TopologyNode, TopologyResponse } from "../api/types";
import type { DiffMark } from "./diffOverlay";

/** The scene to render in preview mode, plus its marks. */
export interface PreviewScene {
  nodes: TopologyNode[];
  edges: TopologyEdge[];
  marks: DiffMark[];
  /** Marks whose entity is not on the rendered scene at all (an entity behind
   * an active layer/VLAN filter). Counted rather than dropped, so "nothing is
   * highlighted" never reads the same as "nothing changes". */
  offMap: DiffMark[];
  addedCount: number;
  removedCount: number;
  modifiedCount: number;
}

function labelFor(change: PreviewChange): string {
  const what = change.name ?? change.ref;
  switch (change.change) {
    case "added":
      return `${what} would be added`;
    case "removed":
      return `${what} would be removed`;
    case "modified":
      return `${what} would change`;
  }
}

/** Builds the preview scene from the live topology (for the entities the
 * changeset removes) and the preview response (for everything else).
 *
 * `live` may be undefined — the map has not loaded yet, or the caller has none.
 * Removed entities are then only counted, never drawn, because there is nowhere
 * to take their node from; they still appear in `offMap` so the status line can
 * report them.
 *
 * Deterministic: every list is built by walking the server's already-ordered
 * arrays. Nothing here iterates a Map or Set into its output. */
export function buildPreviewScene(
  preview: ChangesetPreviewResponse | undefined,
  live: TopologyResponse | undefined,
): PreviewScene {
  const scene: PreviewScene = {
    nodes: [],
    edges: [],
    marks: [],
    offMap: [],
    addedCount: 0,
    removedCount: 0,
    modifiedCount: 0,
  };
  if (!preview) return scene;

  scene.nodes = [...preview.topology.nodes];
  scene.edges = [...preview.topology.edges];

  const removedRefs = new Set(
    preview.changes.filter((c) => c.change === "removed").map((c) => c.ref),
  );
  if (removedRefs.size > 0 && live) {
    const alreadyOnScene = new Set(scene.nodes.map((n) => n.id));
    for (const node of live.nodes) {
      if (removedRefs.has(node.id) && !alreadyOnScene.has(node.id)) {
        scene.nodes.push(node);
        alreadyOnScene.add(node.id);
      }
    }
    // Keep the live edges that touch a removed entity, so it is drawn attached
    // to whatever it currently carries rather than floating alone. Both
    // endpoints must be on the scene, or the edge would dangle.
    for (const edge of live.edges) {
      if (!removedRefs.has(edge.from) && !removedRefs.has(edge.to)) continue;
      if (!alreadyOnScene.has(edge.from) || !alreadyOnScene.has(edge.to)) continue;
      scene.edges.push(edge);
    }
  }

  const onScene = new Set(scene.nodes.map((n) => n.id));
  for (const change of preview.changes) {
    const mark: DiffMark = {
      nodeId: change.ref,
      change: change.change,
      attributed: true,
      label: labelFor(change),
    };
    switch (change.change) {
      case "added":
        scene.addedCount += 1;
        break;
      case "removed":
        scene.removedCount += 1;
        break;
      case "modified":
        scene.modifiedCount += 1;
        break;
    }
    if (onScene.has(change.ref)) {
      scene.marks.push(mark);
    } else {
      scene.offMap.push(mark);
    }
  }
  return scene;
}

/** A short sentence for the preview banner, so the map SAYS what it is showing
 * rather than leaving a ring of colored halos unexplained. */
export function summarizePreviewScene(scene: PreviewScene): string {
  const total = scene.addedCount + scene.removedCount + scene.modifiedCount;
  if (total === 0) return "This changeset does not change the map.";
  const parts: string[] = [];
  if (scene.addedCount > 0) parts.push(`${String(scene.addedCount)} added`);
  if (scene.removedCount > 0) parts.push(`${String(scene.removedCount)} removed`);
  if (scene.modifiedCount > 0) parts.push(`${String(scene.modifiedCount)} modified`);
  if (scene.offMap.length > 0) parts.push(`${String(scene.offMap.length)} not on the current map`);
  return `${parts.join(" · ")}.`;
}

/** The disclosure line for the ops the projection could not express. Returns ""
 * when there are none — but callers must render it whenever it is non-empty:
 * this is the preview admitting what it does not know, which is the whole
 * reason the server sends the list instead of dropping those ops. */
export function summarizeUnprojectable(preview: ChangesetPreviewResponse | undefined): string {
  const ops = preview?.unprojectable ?? [];
  if (ops.length === 0) return "";
  const noun = ops.length === 1 ? "op is" : "ops are";
  return `${String(ops.length)} ${noun} not shown here: ${ops.map((o) => o.op).join(", ")}.`;
}
