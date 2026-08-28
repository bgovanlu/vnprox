// SPDX-License-Identifier: Apache-2.0

// Drift-status tile (T-904 deliverables: "drift status (/findings?source=
// drift)"). Rather than issuing a second request with a server-side
// `?source=drift` filter, this reuses the exact same cached
// useFindingsQuery() response the findings-by-severity tile reads and
// filters to `source === "drift"` client-side — the same "filtering is a
// client-side concern over one shared fetch" convention
// findings/filters.ts's filterFindings already established for
// FindingsStreamPanel (ToolsPage), and TanStack Query's cache means the
// two tiles never duplicate the network call.
import { useMemo } from "react";
import { useNavigate } from "react-router-dom";
import { useFindingsQuery } from "../findings/queries";
import { DashboardTile } from "./DashboardTile";

export function DriftStatusTile() {
  const navigate = useNavigate();
  const { data: findings, isLoading, error } = useFindingsQuery();

  const drift = useMemo(() => (findings ?? []).filter((f) => f.source === "drift"), [findings]);
  const nodes = useMemo(() => Array.from(new Set(drift.flatMap((f) => f.nodes))).sort(), [drift]);

  return (
    <DashboardTile
      title="Drift status"
      description="Configuration that has diverged from what vnprox last applied."
      isLoading={isLoading}
      error={error ? "Could not load drift findings." : undefined}
      empty={
        drift.length === 0
          ? { title: "No drift detected", description: "Every node's live config matches what vnprox applied." }
          : undefined
      }
      onOpen={() => {
        void navigate("/tools");
      }}
      openLabel="Open findings"
    >
      <p className="text-sm text-slate-700 dark:text-slate-200">
        <span className="font-semibold tabular-nums">{drift.length}</span>{" "}
        {drift.length === 1 ? "drift finding" : "drift findings"}
        {nodes.length > 0 ? ` across ${String(nodes.length)} ${nodes.length === 1 ? "node" : "nodes"}` : ""}
      </p>
      {nodes.length > 0 ? (
        <p className="mt-1 truncate text-xs text-fg-subtle">{nodes.join(", ")}</p>
      ) : null}
    </DashboardTile>
  );
}
