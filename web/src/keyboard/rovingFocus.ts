// SPDX-License-Identifier: Apache-2.0

// T-903: roving arrow-key focus across topology entities. Framework-free
// (no React, no DOM) so the ordering logic itself is exhaustively
// Vitest-able without mounting anything — the same discipline
// switchModel.ts/projection.ts follow. useRovingFocus.ts is the DOM-wiring
// half that reads real element geometry and calls into this module.
export interface PositionedEntity {
  readonly id: string;
  readonly x: number;
  readonly y: number;
}

/** Row-banding tolerance: entities within this many px of each other's `y`
 * are treated as the same visual row for ordering purposes. Real layouts —
 * elk-computed graph positions and CSS-flex/grid faceplate cells alike —
 * rarely align pixel-perfect, so a small tolerance keeps "same row to the
 * eye" entities from splitting into an arbitrary order over a few stray
 * pixels. */
const ROW_BAND_PX = 24;

/**
 * Sorts entities into reading order — top-to-bottom by row band, then
 * left-to-right within each band. This is "visual-adjacency order" per
 * docs/features/topology.md §2 / T-903 AC4: the order roving arrow-key
 * focus advances through.
 */
export function visualAdjacencyOrder(entities: readonly PositionedEntity[]): PositionedEntity[] {
  const sorted = [...entities].sort((a, b) => a.y - b.y || a.x - b.x);
  const rows: PositionedEntity[][] = [];
  for (const entity of sorted) {
    const row = rows[rows.length - 1];
    const rowY = row?.[row.length - 1]?.y;
    if (row && rowY !== undefined && Math.abs(rowY - entity.y) <= ROW_BAND_PX) {
      row.push(entity);
    } else {
      rows.push([entity]);
    }
  }
  for (const row of rows) row.sort((a, b) => a.x - b.x);
  return rows.flat();
}

export type RovingDirection = "up" | "down" | "left" | "right";

export function keyToDirection(key: string): RovingDirection | undefined {
  switch (key) {
    case "ArrowUp":
      return "up";
    case "ArrowDown":
      return "down";
    case "ArrowLeft":
      return "left";
    case "ArrowRight":
      return "right";
    default:
      return undefined;
  }
}

/**
 * Given the currently-focused id (undefined = nothing focused yet) and a
 * direction, returns the next id to focus. Right/Down advance through
 * visual-adjacency order; Left/Up move back through it — a 1-D walk over
 * the 2-D reading order, which is enough to guarantee "advances focus in
 * visual-adjacency order" (T-903 AC4) without the ambiguity a full
 * nearest-neighbor-per-direction model would add for irregular layouts
 * (a faceplate's flex-wrapped port grid, in particular, has no reliable
 * per-row column count to do real 2-D neighbor lookups against). Wraps at
 * either end rather than dead-ending, so repeated presses stay useful.
 * Returns undefined only when there are no entities to focus at all.
 */
export function nextFocusId(
  entities: readonly PositionedEntity[],
  currentId: string | undefined,
  direction: RovingDirection,
): string | undefined {
  const order = visualAdjacencyOrder(entities);
  if (order.length === 0) return undefined;
  const currentIndex = currentId ? order.findIndex((e) => e.id === currentId) : -1;
  const forward = direction === "down" || direction === "right";
  const first = order[0];
  const last = order[order.length - 1];
  if (currentIndex === -1) {
    return (forward ? first : last)?.id;
  }
  const nextIndex = forward ? (currentIndex + 1) % order.length : (currentIndex - 1 + order.length) % order.length;
  return order[nextIndex]?.id;
}
