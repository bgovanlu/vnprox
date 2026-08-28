// SPDX-License-Identifier: Apache-2.0

// Minimal Prometheus text-exposition parser (T-1706 Grafana metrics panel).
// It parses the subset of the exposition format vnprox's own exporter
// (T-1001, internal/api/metrics_exporter.go) emits: `# HELP`/`# TYPE`
// comment lines plus `name{label="v",...} value` sample lines. This is not a
// general-purpose Prometheus client — Grafana's own datasource does the real
// scraping in production — it exists so the panel can render (and be tested)
// against a fixture scrape body without a live Prometheus or Grafana.

/** One parsed sample: a metric name, its label set, and its numeric value. */
export interface PromSample {
  name: string;
  labels: Record<string, string>;
  value: number;
}

/** A parsed metric family: name, HELP text, TYPE, and its samples. */
export interface PromFamily {
  name: string;
  help: string;
  type: string;
  samples: PromSample[];
}

function parseLabels(raw: string): Record<string, string> {
  const labels: Record<string, string> = {};
  if (!raw) return labels;
  // Split on commas that are not inside quotes. The exporter only emits
  // simple quoted string values, so a conservative regex is enough.
  const re = /(\w+)="((?:[^"\\]|\\.)*)"/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(raw)) !== null) {
    const key = m[1];
    const val = m[2] ?? "";
    if (key === undefined) continue;
    labels[key] = val.replace(/\\"/g, '"').replace(/\\\\/g, "\\");
  }
  return labels;
}

/** Parses a Prometheus exposition-format body into metric families, keyed
 * and ordered by first appearance. Unknown/blank lines are skipped. */
export function parsePromText(body: string): PromFamily[] {
  const families = new Map<string, PromFamily>();
  const order: string[] = [];

  const family = (name: string): PromFamily => {
    let f = families.get(name);
    if (!f) {
      f = { name, help: "", type: "untyped", samples: [] };
      families.set(name, f);
      order.push(name);
    }
    return f;
  };

  for (const line of body.split("\n")) {
    const trimmed = line.trim();
    if (trimmed === "") continue;
    if (trimmed.startsWith("#")) {
      const help = /^#\s+HELP\s+(\S+)\s+(.*)$/.exec(trimmed);
      if (help?.[1] !== undefined) {
        family(help[1]).help = help[2] ?? "";
        continue;
      }
      const type = /^#\s+TYPE\s+(\S+)\s+(\S+)$/.exec(trimmed);
      if (type?.[1] !== undefined && type[2] !== undefined) {
        family(type[1]).type = type[2];
      }
      continue;
    }
    const sample = /^([a-zA-Z_:][\w:]*)(?:\{(.*)\})?\s+(-?[\d.eE+]+)$/.exec(trimmed);
    const name = sample?.[1];
    if (sample === null || name === undefined) continue;
    const value = Number(sample[3]);
    if (Number.isNaN(value)) continue;
    family(name).samples.push({
      name,
      labels: parseLabels(sample[2] ?? ""),
      value,
    });
  }

  return order.map((n) => {
    const f = families.get(n);
    if (!f) throw new Error(`unreachable: family ${n} missing`);
    return f;
  });
}
