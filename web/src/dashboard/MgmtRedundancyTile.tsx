// SPDX-License-Identifier: Apache-2.0

// Management-path redundancy tile (T-904 deliverables: "mgmt-path
// redundancy per node from GET /protected-interfaces/status, counting
// non-redundant nodes"). Reuses topology/queries.ts's useMgmtStatusQuery —
// the exact same T-702 query the topology inspector's "Management path"
// tab and the Management page (web/src/mgmt/ManagementPage.tsx) already
// call, so this tile shares that cached fetch rather than adding a second
// one.
import { useMemo } from "react";
import { useNavigate } from "react-router-dom";
import { useMgmtStatusQuery } from "../topology/queries";
import { DashboardTile } from "./DashboardTile";

export function MgmtRedundancyTile() {
  const navigate = useNavigate();
  const { data: mgmtStatus, isLoading, error } = useMgmtStatusQuery();

  const { totalNodes, nonRedundantNodes } = useMemo(() => {
    const entries = Object.entries(mgmtStatus?.nodes ?? {});
    const nonRedundant = entries.filter(([, paths]) => paths.some((p) => !p.redundant)).map(([node]) => node);
    return { totalNodes: entries.length, nonRedundantNodes: nonRedundant.sort() };
  }, [mgmtStatus]);

  return (
    <DashboardTile
      title="Management-path redundancy"
      description={
        mgmtStatus?.source === "detected"
          ? "Live-detected paths — protected interfaces were never confirmed during onboarding."
          : "Per-node management/corosync path redundancy."
      }
      isLoading={isLoading}
      error={error ? "Could not load management-path status." : undefined}
      empty={
        totalNodes > 0 && nonRedundantNodes.length === 0
          ? { title: "All nodes redundant", description: `Every one of ${String(totalNodes)} nodes has a redundant management path.` }
          : totalNodes === 0
            ? { title: "No nodes yet", description: "No cluster nodes discovered yet." }
            : undefined
      }
      onOpen={() => {
        void navigate("/management");
      }}
      openLabel="Open management"
    >
      <p className="text-sm text-slate-700 dark:text-slate-200">
        <span className="font-semibold tabular-nums text-amber-700 dark:text-amber-400">
          {nonRedundantNodes.length}
        </span>{" "}
        of <span className="font-semibold tabular-nums">{totalNodes}</span>{" "}
        {totalNodes === 1 ? "node has" : "nodes have"} a single-path management uplink
      </p>
      {nonRedundantNodes.length > 0 ? (
        <p className="mt-1 truncate text-xs text-slate-500 dark:text-slate-400">{nonRedundantNodes.join(", ")}</p>
      ) : null}
    </DashboardTile>
  );
}
