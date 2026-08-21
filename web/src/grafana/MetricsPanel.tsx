// MetricsPanel (T-1706): the render body of the vnprox Grafana metrics
// panel. It is deliberately framework-agnostic React — the Grafana plugin
// wrapper (module.ts registering a PanelPlugin, plugin.json, signing) lives
// in the external plugin repo per the T-1106/T-1705 "contract not source"
// boundary (see web/grafana-panel/README.md); this component and its data
// contract (T-1001's Prometheus exporter body) live in-repo so they can be
// unit-tested against a fixture scrape with no live Grafana instance (AC4).
//
// In production Grafana supplies the scrape body via its Prometheus
// datasource; here the caller passes the raw exposition text as a prop so the
// same component renders identically in a test.
import { useMemo } from "react";
import { parsePromText } from "./promParse";

interface MetricsPanelProps {
  /** Raw Prometheus exposition text from GET /metrics (T-1001). */
  scrape: string;
}

/** The vnprox metric families this panel highlights, in display order. */
const HIGHLIGHT_FAMILIES = [
  "vnprox_findings_open",
  "vnprox_drift_open",
  "vnprox_changesets",
  "vnprox_build_info",
];

export function MetricsPanel({ scrape }: MetricsPanelProps) {
  const families = useMemo(() => {
    const parsed = parsePromText(scrape);
    const byName = new Map(parsed.map((f) => [f.name, f]));
    return HIGHLIGHT_FAMILIES.map((n) => byName.get(n)).filter((f) => f !== undefined);
  }, [scrape]);

  if (families.length === 0) {
    return (
      <p className="text-sm text-slate-600 dark:text-slate-400" data-testid="metrics-panel-empty">
        No vnprox metrics in this scrape.
      </p>
    );
  }

  return (
    <div className="flex flex-col gap-3" data-testid="metrics-panel">
      {families.map((f) => (
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
                    <td className="pr-3 text-slate-600 dark:text-slate-400">{labelText || "—"}</td>
                    <td className="tabular-nums font-medium">{s.value}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </section>
      ))}
    </div>
  );
}
