// The LLDP Ports page (roadmap: "LLDP Ports-view frontend page — API
// already ships GET /ports"). A flat, sortable-by-nature table pairing each
// cluster NIC with the switch/port LLDP reports for it — the read-only
// "what's plugged into what" view, with the same CSV export the backend
// already serves. Stale rows (a neighbor greyed past 2×TTL / 10 min) are
// kept and marked, since an unplugged link is exactly what you come here to
// troubleshoot.
import { API_BASE } from "../api/client";
import { EmptyState } from "../components/EmptyState";
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
      <header className="flex flex-wrap items-start justify-between gap-2">
        <div>
          <h1 className="text-lg font-semibold text-slate-800 dark:text-slate-100">Ports</h1>
          <p className="mt-1 text-sm text-slate-600 dark:text-slate-300">
            Every physical NIC in the cluster and the switch port LLDP says it&apos;s connected to. Stale rows (a link
            that hasn&apos;t been seen recently) are kept and flagged for troubleshooting.
          </p>
        </div>
        <a
          href={`${API_BASE}/ports?format=csv`}
          className="shrink-0 rounded-md border border-slate-300 px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-100 dark:border-slate-600 dark:text-slate-200 dark:hover:bg-slate-800"
          download
        >
          Export CSV
        </a>
      </header>

      {isLoading && <p className="text-sm text-slate-400">Loading ports…</p>}
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
