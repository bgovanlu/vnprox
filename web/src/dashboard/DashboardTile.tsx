// SPDX-License-Identifier: Apache-2.0

// Shared presentational shell for every Home dashboard tile (T-904,
// docs/features/monitoring.md §1/§3/§5, docs/features/topology.md §3):
// a titled card with a deep-link button into the tile's "owning page", and
// one of three body states — loading, error, or content. Every tile's
// content is itself responsible for choosing between its real data and an
// explicit "all clear" empty message (the `empty` prop) — AC4's "empty
// states must be explicit, never blank/error" contract. `EmptyState`
// (components/EmptyState.tsx) is deliberately NOT reused here: its fixed
// `min-h-[16rem]` panel is sized for a full-page placeholder, not a small
// grid tile, so this renders a lighter-weight equivalent instead.
import type { ReactNode } from "react";
import { HelpAnchor } from "../help/HelpAnchor";

export interface DashboardTileEmpty {
  title: string;
  description?: string;
}

export interface DashboardTileProps {
  title: string;
  description?: string;
  /** The tile's help topic, rendered as a `?` beside its heading. Optional
   * because not every tile is a surface with vocabulary of its own — but
   * where one is, this is how it gets into the help coverage census. */
  helpTopic?: string;
  isLoading?: boolean;
  error?: string;
  empty?: DashboardTileEmpty;
  /** Deep-links this tile into the page/surface that owns its data (AC3).
   * Not every "owning page" is a router route — the pending-changesets
   * tile opens the global changeset drawer instead (see
   * PendingChangesetsTile.tsx's doc comment) — so this is a plain
   * callback, not a `to` href. Every built-in tile passes both this and
   * `openLabel`; both are optional (T-3911) only because a plugin-provided
   * tile's `Link` field (docs/plugins/dashboard-tile.md) is itself
   * optional — a tile with genuinely nothing to deep-link to renders with
   * no open button at all, never a button wired to nothing. */
  onOpen?: () => void;
  openLabel?: string;
  children?: ReactNode;
}

export function DashboardTile({
  title,
  description,
  helpTopic,
  isLoading,
  error,
  empty,
  onOpen,
  openLabel,
  children,
}: DashboardTileProps) {
  return (
    <section
      aria-label={title}
      // T-4203: a card sitting on the dashboard's page background —
      // `surface-raised`.
      className="flex flex-col gap-2 rounded-lg border border-slate-200 bg-surface-raised p-4 dark:border-slate-800"
    >
      <div className="flex items-start justify-between gap-3">
        <div>
          <h2 className="flex items-center gap-1.5 text-sm font-semibold text-slate-800 dark:text-slate-100">
            {title}
            {helpTopic !== undefined && <HelpAnchor topic={helpTopic} />}
          </h2>
          {description ? <p className="text-xs text-fg-subtle">{description}</p> : null}
        </div>
        {onOpen && openLabel ? (
          <button
            type="button"
            onClick={onOpen}
            className="shrink-0 text-xs font-medium text-accent-600 underline hover:no-underline dark:text-accent-400"
          >
            {openLabel}
          </button>
        ) : null}
      </div>
      <div className="min-h-[3rem]">
        {isLoading ? (
          <p className="text-sm text-fg-subtle">Loading…</p>
        ) : error ? (
          <p className="text-sm text-status-critical">{error}</p>
        ) : empty ? (
          <div className="flex flex-col items-center justify-center gap-1 rounded-md border border-dashed border-slate-300 py-4 text-center dark:border-slate-700">
            <span className="text-sm font-medium text-slate-700 dark:text-slate-200">{empty.title}</span>
            {empty.description ? (
              <span className="max-w-xs text-xs text-fg-subtle">{empty.description}</span>
            ) : null}
          </div>
        ) : (
          children
        )}
      </div>
    </section>
  );
}
