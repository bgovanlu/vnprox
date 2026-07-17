// Top-talkers tile (T-904 deliverables: "top talkers (per
// docs/features/monitoring.md §3: rank guest-NIC refs on the busiest
// bridge(s) by GET /metrics/live rates — a client-side computation over an
// existing route, no new backend aggregation)"). Reuses
// topology/queries.ts's useTopologyQuery (cache-shared with every other
// topology consumer) to find bridge/guest-nic refs, and
// topology/metricsQueries.ts's useLiveMetrics (the exact hook the map's
// traffic-paint mode and the inspector's Metrics tab already use) to
// sample them — the ranking itself is pure client-side math in
// topTalkers.ts.
import { useMemo } from "react";
import { useNavigate } from "react-router-dom";
import { useTopologyQuery } from "../topology/queries";
import { useLiveMetrics } from "../topology/metricsQueries";
import { formatBps } from "../topology/metricsFormat";
import { bridgeGuestGroups, computeTopTalkers, refsToSample } from "./topTalkers";
import { DashboardTile } from "./DashboardTile";

export function TopTalkersTile() {
  const navigate = useNavigate();
  const { data: topology, isLoading, error } = useTopologyQuery();

  const groups = useMemo(
    () => bridgeGuestGroups(topology?.nodes ?? [], topology?.edges ?? []),
    [topology],
  );
  const refs = useMemo(() => refsToSample(groups), [groups]);
  const live = useLiveMetrics(refs, refs.length > 0);

  const labelOf = useMemo(() => {
    const byId = new Map((topology?.nodes ?? []).map((n) => [n.id, n.label]));
    return (ref: string) => byId.get(ref) ?? ref;
  }, [topology]);

  const result = useMemo(() => computeTopTalkers(groups, live, labelOf), [groups, live, labelOf]);

  return (
    <DashboardTile
      title="Top talkers"
      description="Busiest bridge's guest NICs, ranked by current throughput."
      isLoading={isLoading}
      error={error ? "Could not load topology." : undefined}
      empty={
        !result
          ? { title: "No measurable traffic", description: "No guest NIC traffic observed on any bridge yet." }
          : undefined
      }
      onOpen={() => {
        void navigate("/topology");
      }}
      openLabel="Open topology"
    >
      {result ? (
        <div className="flex flex-col gap-1.5">
          <p className="text-xs text-slate-500 dark:text-slate-400">
            Busiest bridge: <span className="font-medium text-slate-700 dark:text-slate-200">{result.bridgeLabel}</span>
          </p>
          <ul className="flex flex-col gap-1">
            {result.talkers.map((t) => (
              <li key={t.ref} className="flex items-center justify-between gap-2 text-sm">
                <span className="truncate text-slate-700 dark:text-slate-200">{t.label}</span>
                <span className="shrink-0 tabular-nums text-slate-500 dark:text-slate-400">
                  {formatBps(t.rxBps + t.txBps)}
                </span>
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </DashboardTile>
  );
}
