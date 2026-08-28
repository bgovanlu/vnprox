// SPDX-License-Identifier: Apache-2.0

// T-1202: one cluster capsule in the global (outermost-LOD) map view. Built
// on T-902's summary-capsule idea — a per-cluster rollup the operator clicks
// to drill into that cluster's ordinary topology. Name, aggregate findings
// count, drift status, and an unreachable indicator; an unreachable cluster
// renders greyed/degraded rather than being dropped (docs/features/
// topology.md §5's "greyed from last-known data" convention, lifted to the
// cluster level).
import clsx from "clsx";
import type { ClusterSummary } from "../../api/federation";
import type { ClusterInterconnect } from "./interconnects";

const INTERCONNECT_BADGE_CLASS: Record<ClusterInterconnect["state"], string> = {
  up: "bg-emerald-100 text-emerald-800 dark:bg-emerald-900/40 dark:text-emerald-200",
  down: "bg-red-100 text-red-800 dark:bg-red-900/40 dark:text-red-200",
  unknown: "bg-slate-200 text-slate-700 dark:bg-slate-800 dark:text-slate-300",
};

/** Text-only labels — the badge's colour is never the sole carrier of which
 * of the three states applies (WCAG 1.4.1), matching the "findings"/"drift"
 * badges above it, which are text + colour, never colour alone. */
const INTERCONNECT_LABEL: Record<ClusterInterconnect["state"], string> = {
  up: "WG interconnect: up",
  down: "WG interconnect: down",
  unknown: "WG interconnect: unknown",
};

export interface ClusterCapsuleProps {
  summary: ClusterSummary;
  onDrill: (clusterId: string) => void;
  /** T-3909: this cluster's WireGuard interconnect state, if it has one
   * (clusters with no linked tunnel get no badge at all — undefined here
   * means "not tunnel-linked", not "unknown state"). Optional so every
   * existing caller/test that doesn't pass one is unaffected. */
  interconnect?: ClusterInterconnect;
}

export function ClusterCapsule({ summary, onDrill, interconnect }: ClusterCapsuleProps) {
  const { reachable, drift } = summary;
  return (
    <button
      type="button"
      onClick={() => {
        onDrill(summary.clusterId);
      }}
      aria-label={`Open cluster ${summary.clusterName}`}
      data-cluster-id={summary.clusterId}
      data-reachable={reachable}
      className={clsx(
        "flex w-56 flex-col gap-2 rounded-lg border p-4 text-left transition hover:border-accent-500 focus:border-accent-500 focus:outline-none",
        reachable
          ? "border-slate-300 bg-white dark:border-slate-700 dark:bg-slate-900"
          : // T-4216: was `text-slate-400`, which measured 2.37:1 in light mode
            // and, multiplied by this branch's own opacity-70, left the
            // UNREACHABLE cluster as the least readable thing on the page —
            // exactly inverted from what an operator needs to notice. The
            // dimming is kept (it is the affordance that says "not
            // reachable") but the text now sits on a role token that clears
            // AA before that multiplier is applied.
            "border-border bg-surface-sunken text-fg-subtle opacity-70 dark:bg-slate-950",
      )}
    >
      <div className="flex items-center justify-between gap-2">
        <span className="truncate font-medium text-slate-800 dark:text-slate-100">{summary.clusterName}</span>
        {!reachable && (
          <span
            className="shrink-0 rounded bg-slate-300 px-1.5 py-0.5 text-xs font-medium uppercase tracking-wide text-slate-700 dark:bg-slate-700 dark:text-slate-200"
            title="This cluster was unreachable in the last aggregation pass"
          >
            unreachable
          </span>
        )}
      </div>

      {reachable ? (
        <>
          <dl className="grid grid-cols-2 gap-x-3 gap-y-1 text-xs text-fg-subtle">
            <div className="flex items-center justify-between">
              <dt>Nodes</dt>
              <dd className="font-mono text-slate-700 dark:text-slate-200">
                {summary.nodesOnline}/{summary.nodes}
              </dd>
            </div>
            <div className="flex items-center justify-between">
              <dt>Guests</dt>
              <dd className="font-mono text-slate-700 dark:text-slate-200">{summary.guests}</dd>
            </div>
          </dl>
          <div className="flex items-center gap-2 text-xs">
            <span
              className={clsx(
                "rounded px-1.5 py-0.5 font-medium",
                summary.findings > 0
                  ? "bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-200"
                  : "bg-emerald-100 text-emerald-800 dark:bg-emerald-900/40 dark:text-emerald-200",
              )}
            >
              {summary.findings} {summary.findings === 1 ? "finding" : "findings"}
            </span>
            {drift && (
              <span className="rounded bg-amber-100 px-1.5 py-0.5 font-medium text-amber-800 dark:bg-amber-900/40 dark:text-amber-200">
                drift
              </span>
            )}
          </div>
          {interconnect && (
            <span
              className={clsx("w-fit rounded px-1.5 py-0.5 text-xs font-medium", INTERCONNECT_BADGE_CLASS[interconnect.state])}
              title={`WireGuard tunnel ${interconnect.tunnelId} (${interconnect.tunnelSource})`}
            >
              {INTERCONNECT_LABEL[interconnect.state]}
            </span>
          )}
        </>
      ) : (
        <>
          <p className="text-xs">Last aggregation could not reach this cluster.</p>
          {interconnect && (
            <span
              className={clsx("w-fit rounded px-1.5 py-0.5 text-xs font-medium", INTERCONNECT_BADGE_CLASS[interconnect.state])}
              title={`WireGuard tunnel ${interconnect.tunnelId} (${interconnect.tunnelSource})`}
            >
              {INTERCONNECT_LABEL[interconnect.state]}
            </span>
          )}
        </>
      )}
    </button>
  );
}
