// Tools page: T-208's raw interfaces editor (the power-user escape hatch,
// docs/features/change-management.md §7), T-305's drift findings stream,
// and T-505's firewall log viewer. The path simulator lands in a later
// task.
import { useEffect, useMemo, useState } from "react";
import { RawEditorPanel } from "../changesets/rawEditor/RawEditorPanel";
import { EmptyState } from "../components/EmptyState";
import { DriftFindingsPanel } from "../drift/DriftFindingsPanel";
import { FwLogViewer } from "../fwlog/FwLogViewer";
import { MacFdbBrowser } from "../tools/MacFdbBrowser";
import { useTopologyQuery } from "../topology/queries";

export function ToolsPage() {
  const { data: topology } = useTopologyQuery();
  const nodes = useMemo(
    () => Array.from(new Set(topology?.nodes.map((n) => n.nodeGroup).filter(Boolean) ?? [])).sort(),
    [topology],
  );
  const [node, setNode] = useState<string>("");

  useEffect(() => {
    const first = nodes[0];
    if (!node && first !== undefined) {
      setNode(first);
    }
  }, [node, nodes]);

  return (
    <div className="flex h-full flex-col gap-4">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h1 className="text-xl font-semibold">Tools</h1>
          <p className="text-sm text-slate-500 dark:text-slate-400">
            Raw <code>/etc/network/interfaces</code> editor. Saving still goes through the normal changeset
            review/apply flow — this is a different way to author the same change, not a bypass of it.
          </p>
        </div>
        {nodes.length > 0 && (
          <label className="flex items-center gap-2 text-sm">
            <span className="text-slate-500 dark:text-slate-400">Node</span>
            <select
              className="h-9 rounded-md border border-slate-300 bg-white px-2 text-sm dark:border-slate-700 dark:bg-slate-900"
              value={node}
              onChange={(e) => {
                setNode(e.target.value);
              }}
            >
              {nodes.map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
            </select>
          </label>
        )}
      </div>

      {node ? (
        <RawEditorPanel key={node} node={node} />
      ) : (
        <EmptyState title="No nodes yet" description="The raw editor needs at least one cluster node to be discovered." />
      )}

      <hr className="border-slate-200 dark:border-slate-800" />

      <div>
        <h2 className="text-lg font-semibold">Drift findings</h2>
        <p className="mb-3 text-sm text-slate-500 dark:text-slate-400">
          Cross-node consistency checks, re-evaluated every 30s (docs/features/topology.md §6). Drifted entities
          are also outlined with a dashed border on the topology map.
        </p>
        <DriftFindingsPanel />
      </div>

      <hr className="border-slate-200 dark:border-slate-800" />

      <MacFdbBrowser />

      <hr className="border-slate-200 dark:border-slate-800" />

      <FwLogViewer />

      <p className="text-xs text-slate-400 dark:text-slate-500">
        The path simulator ("why can't VM A reach VM B?") lands in a later task.
      </p>
    </div>
  );
}
