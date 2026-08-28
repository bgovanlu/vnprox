// SPDX-License-Identifier: Apache-2.0

// T-3911: pure logic tests for the composable dashboard's tile-ref
// operations — default layout, add/remove/reorder, resolution against the
// live built-in catalog and plugin-tile list (including the graceful
// "unavailable" case this card explicitly calls out), and the runtime
// guard over a fetched `layouts` blob's opaque JSON.
import { describe, expect, it } from "vitest";
import type { DashboardTile, DashboardTileRef } from "../api/types";
import {
  addTile,
  addableTiles,
  defaultDashboardLayout,
  isDashboardLayoutPayload,
  moveTile,
  pluginTileIdFromRef,
  pluginTileRefId,
  removeTile,
  resolveTiles,
} from "./dashboardLayout";
import { BUILTIN_TILES } from "./tileRegistry";

describe("defaultDashboardLayout", () => {
  it("includes every built-in tile, in the built-in catalog's own order, no plugin tiles", () => {
    const layout = defaultDashboardLayout();
    expect(layout.kind).toBe("dashboard-tiles");
    expect(layout.tiles).toEqual(BUILTIN_TILES.map((t) => ({ id: t.id, kind: "builtin" })));
  });

  it("returns a fresh array each call (no shared mutable reference)", () => {
    const a = defaultDashboardLayout();
    const b = defaultDashboardLayout();
    expect(a.tiles).not.toBe(b.tiles);
    a.tiles.pop();
    expect(b.tiles).toHaveLength(BUILTIN_TILES.length);
  });
});

describe("isDashboardLayoutPayload", () => {
  it("accepts a well-formed dashboard-tiles blob", () => {
    expect(isDashboardLayoutPayload(defaultDashboardLayout())).toBe(true);
  });

  it("rejects the reserved topology/onboarding auto-layout shapes (no kind field)", () => {
    expect(isDashboardLayoutPayload({ positions: {}, activeLayers: [] })).toBe(false);
  });

  it("rejects a named saved-view blob (kind: 'view')", () => {
    expect(isDashboardLayoutPayload({ kind: "view", layers: [], zoom: 1, viewport: { x: 0, y: 0 }, view: "graph" })).toBe(
      false,
    );
  });

  it("rejects malformed tiles entries", () => {
    expect(isDashboardLayoutPayload({ kind: "dashboard-tiles", tiles: [{ id: "x" }] })).toBe(false);
    expect(isDashboardLayoutPayload({ kind: "dashboard-tiles", tiles: [{ kind: "builtin" }] })).toBe(false);
    expect(isDashboardLayoutPayload({ kind: "dashboard-tiles", tiles: "not-an-array" })).toBe(false);
  });

  it("rejects non-objects", () => {
    expect(isDashboardLayoutPayload(null)).toBe(false);
    expect(isDashboardLayoutPayload("dashboard-tiles")).toBe(false);
    expect(isDashboardLayoutPayload(42)).toBe(false);
  });
});

describe("pluginTileRefId / pluginTileIdFromRef", () => {
  it("round-trips a plugin tile id through its ref id", () => {
    const refId = pluginTileRefId("sample-tile");
    expect(refId).toBe("plugin:sample-tile");
    expect(pluginTileIdFromRef(refId)).toBe("sample-tile");
  });

  it("returns undefined for a ref id with no plugin prefix", () => {
    expect(pluginTileIdFromRef("builtin:findings")).toBeUndefined();
  });
});

describe("addTile / removeTile / moveTile", () => {
  const refs: DashboardTileRef[] = [
    { id: "builtin:findings", kind: "builtin" },
    { id: "builtin:drift", kind: "builtin" },
    { id: "builtin:changesets", kind: "builtin" },
  ];

  it("addTile appends a new ref and is a no-op if already present", () => {
    const added = addTile(refs, { id: "plugin:sample", kind: "plugin" });
    expect(added).toHaveLength(4);
    expect(added[3]).toEqual({ id: "plugin:sample", kind: "plugin" });

    const dup = addTile(added, { id: "builtin:findings", kind: "builtin" });
    expect(dup).toBe(added); // unchanged reference: genuinely a no-op
  });

  it("removeTile drops the matching ref and leaves others untouched", () => {
    const next = removeTile(refs, "builtin:drift");
    expect(next.map((t) => t.id)).toEqual(["builtin:findings", "builtin:changesets"]);
  });

  it("removeTile is a no-op for an id not present", () => {
    const next = removeTile(refs, "does-not-exist");
    expect(next.map((t) => t.id)).toEqual(refs.map((t) => t.id));
  });

  it("moveTile 'earlier' swaps with the previous slot", () => {
    const next = moveTile(refs, "builtin:drift", "earlier");
    expect(next.map((t) => t.id)).toEqual(["builtin:drift", "builtin:findings", "builtin:changesets"]);
  });

  it("moveTile 'later' swaps with the next slot", () => {
    const next = moveTile(refs, "builtin:drift", "later");
    expect(next.map((t) => t.id)).toEqual(["builtin:findings", "builtin:changesets", "builtin:drift"]);
  });

  it("moveTile is a no-op at the boundaries (first tile earlier, last tile later)", () => {
    expect(moveTile(refs, "builtin:findings", "earlier")).toEqual(refs);
    expect(moveTile(refs, "builtin:changesets", "later")).toEqual(refs);
  });

  it("moveTile is a no-op for an unknown id", () => {
    expect(moveTile(refs, "nope", "earlier")).toEqual(refs);
  });
});

describe("resolveTiles", () => {
  const pluginTiles: DashboardTile[] = [{ id: "sample-tile", title: "Sample", value: "42", link: "/topology" }];

  /** Resolves a single ref and returns it, asserting exactly one result —
   * avoids a bare `[resolved]` destructure, which under
   * noUncheckedIndexedAccess types as possibly-undefined. */
  function resolveOne(ref: DashboardTileRef, tiles: DashboardTile[]) {
    const results = resolveTiles([ref], tiles);
    expect(results).toHaveLength(1);
    const resolved = results[0];
    if (resolved === undefined) throw new Error("unreachable: length was asserted above");
    return resolved;
  }

  it("resolves a builtin ref against the live catalog", () => {
    const resolved = resolveOne({ id: "builtin:findings", kind: "builtin" }, []);
    expect(resolved.status).toBe("builtin");
    if (resolved.status === "builtin") {
      expect(resolved.def.id).toBe("builtin:findings");
    }
  });

  it("resolves a plugin ref against the current GET /dashboard/tiles response", () => {
    const resolved = resolveOne({ id: "plugin:sample-tile", kind: "plugin" }, pluginTiles);
    expect(resolved.status).toBe("plugin");
    if (resolved.status === "plugin") {
      expect(resolved.tile.id).toBe("sample-tile");
      expect(resolved.tile.value).toBe("42");
    }
  });

  it("resolves a plugin ref whose provider is absent/disabled/erroring as 'unavailable' — the explicit graceful-degradation case this card requires", () => {
    // The tile was on the grid once (its ref is still in the saved layout),
    // but the plugin is no longer in GET /dashboard/tiles' response —
    // disabled, uninstalled, or its Tiles() call started erroring
    // (already dropped server-side per plugin.Registry.DashboardTiles'
    // degrade-one-provider contract). resolveTiles must not throw and must
    // not silently drop the slot; it reports it as unavailable so the grid
    // can render an explicit placeholder instead.
    const resolved = resolveOne({ id: "plugin:gone-tile", kind: "plugin" }, pluginTiles);
    expect(resolved.status).toBe("unavailable");
  });

  it("resolves a stale builtin id (removed from a newer build) as 'unavailable' too, not a crash", () => {
    const resolved = resolveOne({ id: "builtin:no-longer-exists", kind: "builtin" }, []);
    expect(resolved.status).toBe("unavailable");
  });

  it("resolves an empty plugin-tiles list without throwing (network failure / no plugins installed)", () => {
    const resolved = resolveTiles([{ id: "plugin:anything", kind: "plugin" }], []);
    expect(resolved).toEqual([{ status: "unavailable", ref: { id: "plugin:anything", kind: "plugin" } }]);
  });
});

describe("addableTiles", () => {
  it("offers every built-in and plugin tile not already on the grid", () => {
    const pluginTiles: DashboardTile[] = [{ id: "sample-tile", title: "Sample", value: "42" }];
    const current: DashboardTileRef[] = [{ id: "builtin:findings", kind: "builtin" }];

    const addable = addableTiles(current, pluginTiles);
    expect(addable.some((a) => a.ref.id === "builtin:findings")).toBe(false); // already present
    expect(addable.some((a) => a.ref.id === "builtin:drift")).toBe(true);
    expect(addable.find((a) => a.ref.id === "plugin:sample-tile")).toEqual({
      ref: { id: "plugin:sample-tile", kind: "plugin" },
      label: "Sample",
    });
  });

  it("returns nothing addable once every built-in and plugin tile is already present", () => {
    const pluginTiles: DashboardTile[] = [{ id: "sample-tile", title: "Sample", value: "42" }];
    const current: DashboardTileRef[] = [
      ...BUILTIN_TILES.map((t) => ({ id: t.id, kind: "builtin" as const })),
      { id: "plugin:sample-tile", kind: "plugin" as const },
    ];
    expect(addableTiles(current, pluginTiles)).toEqual([]);
  });
});
