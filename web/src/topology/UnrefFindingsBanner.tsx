// T-3501 AC5: findings whose Refs are empty (health/service_down for a bare
// service name like dnsmasq/frr on the reference node — nothing to name)
// have nothing to paint on the map. They must not be invisible, so this
// banner is their home: rendered once in TopologyPage.tsx, above both the
// Switch and Graph views, so the two views can never contradict each other
// about it (there is only one component, not a per-view rendering). Mirrors
// StalenessBanner.tsx's shape/placement exactly — same pattern, different
// data.
import type { UnrefFinding } from "../api/types";
import { findingBadgeClass, findingChipText, worstFindingSeverity } from "./findingBadges";

export interface UnrefFindingsBannerProps {
  findings: readonly UnrefFinding[] | undefined;
}

export function UnrefFindingsBanner({ findings }: UnrefFindingsBannerProps) {
  if (!findings || findings.length === 0) return null;
  const worst = worstFindingSeverity(findings.map((f) => `finding:${f.source}:${f.severity}`)) ?? "info";
  return (
    <div
      role="status"
      className={`rounded-md border px-3 py-2 text-xs ${
        worst === "error"
          ? "border-red-300 dark:border-red-700"
          : worst === "warning"
            ? "border-amber-300 dark:border-amber-700"
            : "border-slate-300 dark:border-slate-700"
      } bg-slate-50 dark:bg-slate-900`}
    >
      <p className="font-medium text-slate-700 dark:text-slate-200">
        {findings.length === 1
          ? "1 finding is not tied to any map entity:"
          : `${String(findings.length)} findings are not tied to any map entity:`}
      </p>
      <ul className="mt-1 space-y-0.5">
        {findings.map((f) => (
          <li key={`${f.source}:${f.check}:${f.nodes.join(",")}`} className="flex flex-wrap items-center gap-1.5">
            <span
              className={`rounded px-1 py-0.5 text-[10px] font-medium ${findingBadgeClass(f.severity)}`}
            >
              {findingChipText({ source: f.source, severity: f.severity })}
            </span>
            <span className="text-slate-600 dark:text-slate-300">{f.detail || f.check}</span>
            {f.nodes.length > 0 && (
              <span className="text-slate-500 dark:text-slate-400">
                (on {f.nodes.join(", ")})
              </span>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}
