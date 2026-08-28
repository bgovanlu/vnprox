// SPDX-License-Identifier: Apache-2.0

// The LLDP Ports page (roadmap: "LLDP Ports-view frontend page — API
// already ships GET /ports"). A flat, sortable-by-nature table pairing each
// cluster NIC with the switch/port LLDP reports for it — the read-only
// "what's plugged into what" view, with the same CSV export the backend
// already serves. Stale rows (a neighbor greyed past 2×TTL / 10 min) are
// kept and marked, since an unplugged link is exactly what you come here to
// troubleshoot.
import { API_BASE } from "../api/client";
import { EmptyState } from "../components/EmptyState";
import { PageHeader } from "../components/PageHeader";
import type { PortRow } from "../api/types";
import { usePortsQuery } from "./queries";

function speedLabel(row: PortRow): string {
  if (row.speedMbps && row.speedMbps > 0) {
    return row.speedMbps >= 1000 ? `${String(row.speedMbps / 1000)} Gbps` : `${String(row.speedMbps)} Mbps`;
  }
  return row.speedDescr ?? "";
}

function lastSeenLabel(sec?: number): string {
  if (!sec) return "";
  const ageSec = Math.max(0, Math.floor(Date.now() / 1000) - sec);
  if (ageSec < 60) return "just now";
  if (ageSec < 3600) return `${String(Math.floor(ageSec / 60))}m ago`;
  if (ageSec < 86400) return `${String(Math.floor(ageSec / 3600))}h ago`;
  return `${String(Math.floor(ageSec / 86400))}d ago`;
}

const th = "px-2 py-1.5 text-left font-medium text-slate-500 dark:text-slate-400";
const td = "px-2 py-1.5 align-top";

export function PortsPage() {
  const { data, isLoading, isError } = usePortsQuery();
  const rows = data?.items ?? [];

  return (
    <div className="mx-auto max-w-5xl space-y-4">
      <PageHeader
        title="Ports"
        description="Every physical NIC in the cluster and the switch port LLDP says it's connected to. Stale rows (a link
          that hasn't been seen recently) are kept and flagged for troubleshooting."
        actions={
          // T-3404: a page-level action gets the pill treatment, but this is
          // a file download (`<a download>`, same reasoning as
          // ToolsPage.tsx's doc-export links) rather than a `Button` — no
          // `onClick`/handler to preserve, so it's styled to match
          // `Button({ variant: "secondary", shape: "pill" })` by hand.
          <a
            href={`${API_BASE}/ports?format=csv`}
            download
            className="inline-flex h-9 shrink-0 items-center justify-center rounded-pill bg-slate-200 px-3.5 text-sm font-medium text-slate-900 hover:bg-slate-300 dark:bg-slate-800 dark:text-slate-100 dark:hover:bg-slate-700"
          >
            Export CSV
          </a>
        }
      />

      {isLoading && <p className="text-sm text-slate-600 dark:text-slate-400">Loading ports…</p>}
      {isError && <p className="text-sm text-red-600 dark:text-red-400">Could not load the ports table.</p>}

      {data && rows.length === 0 && (
        <EmptyState
          title="No ports discovered yet"
          description="vnprox learns this table from LLDP. If it stays empty, lldpd may not be running on your nodes — the onboarding walkthrough can install it, or check your switches advertise LLDP."
        />
      )}

      {rows.length > 0 && (
        <div className="overflow-x-auto rounded-lg border border-slate-200 dark:border-slate-700">
          <table className="w-full border-collapse text-sm">
            <thead className="border-b border-slate-200 bg-slate-50 dark:border-slate-700 dark:bg-slate-900">
              <tr>
                <th className={th}>Node</th>
                <th className={th}>NIC</th>
                <th className={th}>Switch</th>
                <th className={th}>Port</th>
                <th className={th}>Speed</th>
                <th className={th}>PVID</th>
                <th className={th}>Tagged VLANs</th>
                <th className={th}>Last seen</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row, i) => (
                <tr
                  key={`${row.node}/${row.nic}/${String(i)}`}
                  className={
                    "border-b border-slate-100 last:border-b-0 dark:border-slate-800 " +
                    (row.stale ? "text-slate-500 dark:text-slate-400" : "text-slate-700 dark:text-slate-200")
                  }
                >
                  <td className={td}>{row.node}</td>
                  <td className={`${td} font-medium`}>{row.nic}</td>
                  <td className={td}>{row.switch}</td>
                  <td className={td}>{row.port}</td>
                  <td className={td}>{speedLabel(row)}</td>
                  <td className={td}>{row.pvid ? String(row.pvid) : ""}</td>
                  <td className={td}>{row.taggedVlans && row.taggedVlans.length > 0 ? row.taggedVlans.join(", ") : ""}</td>
                  <td className={td}>
                    {lastSeenLabel(row.lastSeen)}
                    {row.stale && (
                      <span className="ml-1.5 rounded bg-amber-100 px-1.5 py-0.5 text-[10px] font-semibold text-amber-800 dark:bg-amber-900/50 dark:text-amber-200">
                        stale
                      </span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
