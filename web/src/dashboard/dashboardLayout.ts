// SPDX-License-Identifier: Apache-2.0

// T-3911's composable dashboard: pure, framework-free logic over a
// DashboardLayoutPayload's ordered tile-ref list — add/remove/reorder,
// resolving a ref against the current built-in catalog and the current
// GET /dashboard/tiles response, and the runtime guard for a fetched
// `layouts` blob's opaque `layout` value. Deliberately no React/TanStack
// import here (DashboardGrid.tsx and dashboardLayoutQueries.ts are the
// framework-facing callers), following web/src/topology/savedViews.ts's
// established "state logic is plain-object-testable" convention.
import type { DashboardLayoutPayload, DashboardTile, DashboardTileRef } from "../api/types";
import { BUILTIN_TILES, findBuiltinTile, type BuiltinTileDef } from "./tileRegistry";

/** The `"plugin:"` id prefix a DashboardTileRef uses to disambiguate a
 * plugin tile's ref from a built-in one, per docs/api.md's "Dashboard tile
 * layout shape" note. */
const PLUGIN_ID_PREFIX = "plugin:";

export function pluginTileRefId(tileId: string): string {
  return `${PLUGIN_ID_PREFIX}${tileId}`;
}

/** Inverse of pluginTileRefId — the plugin.Tile.ID a plugin ref points at.
 * Returns undefined for a ref that isn't plugin-prefixed (defensive: a
 * caller should already have checked `ref.kind === "plugin"`). */
export function pluginTileIdFromRef(refId: string): string | undefined {
  return refId.startsWith(PLUGIN_ID_PREFIX) ? refId.slice(PLUGIN_ID_PREFIX.length) : undefined;
}

/** The default layout for a user who has never customised their dashboard
 * (GET /layouts/dashboard 404s) — every built-in tile, in the same order
 * DashboardPage.tsx originally hardcoded, no plugin tiles. Never written to
 * the server until the user actually changes something (dashboardLayoutQueries.ts). */
export function defaultDashboardLayout(): DashboardLayoutPayload {
  return {
    kind: "dashboard-tiles",
    tiles: BUILTIN_TILES.map((t) => ({ id: t.id, kind: "builtin" })),
  };
}

function isDashboardTileRef(value: unknown): value is DashboardTileRef {
  if (typeof value !== "object" || value === null) return false;
  const v = value as Record<string, unknown>;
  return typeof v.id === "string" && (v.kind === "builtin" || v.kind === "plugin");
}

/** Runtime guard for a `layouts` list item's opaque `layout` value: true
 * iff it looks like T-3911's reserved "dashboard" blob, mirroring
 * savedViews.ts's isSavedViewPayload exactly (same idiom, different
 * discriminator). */
export function isDashboardLayoutPayload(value: unknown): value is DashboardLayoutPayload {
  if (typeof value !== "object" || value === null) return false;
  const v = value as Record<string, unknown>;
  if (v.kind !== "dashboard-tiles") return false;
  if (!Array.isArray(v.tiles)) return false;
  return v.tiles.every(isDashboardTileRef);
}

/** One tile ref resolved against the live built-in catalog and the current
 * plugin-tile list. `"unavailable"` covers every reason a ref might no
 * longer resolve — the plugin was disabled/uninstalled, its provider
 * started erroring (both already dropped server-side before
 * GET /dashboard/tiles' response is built), or a built-in tile id from an
 * older build no longer exists — without the grid needing to distinguish
 * which, since the on-screen treatment (an explicit placeholder, not a
 * crash) is the same either way. */
export type ResolvedTile =
  | { status: "builtin"; ref: DashboardTileRef; def: BuiltinTileDef }
  | { status: "plugin"; ref: DashboardTileRef; tile: DashboardTile }
  | { status: "unavailable"; ref: DashboardTileRef };

export function resolveTiles(refs: DashboardTileRef[], pluginTiles: DashboardTile[]): ResolvedTile[] {
  return refs.map((ref): ResolvedTile => {
    if (ref.kind === "builtin") {
      const def = findBuiltinTile(ref.id);
      return def ? { status: "builtin", ref, def } : { status: "unavailable", ref };
    }
    const tileId = pluginTileIdFromRef(ref.id);
    const tile = tileId === undefined ? undefined : pluginTiles.find((t) => t.id === tileId);
    return tile ? { status: "plugin", ref, tile } : { status: "unavailable", ref };
  });
}

/** One tile not currently on the grid, offered by the "add tile" picker. */
export interface AddableTile {
  ref: DashboardTileRef;
  label: string;
}

export function addableTiles(current: DashboardTileRef[], pluginTiles: DashboardTile[]): AddableTile[] {
  const present = new Set(current.map((t) => t.id));
  const builtinAddable: AddableTile[] = BUILTIN_TILES.filter((b) => !present.has(b.id)).map((b) => ({
    ref: { id: b.id, kind: "builtin" },
    label: b.label,
  }));
  const pluginAddable: AddableTile[] = pluginTiles
    .filter((t) => !present.has(pluginTileRefId(t.id)))
    .map((t) => ({ ref: { id: pluginTileRefId(t.id), kind: "plugin" }, label: t.title }));
  return [...builtinAddable, ...pluginAddable];
}

export function addTile(tiles: DashboardTileRef[], ref: DashboardTileRef): DashboardTileRef[] {
  if (tiles.some((t) => t.id === ref.id)) return tiles;
  return [...tiles, ref];
}

export function removeTile(tiles: DashboardTileRef[], id: string): DashboardTileRef[] {
  return tiles.filter((t) => t.id !== id);
}

/** Swaps a tile earlier/later in display order; a no-op at either end
 * (index 0 has nothing earlier, the last index nothing later) rather than
 * wrapping around, matching how a "move up"/"move down" button reads. */
export function moveTile(tiles: DashboardTileRef[], id: string, direction: "earlier" | "later"): DashboardTileRef[] {
  const idx = tiles.findIndex((t) => t.id === id);
  if (idx === -1) return tiles;
  const targetIdx = direction === "earlier" ? idx - 1 : idx + 1;
  const current = tiles[idx];
  const target = tiles[targetIdx];
  // `target` is undefined both for a genuinely out-of-range targetIdx (the
  // boundary no-op case) and, under noUncheckedIndexedAccess, as far as the
  // type checker can tell for any index — either way, nothing to swap.
  if (current === undefined || target === undefined) return tiles;
  const next = tiles.slice();
  next[idx] = target;
  next[targetIdx] = current;
  return next;
}
