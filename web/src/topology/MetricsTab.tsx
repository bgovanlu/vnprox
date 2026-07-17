// Inspector "Metrics" tab (docs/features/monitoring.md §1-2): live rx/tx
// bps/pps/errors/drops counters, a recharts rate-over-time sparkline from
// the 24h history ring, and — for a Bond — the per-slave balance view
// ("spot the LACP hash imbalance instantly").
import clsx from "clsx";
import { Area, AreaChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import type { WsClient } from "../api/ws";
import { useLiveMetrics, useMetricsHistoryQuery } from "./metricsQueries";
import { formatBps } from "./metricsFormat";

export interface MetricsTabProps {
  entityRef: string;
  kind: string;
  /** Injectable WS client, for tests (see useLiveMetrics's doc comment) —
   * production callers never pass this, so the shared browser singleton is
   * used. */
  wsClient?: WsClient;
}

function formatPerSec(v: number): string {
  return v < 10 ? v.toFixed(2) : v.toFixed(0);
}

function Stat({ label, value, warn }: { label: string; value: string; warn?: boolean }) {
  return (
    <div className="rounded border border-slate-200 p-1.5 dark:border-slate-700">
      <div className="text-slate-400">{label}</div>
      <div className={clsx("font-medium", warn ? "text-amber-600 dark:text-amber-400" : "text-slate-700 dark:text-slate-200")}>
        {value}
      </div>
    </div>
  );
}

export function MetricsTab({ entityRef, kind, wsClient }: MetricsTabProps) {
  const live = useLiveMetrics([entityRef], true, wsClient);
  const metric = live.get(entityRef);
  const { data: history } = useMetricsHistoryQuery(entityRef, true);

  const chartData = (history ?? []).map((p) => ({
    time: new Date(p.at * 1000).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" }),
    rxBps: p.rates.rxBps,
    txBps: p.rates.txBps,
  }));

  const errorsPerSec = (metric?.rates.rxErrsPerSec ?? 0) + (metric?.rates.txErrsPerSec ?? 0);
  const dropsPerSec = (metric?.rates.rxDropPerSec ?? 0) + (metric?.rates.txDropPerSec ?? 0);

  return (
    <div className="space-y-4">
      {!metric && (
        <p className="text-xs text-slate-400">
          No live rate yet — this entity needs at least two 5s samples before a rate is computable.
        </p>
      )}

      <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
        <Stat label="Rx" value={metric ? formatBps(metric.rates.rxBps) : "—"} />
        <Stat label="Tx" value={metric ? formatBps(metric.rates.txBps) : "—"} />
        <Stat label="Rx pps" value={metric ? formatPerSec(metric.rates.rxPps) : "—"} />
        <Stat label="Tx pps" value={metric ? formatPerSec(metric.rates.txPps) : "—"} />
        <Stat label="Errors/s" value={metric ? formatPerSec(errorsPerSec) : "—"} warn={errorsPerSec > 0} />
        <Stat label="Drops/s" value={metric ? formatPerSec(dropsPerSec) : "—"} warn={dropsPerSec > 0} />
        {metric?.utilizationPct !== undefined && (
          <Stat label="Utilization" value={`${metric.utilizationPct.toFixed(1)}%`} />
        )}
        {metric?.speedMbps !== undefined && metric.speedMbps > 0 && (
          <Stat label="Link speed" value={`${String(metric.speedMbps)} Mbps`} />
        )}
      </div>

      {chartData.length > 1 ? (
        <div className="h-32" data-testid="metrics-sparkline">
          <ResponsiveContainer width="100%" height="100%">
            <AreaChart data={chartData}>
              <CartesianGrid strokeDasharray="3 3" stroke="currentColor" className="text-slate-200 dark:text-slate-700" />
              <XAxis dataKey="time" tick={{ fontSize: 9 }} minTickGap={20} />
              <YAxis tick={{ fontSize: 9 }} width={44} tickFormatter={(v: number) => formatBps(v)} />
              <Tooltip formatter={(v) => formatBps(typeof v === "number" ? v : Number(v ?? 0))} />
              <Area type="monotone" dataKey="rxBps" name="Rx" stroke="#38bdf8" fill="#38bdf8" fillOpacity={0.2} />
              <Area type="monotone" dataKey="txBps" name="Tx" stroke="#a855f7" fill="#a855f7" fillOpacity={0.2} />
            </AreaChart>
          </ResponsiveContainer>
        </div>
      ) : (
        <p className="text-xs text-slate-400">
          Not enough history yet to chart (need at least two 30s-resolution samples).
        </p>
      )}

      {kind === "bond" && metric?.slaves && metric.slaves.length > 0 && (
        <div>
          <h3 className="mb-1 text-xs font-medium text-slate-500 dark:text-slate-400">
            Slave balance ({metric.slaves.length})
          </h3>
          <ul className="space-y-1">
            {metric.slaves.map((s) => (
              <li
                key={s.ref}
                className="flex items-center justify-between gap-2 rounded px-2 py-1 hover:bg-slate-100 dark:hover:bg-slate-800"
              >
                <span className="flex items-center gap-1.5 truncate">
                  <span
                    className={clsx(
                      "h-1.5 w-1.5 shrink-0 rounded-full",
                      s.active ? "bg-green-500" : "bg-slate-300 dark:bg-slate-600",
                    )}
                    title={s.active ? "active" : "inactive"}
                  />
                  <span className="truncate text-slate-700 dark:text-slate-200">{s.ref}</span>
                </span>
                <span className="shrink-0 text-slate-500 dark:text-slate-400">
                  {formatBps(s.rates.rxBps)} rx / {formatBps(s.rates.txBps)} tx
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
