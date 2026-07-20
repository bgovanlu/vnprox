// T-1202: the gate that decides whether the /topology route shows the
// global cross-cluster capsule view or a single cluster's ordinary topology.
//
// The invariant this enforces: federation is *invisible* until a second
// cluster is attached. With 0 or 1 attached clusters (or federation not
// wired at all — the query normalizes a 404 to zero clusters), this renders
// the existing <TopologyPage/> with no wrapper DOM whatsoever, so the page is
// byte-identical to its pre-Phase-12 self (T-1202 AC2). Only with >=2
// clusters attached, and no capsule yet drilled into, does the capsule view
// appear; drilling sets `?cluster=<id>`, which both this gate reads (to show
// the drilled topology + a back affordance) and TopologyPage reads (to fetch
// that cluster's projection).
import { useCallback } from "react";
import { useSearchParams } from "react-router-dom";
import { TopologyPage } from "../TopologyPage";
import { GlobalTopologyView } from "./GlobalTopologyView";
import { federationIsActive, useFederationTopologyQuery } from "./federationQueries";

export function GlobalTopologyGate() {
  const [searchParams, setSearchParams] = useSearchParams();
  const drilledClusterId = searchParams.get("cluster") ?? undefined;
  const { data, isLoading } = useFederationTopologyQuery();

  const drill = useCallback(
    (clusterId: string) => {
      const next = new URLSearchParams(searchParams);
      next.set("cluster", clusterId);
      setSearchParams(next);
    },
    [searchParams, setSearchParams],
  );

  const backToGlobal = useCallback(() => {
    const next = new URLSearchParams(searchParams);
    next.delete("cluster");
    setSearchParams(next);
  }, [searchParams, setSearchParams]);

  // While the (cheap, cached) federation summary is still in flight we hold
  // off rather than briefly flashing the local topology page and its fetch;
  // the summary settles quickly and a single-cluster deployment lands on the
  // unchanged page either way.
  if (isLoading) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-slate-400" aria-busy="true">
        Loading…
      </div>
    );
  }

  const active = federationIsActive(data);

  // Single-cluster (or unwired) deployment: render the ordinary topology page
  // with zero wrapper DOM — byte-identical to the pre-federation page.
  if (!active) {
    return <TopologyPage />;
  }

  // Multi-cluster, nothing drilled in yet: the global capsule view.
  if (!drilledClusterId) {
    return <GlobalTopologyView clusters={data?.clusters ?? []} partial={data?.partial} onDrill={drill} />;
  }

  // Drilled into a specific cluster: the unchanged topology page (scoped by
  // `?cluster` inside TopologyPage) plus a back affordance to the global map.
  const drilledName =
    data?.clusters.find((c) => c.clusterId === drilledClusterId)?.clusterName ?? drilledClusterId;
  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center gap-2 border-b border-slate-200 px-4 py-1.5 text-sm dark:border-slate-800">
        <button
          type="button"
          onClick={backToGlobal}
          className="rounded px-2 py-0.5 text-accent-600 hover:bg-slate-100 dark:text-accent-400 dark:hover:bg-slate-800"
        >
          ← Global map
        </button>
        <span className="text-slate-400">/</span>
        <span className="font-medium text-slate-700 dark:text-slate-200">{drilledName}</span>
      </div>
      <div className="min-h-0 flex-1">
        <TopologyPage />
      </div>
    </div>
  );
}
