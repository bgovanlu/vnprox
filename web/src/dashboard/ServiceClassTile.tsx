// Service-network traffic tile (T-1504 deliverable: "New home-dashboard
// tile (extends T-904's tile set): per-serviceClass bytes/sec breakdown
// over the retained flow window"). Reuses flows/flowsQueries.ts's
// useFlowsQuery (the exact hook the Flow Explorer itself uses) over the
// existing GET /flows route — no new backend aggregation, matching
// TopTalkersTile.tsx's own "client-side computation over an existing
// route" convention; the ranking/bucketing itself is pure client-side math
// in serviceClassBreakdown.ts.
import { useNavigate } from "react-router-dom";
import { useFlowsQuery } from "../flows/flowsQueries";
import { formatBps } from "../topology/metricsFormat";
import { computeServiceClassBreakdown } from "./serviceClassBreakdown";
import { DashboardTile } from "./DashboardTile";
import type { ServiceClass } from "../api/types";

// SERVICE_CLASS_QUERY_LIMIT: a deliberately larger page than the Flow
// Explorer's own default (100) so this tile's bytes/sec estimate spans a
// wider slice of the retained window rather than just the handful of
// newest records — still a single bounded GET /flows page, no new backend
// aggregation.
const SERVICE_CLASS_QUERY_LIMIT = 1000;

const SERVICE_CLASS_LABELS: Record<ServiceClass, string> = {
  migration: "Migration",
  backup: "Backup",
  "ceph-public": "Ceph (public)",
  "ceph-cluster": "Ceph (cluster)",
  corosync: "Corosync",
  unclassified: "Unclassified",
};

export function ServiceClassTile() {
  const navigate = useNavigate();
  const { data, isLoading, error } = useFlowsQuery({ limit: SERVICE_CLASS_QUERY_LIMIT });
  const result = computeServiceClassBreakdown(data?.items ?? []);

  return (
    <DashboardTile
      title="Service-network traffic"
      helpTopic="service-class-traffic"
      description="Migration/backup/Ceph/corosync traffic, ranked by bytes/sec over the recent flow window."
      isLoading={isLoading}
      error={error ? "Could not load flow records." : undefined}
      empty={
        !result
          ? {
              title: "No classified traffic",
              description:
                "No flow records carry a serviceClass yet — enable flow ingestion and a service-network source (corosync, migration, Ceph, backup).",
            }
          : undefined
      }
      onOpen={() => {
        void navigate("/flows");
      }}
      openLabel="Open flow explorer"
    >
      {result ? (
        <ul className="flex flex-col gap-1">
          {result.entries.map((e) => (
            <li key={e.serviceClass} className="flex items-center justify-between gap-2 text-sm">
              <span className="truncate text-slate-700 dark:text-slate-200">
                {SERVICE_CLASS_LABELS[e.serviceClass]}
              </span>
              <span className="shrink-0 tabular-nums text-slate-500 dark:text-slate-400">
                {formatBps(e.bytesPerSec * 8)}
              </span>
            </li>
          ))}
        </ul>
      ) : null}
    </DashboardTile>
  );
}
