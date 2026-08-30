// SPDX-License-Identifier: Apache-2.0

// T-2403's inspector History tab: what has been done to this entity, and by
// whom.
//
// Three sources are merged server-side because each knows a different half of
// the story — a changeset says what was intended and by whom, an audit row says
// when something actually happened and whether it succeeded, and a snapshot
// says where a restore point sits relative to both. This component's job is to
// keep them visually distinguishable while showing them on one timeline.
//
// `truncated` is surfaced rather than swallowed. A silently short history is
// indistinguishable from a genuinely short one, and "nothing has ever touched
// this bridge" is a conclusion an operator will act on.
import clsx from "clsx";
import { EmptyState } from "../components/EmptyState";
import { useEntityHistoryQuery } from "./entityHistoryQuery";
import type { EntityHistoryEntry } from "../api/types";

const KIND_LABEL: Record<EntityHistoryEntry["kind"], string> = {
  changeset: "Changeset",
  audit: "Audit",
  snapshot: "Snapshot",
};

const KIND_CLASSES: Record<EntityHistoryEntry["kind"], string> = {
  changeset: "bg-accent-soft text-accent-fg",
  audit: "bg-slate-200 text-slate-700 dark:bg-slate-700 dark:text-slate-200",
  snapshot: "bg-emerald-100 text-emerald-800 dark:bg-emerald-900 dark:text-emerald-100",
};

export interface EntityHistoryTabProps {
  /** The entity's full `kind:node:id` ref. */
  entityRef: string;
  /** False while the tab is not shown, so the fetch is not made until asked. */
  enabled?: boolean;
}

export function EntityHistoryTab({ entityRef, enabled = true }: EntityHistoryTabProps) {
  const { data, isLoading, isError } = useEntityHistoryQuery(entityRef, enabled);

  if (isLoading) {
    return <p className="text-xs text-slate-600 dark:text-slate-400">Loading history…</p>;
  }
  // A failure must not render as "nothing has touched this" — that is a
  // conclusion, and it would be the wrong one.
  if (isError || !data) {
    return (
      <p className="text-xs text-red-600 dark:text-red-400">
        Could not load this entity&rsquo;s history.
      </p>
    );
  }
  if (data.items.length === 0) {
    return (
      <EmptyState
        icon="node"
        variant="empty"
        title="No recorded history"
        description="vnprox has no changeset, audit entry, or snapshot touching this entity."
      />
    );
  }

  return (
    <div className="flex flex-col gap-2">
      {data.truncated && (
        <p className="rounded border border-amber-300 bg-amber-50 px-2 py-1 text-[11px] text-amber-800 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-200">
          Older changesets were not searched. This history is incomplete.
        </p>
      )}
      <ol className="space-y-1.5 text-xs" aria-label="Entity history">
        {data.items.map((e, i) => (
          <li
            key={`${e.kind}:${String(e.at)}:${e.changesetId ?? ""}:${e.snapshotId ?? ""}:${String(i)}`}
            className="rounded border border-slate-200 px-2 py-1.5 dark:border-slate-700"
          >
            <div className="flex flex-wrap items-baseline gap-2">
              <span className={clsx("rounded px-1 py-0.5 text-[10px] uppercase tracking-wide", KIND_CLASSES[e.kind])}>
                {KIND_LABEL[e.kind]}
              </span>
              <span className="text-fg-subtle">
                {new Date(e.at * 1000).toLocaleString()}
              </span>
              {e.actor && <span className="text-slate-600 dark:text-slate-300">{e.actor}</span>}
              {e.result && <span className="text-slate-600 dark:text-slate-400">{e.result}</span>}
            </div>
            <p className="mt-0.5 text-slate-700 dark:text-slate-200">{e.summary}</p>
            {e.changesetId && (
              <a
                href={`/changesets/${encodeURIComponent(e.changesetId)}`}
                className="text-[11px] text-accent-fg underline"
              >
                Open changeset
              </a>
            )}
          </li>
        ))}
      </ol>
    </div>
  );
}
