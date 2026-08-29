// SPDX-License-Identifier: Apache-2.0

// T-1202: the global (cross-cluster) map view — one capsule per attached
// cluster, the outermost level of the map. Clicking a capsule drills into
// that cluster's ordinary topology (the gate swaps this view for the
// unchanged TopologyPage, scoped by `?cluster=<id>`). This view renders only
// when >=2 clusters are attached; the gate never mounts it otherwise.
//
// T-3909 extends the capsule grid into a stitched hub-and-spoke graph: the
// local vnprox instance is the hub (every WireGuard interconnect tunnel a
// cluster names originates here — see interconnects.ts), and one edge is
// drawn to every attached cluster that has an effective tunnel linkage
// (`FederationCluster.wgTunnelId`). Clusters with no linked tunnel get no
// edge — federation without a WireGuard interconnect is a perfectly normal,
// unlinked configuration, not a missing/unknown one. Fetching the two extra
// data sources (the cluster registry's tunnel linkage, and this node's own
// live WireGuard tunnel state) lives entirely inside this view, so the gate
// and its existing tests are untouched: they mock this component wholesale.
import { useMemo } from "react";
import type { ClusterSummary, FederationCluster } from "../../api/federation";
import { HelpAnchor } from "../../help/HelpAnchor";
import { useWireGuardTunnelsQuery } from "../../wireguard/wgTunnelsQuery";
import { ClusterCapsule } from "./ClusterCapsule";
import { HUB_POSITION, radialClusterLayout } from "./clusterLayout";
import { useFederationClustersQuery } from "./federationQueries";
import { deriveInterconnects, type ClusterInterconnect, type InterconnectState } from "./interconnects";

export interface GlobalTopologyViewProps {
  clusters: ClusterSummary[];
  partial?: boolean;
  onDrill: (clusterId: string) => void;
}

const INTERCONNECT_ICON: Record<InterconnectState, string> = { up: "●", down: "✕", unknown: "?" };

const INTERCONNECT_LIST_ITEM_CLASS: Record<InterconnectState, string> = {
  up: "border-emerald-300 bg-emerald-50 text-emerald-800 dark:border-emerald-800 dark:bg-emerald-900/30 dark:text-emerald-200",
  down: "border-red-300 bg-red-50 text-red-800 dark:border-red-800 dark:bg-red-900/30 dark:text-red-200",
  unknown: "border-slate-300 bg-slate-100 text-slate-700 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-300",
};

/** Every edge state's line paint, mirroring canvasDraw.ts's own statusBorder/
 * dash convention (unknown -> dashed; down -> thicker line) so edge state is
 * never colour alone even on the decorative SVG line itself — the line is
 * additionally `aria-hidden` because the "WireGuard interconnects" list below
 * it is the actual accessible statement of the same facts, in text. */
const EDGE_STROKE: Record<InterconnectState, string> = {
  up: "#10b981", // emerald-500
  down: "#ef4444", // red-500
  unknown: "#94a3b8", // slate-400
};

function edgeDashArray(state: InterconnectState): string | undefined {
  if (state === "unknown") return "2 2";
  return undefined;
}

/** Joins each cluster capsule to its radial layout point and, where one
 * exists, its interconnect — a single pass so the render below never has to
 * re-derive index alignment. */
interface PositionedCluster {
  summary: ClusterSummary;
  x: number;
  y: number;
  interconnect?: ClusterInterconnect;
}

/** Narrows PositionedCluster.interconnect from optional to present — used to
 * build the edge/legend lists without an `?? "unknown"` fallback that would
 * otherwise be needed on every read of a filtered array TypeScript can't
 * itself see was filtered by presence. */
function hasInterconnect(p: PositionedCluster): p is PositionedCluster & { interconnect: ClusterInterconnect } {
  return p.interconnect !== undefined;
}

export function GlobalTopologyView({ clusters, partial, onDrill }: GlobalTopologyViewProps) {
  // T-3909's two extra, independently-fetched data sources. Neither gates
  // rendering the capsule grid itself: a failed/slow read here degrades only
  // the edge layer (every linked cluster's interconnect reports "unknown"),
  // never the capsules — the same per-surface isolation every other
  // federation read already carries.
  const { data: registryClusters } = useFederationClustersQuery();
  const { data: tunnels, isError: tunnelsErrored } = useWireGuardTunnelsQuery(true);

  const interconnects = useMemo<ClusterInterconnect[]>(
    () => deriveInterconnects(registryClusters ?? ([] as FederationCluster[]), tunnels, tunnelsErrored),
    [registryClusters, tunnels, tunnelsErrored],
  );
  const interconnectByCluster = useMemo(() => new Map(interconnects.map((ic) => [ic.clusterId, ic])), [interconnects]);

  const points = useMemo(() => radialClusterLayout(clusters.length), [clusters.length]);
  const positioned: PositionedCluster[] = clusters.map((summary, i) => ({
    summary,
    x: points[i]?.x ?? HUB_POSITION.x,
    y: points[i]?.y ?? HUB_POSITION.y,
    interconnect: interconnectByCluster.get(summary.clusterId),
  }));
  const edges = positioned.filter(hasInterconnect);

  return (
    <section aria-label="Global cluster map" className="flex h-full flex-col gap-4 overflow-y-auto p-6">
      <header className="flex items-baseline justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-1.5 text-lg font-semibold text-slate-800 dark:text-slate-100">
            Global map
            <HelpAnchor topic="federation" />
          </h1>
          <p className="text-sm text-slate-600 dark:text-slate-400">
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

      <div className="relative min-h-[26rem] flex-1">
        <svg
          aria-hidden="true"
          className="pointer-events-none absolute inset-0 h-full w-full"
          viewBox="0 0 100 100"
          preserveAspectRatio="none"
        >
          {edges.map((p) => (
            <line
              key={p.summary.clusterId}
              x1={HUB_POSITION.x}
              y1={HUB_POSITION.y}
              x2={p.x}
              y2={p.y}
              stroke={EDGE_STROKE[p.interconnect.state]}
              strokeWidth={p.interconnect.state === "down" ? 1 : 0.6}
              strokeDasharray={edgeDashArray(p.interconnect.state)}
              vectorEffect="non-scaling-stroke"
            />
          ))}
          <circle cx={HUB_POSITION.x} cy={HUB_POSITION.y} r={1.6} className="fill-slate-400 dark:fill-slate-500" />
        </svg>

        <div
          className="absolute flex -translate-x-1/2 -translate-y-1/2 items-center gap-1 rounded-full border border-slate-300 bg-white px-2 py-1 text-xs font-medium text-slate-600 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-400"
          style={{ left: `${String(HUB_POSITION.x)}%`, top: `${String(HUB_POSITION.y)}%` }}
          title="This vnprox instance — the WireGuard interconnect hub every linked tunnel originates from"
        >
          This instance
        </div>

        {positioned.map((p) => (
          <div
            key={p.summary.clusterId}
            className="absolute -translate-x-1/2 -translate-y-1/2"
            style={{ left: `${String(p.x)}%`, top: `${String(p.y)}%` }}
          >
            <ClusterCapsule summary={p.summary} onDrill={onDrill} interconnect={p.interconnect} />
          </div>
        ))}
      </div>

      {edges.length > 0 && (
        <div aria-label="WireGuard interconnects" role="list" className="flex flex-wrap gap-2 text-xs">
          {edges.map((p) => {
            const ic = p.interconnect;
            return (
              <div
                key={ic.clusterId}
                role="listitem"
                className={`flex items-center gap-1.5 rounded border px-2 py-1 ${INTERCONNECT_LIST_ITEM_CLASS[ic.state]}`}
              >
                <span aria-hidden="true">{INTERCONNECT_ICON[ic.state]}</span>
                <span className="font-medium">{ic.clusterName}</span>
                <span>
                  {ic.state === "up" && "interconnect up"}
                  {ic.state === "down" && "interconnect down"}
                  {ic.state === "unknown" && "interconnect state unknown"}
                </span>
              </div>
            );
          })}
        </div>
      )}
    </section>
  );
}
