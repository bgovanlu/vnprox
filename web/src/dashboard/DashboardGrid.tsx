// SPDX-License-Identifier: Apache-2.0

// T-3911's composable dashboard grid: a per-user, persisted, ordered tile
// list mixing built-in tiles (tileRegistry.ts) and plugin-provided tiles
// (GET /dashboard/tiles) through one mechanism — add/remove/reorder apply
// identically to both, and every tile renders through DashboardTile.tsx's
// shared shell (built-ins already did; PluginTile.tsx now does too, and so
// does the graceful UnavailableTile.tsx placeholder). Reorder is a plain
// "move earlier"/"move later" button pair, not drag-and-drop — every
// control here is a real `<button>`, reachable and operable with Tab/Enter
// alone, satisfying this card's "keyboard-operable, not drag-only"
// requirement by construction rather than as an added fallback.
import { useMemo } from "react";
import * as RadixDropdown from "@radix-ui/react-dropdown-menu";
import { Button } from "../components/Button";
import { useToast } from "../components/Toast";
import type { DashboardTileRef } from "../api/types";
import {
  addTile,
  addableTiles,
  defaultDashboardLayout,
  moveTile,
  removeTile,
  resolveTiles,
  type ResolvedTile,
} from "./dashboardLayout";
import {
  useDashboardLayoutQuery,
  useDashboardPluginTilesQuery,
  useSaveDashboardLayoutMutation,
} from "./dashboardLayoutQueries";
import { PluginTile } from "./PluginTile";
import { UnavailableTile } from "./UnavailableTile";

const menuItemClass =
  "flex cursor-pointer items-center rounded px-2 py-1.5 text-sm outline-none hover:bg-slate-100 dark:hover:bg-slate-800";

function labelFor(resolved: ResolvedTile): string {
  switch (resolved.status) {
    case "builtin":
      return resolved.def.label;
    case "plugin":
      return resolved.tile.title;
    case "unavailable":
      return "Unavailable tile";
  }
}

export function DashboardGrid() {
  // No blocking spinner while the saved layout loads: rendering starts
  // immediately from the built-in default (defaultDashboardLayout's tiles,
  // exactly what a never-customised user's saved layout would resolve to
  // anyway) and reconciles to the real saved arrangement once the query
  // settles — the pre-T-3911 grid mounted every built-in tile synchronously
  // on first paint (each tile owned its own loading state internally,
  // DashboardTile.tsx's `isLoading`), and gating the whole grid behind one
  // more network round trip here would be a regression, not a feature.
  const { data: layout } = useDashboardLayoutQuery();
  const { data: pluginTiles } = useDashboardPluginTilesQuery();
  const saveMutation = useSaveDashboardLayoutMutation();
  const { toast } = useToast();

  const tiles = useMemo(() => layout?.tiles ?? defaultDashboardLayout().tiles, [layout]);
  const resolved = useMemo(() => resolveTiles(tiles, pluginTiles ?? []), [tiles, pluginTiles]);
  const addable = useMemo(() => addableTiles(tiles, pluginTiles ?? []), [tiles, pluginTiles]);

  function persist(nextTiles: DashboardTileRef[]): void {
    saveMutation.mutate(
      { kind: "dashboard-tiles", tiles: nextTiles },
      {
        onError: () => {
          toast({ title: "Could not save dashboard layout", variant: "error" });
        },
      },
    );
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex justify-end">
        <RadixDropdown.Root>
          <RadixDropdown.Trigger asChild>
            <Button size="sm" variant="secondary" disabled={addable.length === 0}>
              Add tile ▾
            </Button>
          </RadixDropdown.Trigger>
          <RadixDropdown.Portal>
            <RadixDropdown.Content
              align="end"
              sideOffset={6}
              className="z-50 min-w-[14rem] rounded-md border border-border bg-white p-1 shadow-lg dark:bg-slate-900"
            >
              {addable.length === 0 ? (
                <p className="px-2 py-1.5 text-xs text-fg-muted">
                  Every available tile is already on your dashboard.
                </p>
              ) : (
                addable.map((a) => (
                  <RadixDropdown.Item
                    key={a.ref.id}
                    className={menuItemClass}
                    onSelect={() => {
                      persist(addTile(tiles, a.ref));
                    }}
                  >
                    {a.label}
                  </RadixDropdown.Item>
                ))
              )}
            </RadixDropdown.Content>
          </RadixDropdown.Portal>
        </RadixDropdown.Root>
      </div>

      {resolved.length === 0 ? (
        <div className="flex flex-col items-center justify-center gap-1 rounded-md border border-dashed border-border-strong py-8 text-center">
          <span className="text-sm font-medium text-fg-body">No tiles on your dashboard</span>
          <span className="max-w-xs text-xs text-fg-subtle">
            Use "Add tile" above to bring one back.
          </span>
        </div>
      ) : (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {resolved.map((r, index) => {
            const label = labelFor(r);
            return (
              <div key={r.ref.id} className="flex flex-col gap-1">
                <div className="flex items-center justify-end gap-1 px-1">
                  <button
                    type="button"
                    aria-label={`Move ${label} earlier`}
                    disabled={index === 0}
                    onClick={() => {
                      persist(moveTile(tiles, r.ref.id, "earlier"));
                    }}
                    className="rounded p-1 text-xs leading-none text-fg-muted hover:bg-slate-200 disabled:pointer-events-none disabled:opacity-30 dark:hover:bg-slate-800"
                  >
                    <span aria-hidden>↑</span>
                  </button>
                  <button
                    type="button"
                    aria-label={`Move ${label} later`}
                    disabled={index === resolved.length - 1}
                    onClick={() => {
                      persist(moveTile(tiles, r.ref.id, "later"));
                    }}
                    className="rounded p-1 text-xs leading-none text-fg-muted hover:bg-slate-200 disabled:pointer-events-none disabled:opacity-30 dark:hover:bg-slate-800"
                  >
                    <span aria-hidden>↓</span>
                  </button>
                  <button
                    type="button"
                    aria-label={`Remove ${label} from dashboard`}
                    onClick={() => {
                      persist(removeTile(tiles, r.ref.id));
                    }}
                    className="rounded p-1 text-xs leading-none text-fg-muted hover:bg-red-100 hover:text-red-600 dark:hover:bg-red-900/40 dark:hover:text-red-400"
                  >
                    <span aria-hidden>✕</span>
                  </button>
                </div>
                {r.status === "builtin" ? (
                  <r.def.Component />
                ) : r.status === "plugin" ? (
                  <PluginTile tile={r.tile} />
                ) : (
                  <UnavailableTile />
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
