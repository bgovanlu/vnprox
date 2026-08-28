// SPDX-License-Identifier: Apache-2.0

// Pending / awaiting-confirm changesets tile (T-904 deliverables:
// "pending/awaiting-confirm changesets (GET /changesets, client-filtered
// to non-terminal statuses)"). Deviation from the task card's prose
// ("/changesets" listed alongside the other tiles' deep-link routes):
// there is no routed /changesets page in App.tsx — changesets are worked
// in the persistent global ChangesetDrawer (AppShell.tsx), opened by
// setting changesets/store.ts's `activeId`, not by navigation. This tile's
// "owning page" is therefore that drawer, opened the same way
// FindingsStreamPanel's "Create fixing changeset" action and every entity
// editor already do.
import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { listChangesets } from "../api/changesets";
import type { Changeset, ChangesetStatus } from "../api/types";
import { TERMINAL_STATUSES } from "../changesets/drawerMachine";
import { useChangesetDrawerStore } from "../changesets/store";
import { DashboardTile } from "./DashboardTile";

const STATUS_LABELS: Record<ChangesetStatus, string> = {
  draft: "Draft",
  validated: "Validated",
  applying: "Applying",
  awaiting_confirm: "Awaiting confirm",
  committed: "Committed",
  rolled_back: "Rolled back",
  failed: "Failed",
  discarded: "Discarded",
};

function isPending(cs: Changeset): boolean {
  return !TERMINAL_STATUSES.has(cs.status);
}

export function PendingChangesetsTile() {
  const setActiveId = useChangesetDrawerStore((s) => s.setActiveId);
  const setDrawerOpen = useChangesetDrawerStore((s) => s.setDrawerOpen);

  const { data, isLoading, error } = useQuery<Changeset[]>({
    queryKey: ["changesets", "list", "dashboard-pending"],
    queryFn: () => listChangesets(),
    staleTime: 5_000,
  });

  const pending = useMemo(() => {
    return (data ?? []).filter(isPending).sort((a, b) => b.updatedAt - a.updatedAt);
  }, [data]);

  const awaitingConfirmCount = pending.filter((c) => c.status === "awaiting_confirm").length;

  function open(): void {
    const mostRecent = pending[0];
    if (mostRecent) {
      setActiveId(mostRecent.id);
    } else {
      setDrawerOpen(true);
    }
  }

  return (
    <DashboardTile
      title="Pending changesets"
      description="Drafts, validated changes, and anything awaiting commit-confirm."
      isLoading={isLoading}
      error={error ? "Could not load changesets." : undefined}
      empty={
        pending.length === 0
          ? { title: "Nothing pending", description: "No draft, validated, applying, or awaiting-confirm changesets." }
          : undefined
      }
      onOpen={open}
      openLabel="Open drawer"
    >
      <div className="flex flex-col gap-1.5">
        {awaitingConfirmCount > 0 ? (
          <p className="text-sm font-medium text-amber-700 dark:text-amber-400">
            {awaitingConfirmCount} awaiting confirm
          </p>
        ) : null}
        <ul className="flex flex-col gap-1">
          {pending.slice(0, 5).map((cs) => (
            <li key={cs.id} className="flex items-center justify-between gap-2 text-sm">
              <button
                type="button"
                onClick={() => {
                  setActiveId(cs.id);
                }}
                className="truncate text-left text-slate-700 underline-offset-2 hover:underline dark:text-slate-200"
                title={cs.title}
              >
                {cs.title}
              </button>
              <span className="shrink-0 text-xs text-slate-500 dark:text-slate-400">{STATUS_LABELS[cs.status]}</span>
            </li>
          ))}
        </ul>
      </div>
    </DashboardTile>
  );
}
