// T-1202: one cluster capsule in the global (outermost-LOD) map view. Built
// on T-902's summary-capsule idea — a per-cluster rollup the operator clicks
// to drill into that cluster's ordinary topology. Name, aggregate findings
// count, drift status, and an unreachable indicator; an unreachable cluster
// renders greyed/degraded rather than being dropped (docs/features/
// topology.md §5's "greyed from last-known data" convention, lifted to the
// cluster level).
import clsx from "clsx";
import type { ClusterSummary } from "../../api/federation";

export interface ClusterCapsuleProps {
  summary: ClusterSummary;
  onDrill: (clusterId: string) => void;
}

export function ClusterCapsule({ summary, onDrill }: ClusterCapsuleProps) {
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
          : "border-slate-200 bg-slate-100 text-slate-400 opacity-70 dark:border-slate-800 dark:bg-slate-950",
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
          <dl className="grid grid-cols-2 gap-x-3 gap-y-1 text-xs text-slate-500 dark:text-slate-400">
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
        </>
      ) : (
        <p className="text-xs">Last aggregation could not reach this cluster.</p>
      )}
    </button>
  );
}
