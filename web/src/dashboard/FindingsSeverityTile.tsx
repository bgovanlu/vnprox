// SPDX-License-Identifier: Apache-2.0

// Findings-by-severity tile (T-904 deliverables: "findings-by-severity
// (GET /findings, grouped)"). Reuses findings/queries.ts's
// useFindingsQuery — the same unbounded, unfiltered fetch
// FindingsStreamPanel.tsx (ToolsPage) already uses and filters
// client-side, so this tile shares that one cached response rather than
// issuing a second /findings round trip (findings/queries.ts's own doc
// comment: "no server-side filter — filtering happens client-side").
import { useMemo } from "react";
import { useNavigate } from "react-router-dom";
import { useFindingsQuery } from "../findings/queries";
import type { Severity } from "../api/types";
import { DashboardTile } from "./DashboardTile";

const SEVERITY_ORDER: Severity[] = ["error", "warning", "info"];

const SEVERITY_LABELS: Record<Severity, string> = {
  error: "Error",
  warning: "Warning",
  info: "Info",
};

const SEVERITY_DOT: Record<Severity, string> = {
  error: "bg-status-critical",
  warning: "bg-status-degraded",
  info: "bg-status-info",
};

export function FindingsSeverityTile() {
  const navigate = useNavigate();
  const { data: findings, isLoading, error } = useFindingsQuery();

  const counts = useMemo(() => {
    const c: Record<Severity, number> = { error: 0, warning: 0, info: 0 };
    for (const f of findings ?? []) c[f.severity] += 1;
    return c;
  }, [findings]);

  const total = counts.error + counts.warning + counts.info;

  return (
    <DashboardTile
      title="Findings by severity"
      description="Drift, LLDP, IPAM, and health checks — the unified findings stream."
      isLoading={isLoading}
      error={error ? "Could not load findings." : undefined}
      empty={
        total === 0
          ? { title: "All clear", description: "No open findings of any kind right now." }
          : undefined
      }
      onOpen={() => {
        void navigate("/tools");
      }}
      openLabel="Open findings"
    >
      <ul className="flex flex-col gap-1.5">
        {SEVERITY_ORDER.filter((s) => counts[s] > 0).map((s) => (
          <li key={s} className="flex items-center gap-2 text-sm">
            <span aria-hidden className={`h-2.5 w-2.5 shrink-0 rounded-full ${SEVERITY_DOT[s]}`} />
            <span className="text-fg-body">{SEVERITY_LABELS[s]}</span>
            <span className="ml-auto font-semibold tabular-nums">{counts[s]}</span>
          </li>
        ))}
      </ul>
    </DashboardTile>
  );
}
