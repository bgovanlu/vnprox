// SPDX-License-Identifier: Apache-2.0

// DaemonMetricsPanel (T-1903): the render body of vnprox's daemon self-
// observability Grafana panel — the exit demo's "the daemon's own Grafana
// dashboard shows the apply that caused it." Shipped alongside
// MetricsPanel.tsx (the cluster-derived panel, T-1001) with the identical
// contract: framework-agnostic React consuming GET /metrics' raw exposition
// text via the same parsePromText parser, tested against a fixture scrape
// with no live Grafana instance (web/grafana-panel/README.md's boundary —
// the packaged/signed Grafana plugin wrapper lives in the external plugin
// repo; this component and its data contract live here).
//
// Unlike MetricsPanel's flat counter/gauge families, several of T-1903's
// series are histograms (docs/features/monitoring.md §9): rendering every
// individual `_bucket{le=...}` sample as its own table row would be far
// too dense for a summary panel (a real percentile view belongs in a
// proper Grafana heatmap/histogram panel against the raw series, which
// this summary table is not trying to replace). Instead each histogram
// family is reduced to its `_count` and `_sum` samples per label
// combination and shown as "N calls, avg Xs" — enough to spot "the apply
// pipeline just got slow" or "one collector source is failing" at a
// glance, which is this panel's whole job.
import { useMemo } from "react";
import { type PromFamily, type PromSample, parsePromText } from "./promParse";

interface DaemonMetricsPanelProps {
  /** Raw Prometheus exposition text from GET /metrics (T-1001/T-1903). */
  scrape: string;
}

/** Plain counter/gauge families rendered as a flat label/value table,
 * exactly like MetricsPanel's own HIGHLIGHT_FAMILIES — in display order. */
const COUNTER_AND_GAUGE_FAMILIES = [
  "vnprox_http_requests_total",
  "vnprox_collector_polls_total",
  "vnprox_collector_consecutive_failures",
  "vnprox_change_outcomes_total",
  "vnprox_peer_calls_total",
  "vnprox_store_size_bytes",
  "vnprox_store_schema_version",
  "vnprox_store_schema_migration_pending",
  "vnprox_ws_connections",
];

/** Histogram families (base name, matching each one's `# TYPE ... histogram`
 * line) reduced to count/avg per label combination — in display order. */
const HISTOGRAM_FAMILIES = [
  "vnprox_http_request_duration_seconds",
  "vnprox_collector_poll_duration_seconds",
  "vnprox_change_awaiting_confirm_seconds",
  "vnprox_store_query_duration_seconds",
  "vnprox_peer_call_duration_seconds",
];

/** A stable key for a sample's label set, so a histogram's `_sum` and
 * `_count` samples for the same label combination can be matched up
 * regardless of declaration order. */
function labelKey(labels: Record<string, string>): string {
  return Object.entries(labels)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([k, v]) => `${k}=${v}`)
    .join(",");
}

interface HistogramRow {
  labelText: string;
  count: number;
  avgSeconds: number;
}

/** Reduces a histogram family's `<base>_sum`/`<base>_count` samples (found
 * by name in byName) to one row per label combination. Missing/unmatched
 * samples are simply skipped — a scrape mid-transition or a fixture that
 * only sets up one of the pair renders nothing for that row rather than
 * throwing. */
function histogramRows(base: string, byName: Map<string, PromFamily>): HistogramRow[] {
  const sums = byName.get(`${base}_sum`)?.samples ?? [];
  const counts = byName.get(`${base}_count`)?.samples ?? [];
  const countByKey = new Map<string, PromSample>();
  for (const s of counts) countByKey.set(labelKey(s.labels), s);

  const rows: HistogramRow[] = [];
  for (const sumSample of sums) {
    const key = labelKey(sumSample.labels);
    const countSample = countByKey.get(key);
    if (!countSample || countSample.value <= 0) continue;
    const labelText = Object.entries(sumSample.labels)
      .map(([k, v]) => `${k}=${v}`)
      .join(", ");
    rows.push({ labelText, count: countSample.value, avgSeconds: sumSample.value / countSample.value });
  }
  return rows;
}

export function DaemonMetricsPanel({ scrape }: DaemonMetricsPanelProps) {
  const { flatFamilies, histograms } = useMemo(() => {
    const parsed = parsePromText(scrape);
    const byName = new Map(parsed.map((f) => [f.name, f]));
    const flat = COUNTER_AND_GAUGE_FAMILIES.map((n) => byName.get(n)).filter((f): f is PromFamily => f !== undefined);
    const hist = HISTOGRAM_FAMILIES.map((base) => ({ base, rows: histogramRows(base, byName) })).filter(
      (h) => h.rows.length > 0,
    );
    return { flatFamilies: flat, histograms: hist };
  }, [scrape]);

  if (flatFamilies.length === 0 && histograms.length === 0) {
    return (
      <p className="text-sm text-fg-muted" data-testid="daemon-metrics-panel-empty">
        No vnprox daemon self-observability metrics in this scrape.
      </p>
    );
  }

  return (
    <div className="flex flex-col gap-3" data-testid="daemon-metrics-panel">
      {flatFamilies.map((f) => (
        <section key={f.name}>
          <h3 className="text-sm font-semibold" title={f.help}>
            {f.name}
          </h3>
          <table className="w-full text-sm">
            <tbody>
              {f.samples.map((s, i) => {
                const labelText = Object.entries(s.labels)
                  .map(([k, v]) => `${k}=${v}`)
                  .join(", ");
                return (
                  <tr key={`${f.name}-${String(i)}`}>
                    <td className="pr-3 text-fg-muted">{labelText || "—"}</td>
                    <td className="tabular-nums font-medium">{s.value}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </section>
      ))}
      {histograms.map(({ base, rows }) => (
        <section key={base}>
          <h3 className="text-sm font-semibold">{base}</h3>
          <table className="w-full text-sm">
            <tbody>
              {rows.map((row, i) => (
                <tr key={`${base}-${String(i)}`}>
                  <td className="pr-3 text-fg-muted">{row.labelText || "—"}</td>
                  <td className="tabular-nums font-medium">
                    {row.count} calls, avg {row.avgSeconds.toFixed(3)}s
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      ))}
    </div>
  );
}
