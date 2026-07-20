// T-1202: the global (cross-cluster) map view — one capsule per attached
// cluster, the outermost level of the map. Clicking a capsule drills into
// that cluster's ordinary topology (the gate swaps this view for the
// unchanged TopologyPage, scoped by `?cluster=<id>`). This view renders only
// when >=2 clusters are attached; the gate never mounts it otherwise.
import type { ClusterSummary } from "../../api/federation";
import { ClusterCapsule } from "./ClusterCapsule";

export interface GlobalTopologyViewProps {
  clusters: ClusterSummary[];
  partial?: boolean;
  onDrill: (clusterId: string) => void;
}

export function GlobalTopologyView({ clusters, partial, onDrill }: GlobalTopologyViewProps) {
  return (
    <section aria-label="Global cluster map" className="flex h-full flex-col gap-4 p-6">
      <header className="flex items-baseline justify-between gap-3">
        <div>
          <h1 className="text-lg font-semibold text-slate-800 dark:text-slate-100">Global map</h1>
          <p className="text-sm text-slate-500 dark:text-slate-400">
            {clusters.length} attached clusters — select one to open its topology.
          </p>
        </div>
        {partial && (
          <span
            className="rounded bg-amber-100 px-2 py-1 text-xs font-medium text-amber-800 dark:bg-amber-900/40 dark:text-amber-200"
            role="status"
          >
            Some clusters were unreachable in the last pass.
          </span>
        )}
      </header>
      <div className="flex flex-wrap gap-4">
        {clusters.map((c) => (
          <ClusterCapsule key={c.clusterId} summary={c} onDrill={onDrill} />
        ))}
      </div>
    </section>
  );
}
