// SPDX-License-Identifier: Apache-2.0

// T-2704's point-in-time topology diff, rendered.
//
// The file-level snapshot diff next to this one answers "what text changed".
// This answers the question an operator actually asks: WHICH ENTITIES are
// different from Tuesday, field by field — and, for each one, whether vnprox
// did it.
//
// THE UNATTRIBUTED ROWS ARE THE POINT, so they are not a subtle grey label:
// they carry their own badge and are counted in the header. A difference no
// changeset explains is a change somebody made outside vnprox, which is the
// class of change the drift checker exists to catch.
import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";

import { ApiError } from "../api/client";
import {
  allTopologyDiffRows,
  fetchTopologyDiff,
  type TopologyDiffResponse,
  type TopologyEntityDiff,
} from "../api/topologyDiff";
import { Button } from "../components/Button";
import { EmptyState } from "../components/EmptyState";
import { HelpAnchor } from "../help/HelpAnchor";

export interface TopologyDiffPanelProps {
  /** Snapshot id, unix/RFC3339 timestamp, or "" for "nothing selected". */
  from: string;
  /** Same, plus the "now" sentinel. */
  to: string;
}

function changeLabel(change: TopologyEntityDiff["change"]): string {
  switch (change) {
    case "added":
      return "Added";
    case "removed":
      return "Removed";
    case "modified":
      return "Changed";
  }
}

function changeClasses(change: TopologyEntityDiff["change"]): string {
  switch (change) {
    case "added":
      return "bg-green-100 text-green-800 dark:bg-green-950 dark:text-green-200";
    case "removed":
      return "bg-purple-100 text-purple-800 dark:bg-purple-950 dark:text-purple-200";
    case "modified":
      return "bg-blue-100 text-blue-800 dark:bg-blue-950 dark:text-blue-200";
  }
}

/** Renders one row's attribution. An attributed row names the changeset and
 * links to it; an unattributed one says so in as many words. */
function Attribution({ row }: { row: TopologyEntityDiff }) {
  if (!row.attribution.attributed) {
    return (
      <span
        className="rounded bg-red-100 px-1.5 py-0.5 text-xs font-medium text-red-800 dark:bg-red-950 dark:text-red-200"
        title="No changeset explains this difference — it was made outside vnprox."
      >
        Unattributed
      </span>
    );
  }
  const { changesetId, changesetTitle, actor } = row.attribution;
  return (
    <span className="text-xs text-fg-muted">
      by {actor ?? "vnprox"} in{" "}
      {changesetId ? (
        <Link className="underline" to={`/changesets?id=${encodeURIComponent(changesetId)}`}>
          {changesetTitle && changesetTitle !== "" ? changesetTitle : changesetId}
        </Link>
      ) : (
        (changesetTitle ?? "a changeset")
      )}
    </span>
  );
}

function DiffRow({ row }: { row: TopologyEntityDiff }) {
  return (
    <li className="rounded-md border border-border px-3 py-2">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex min-w-0 items-center gap-2">
          <span className={`rounded px-1.5 py-0.5 text-xs font-medium ${changeClasses(row.change)}`}>
            {changeLabel(row.change)}
          </span>
          <span className="truncate text-sm font-medium text-slate-800 dark:text-slate-100">
            {row.name ?? row.ref}
          </span>
          <span className="truncate text-xs text-fg-muted">{row.ref}</span>
        </div>
        <Attribution row={row} />
      </div>
      {row.fields.length > 0 && (
        <table className="mt-2 w-full table-fixed text-xs">
          <thead className="text-fg-muted">
            <tr>
              <th className="w-1/3 text-left font-normal">Field</th>
              <th className="w-1/3 text-left font-normal">Before</th>
              <th className="w-1/3 text-left font-normal">After</th>
            </tr>
          </thead>
          <tbody>
            {row.fields.map((f) => (
              <tr key={f.field}>
                <td className="truncate pr-2 text-fg-body">{f.field}</td>
                <td className="truncate pr-2 font-mono text-fg-muted">{f.before || "—"}</td>
                <td className="truncate font-mono text-fg">{f.after || "—"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </li>
  );
}

export function TopologyDiffPanel({ from, to }: TopologyDiffPanelProps) {
  const enabled = from !== "" && to !== "";
  const query = useQuery<TopologyDiffResponse>({
    queryKey: ["topology-diff", from, to],
    queryFn: () => fetchTopologyDiff(from, to),
    enabled,
    retry: false,
  });

  if (!enabled) {
    return (
      <EmptyState
        title="Pick two points to compare"
        description='Choose a "From" and a "To" snapshot on the timeline, or use "vs live" to compare a snapshot with the cluster as it is right now.'
      />
    );
  }
  if (query.isLoading) {
    return <p className="text-sm text-fg-muted">Computing topology diff…</p>;
  }
  if (query.isError) {
    // The server's message is shown verbatim on purpose: for an uncovered
    // range it NAMES the nearest snapshots that do exist, which is the one
    // thing that tells the operator which range to ask for instead.
    return (
      <EmptyState
        icon="node"
        variant="failed"
        title="Could not diff this range"
        description={query.error instanceof ApiError ? query.error.message : "unexpected error"}
        action={
          <Button variant="secondary" size="sm" onClick={() => void query.refetch()}>
            Retry
          </Button>
        }
      />
    );
  }

  const diff = query.data;
  if (!diff) return null;
  const rows = allTopologyDiffRows(diff);

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2 text-xs text-fg-muted">
        <HelpAnchor topic="topology-point-in-time-diff" />
        <span>
          {rows.length} {rows.length === 1 ? "difference" : "differences"} across{" "}
          {diff.coverage.nodes.length || "no"} {diff.coverage.nodes.length === 1 ? "node" : "nodes"}
        </span>
        {diff.unattributedCount > 0 && (
          <span className="rounded bg-red-100 px-1.5 py-0.5 font-medium text-red-800 dark:bg-red-950 dark:text-red-200">
            {diff.unattributedCount} made outside vnprox
          </span>
        )}
        <Link
          className="underline"
          to={`/topology?diffFrom=${encodeURIComponent(from)}&diffTo=${encodeURIComponent(to)}`}
        >
          Show on map
        </Link>
      </div>

      {/* Coverage is stated, not implied: a node captured at only one end of
          the range is named here rather than having every interface on it
          reported as created or deleted. */}
      {diff.coverage.unmatchedNodes && diff.coverage.unmatchedNodes.length > 0 && (
        <p className="rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-200">
          Not compared:{" "}
          {diff.coverage.unmatchedNodes
            .map((u) => `${u.node} (only in ${u.presentIn === "from" ? "the earlier" : "the later"} point)`)
            .join(", ")}
          . These nodes were captured at only one end of the range, so nothing is claimed about them.
        </p>
      )}

      {rows.length === 0 ? (
        <EmptyState
          icon="node"
          variant="empty"
          title="No differences"
          description={`Nothing changed on ${diff.coverage.nodes.join(", ") || "the compared nodes"} between these two points.`}
        />
      ) : (
        <ul className="flex flex-col gap-2">
          {rows.map((row) => (
            <DiffRow key={`${row.change}:${row.ref}`} row={row} />
          ))}
        </ul>
      )}
    </div>
  );
}
