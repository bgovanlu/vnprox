// SPDX-License-Identifier: Apache-2.0

// Recent audit tile (T-904 deliverables: "recent audit entries (GET
// /audit?limit=5)"). Note the capability nuance for the report: `/audit`
// is gated on the `audit` capability (docs/api.md: "requires the `audit`
// capability ..., not netRead"), distinct from every other tile's route
// (findings/changesets/protected-interfaces/status/metrics/live are all
// netRead-gated). This tile simply renders whatever the session is
// entitled to see; a session with netRead but not audit gets a normal
// 403 surfaced the same way AuditPage.tsx's own fetch already handles it
// (an error state, not a crash).
import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { fetchAudit, type AuditEntry, type AuditListResponse } from "../api/audit";
import { DashboardTile } from "./DashboardTile";

function formatTime(unixSeconds: number): string {
  return new Date(unixSeconds * 1000).toLocaleString();
}

function describe(entry: AuditEntry): string {
  return entry.target ? `${entry.action} · ${entry.target}` : entry.action;
}

export function RecentAuditTile() {
  const navigate = useNavigate();
  const { data, isLoading, error } = useQuery<AuditListResponse>({
    queryKey: ["audit", "dashboard-recent"],
    queryFn: () => fetchAudit({}, undefined, 5),
    staleTime: 10_000,
  });

  const items = data?.items ?? [];

  return (
    <DashboardTile
      title="Recent audit activity"
      description="The last 5 entries in vnprox's own audit log."
      isLoading={isLoading}
      error={error ? "Could not load the audit log." : undefined}
      empty={items.length === 0 ? { title: "No audit activity yet", description: "Nothing has been logged yet." } : undefined}
      onOpen={() => {
        void navigate("/audit");
      }}
      openLabel="Open audit log"
    >
      <ul className="flex flex-col gap-1.5">
        {items.map((entry) => (
          <li key={entry.id} className="flex items-center justify-between gap-2 text-sm">
            <span className="truncate text-slate-700 dark:text-slate-200" title={describe(entry)}>
              {describe(entry)}
            </span>
            <span className="shrink-0 text-xs text-slate-500 dark:text-slate-400">{formatTime(entry.at)}</span>
          </li>
        ))}
      </ul>
    </DashboardTile>
  );
}
