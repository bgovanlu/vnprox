// Tools page: T-208's raw interfaces editor (the power-user escape hatch,
// docs/features/change-management.md §7) and T-305's drift findings
// stream. The path simulator lands in a later task.
import { useEffect, useMemo, useState } from "react";
import { RawEditorPanel } from "../changesets/rawEditor/RawEditorPanel";
import { EmptyState } from "../components/EmptyState";
import { DriftFindingsPanel } from "../drift/DriftFindingsPanel";
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

      <div>
        <h2 className="text-lg font-semibold">Export documentation</h2>
        <p className="mb-3 text-sm text-slate-500 dark:text-slate-400">
          A timestamped as-built document of the cluster network (docs/features/blueprints.md §4): rendered topology,
          per-node interface tables, VLAN matrix, SDN inventory, firewall summaries, and the LLDP wiring table. This
          is a read-only, GET-only export — no capability gating needed beyond ordinary network-read access.
        </p>
        <div className="flex gap-2">
          {/* Plain <a download> rather than routing through apiFetch: these
           * are file downloads (text/markdown, text/html), not JSON, and a
           * normal same-origin navigation/click already sends the session
           * cookie — see docs/api.md's "Config documentation export
           * (T-605)" section. Also the most Playwright-testable shape
           * (page.waitForEvent("download")). */}
          <a
            href="/api/v1/export/doc?format=md"
            download
            className="inline-flex h-9 items-center justify-center rounded-md bg-slate-200 px-3.5 text-sm font-medium text-slate-900 hover:bg-slate-300 dark:bg-slate-800 dark:text-slate-100 dark:hover:bg-slate-700"
          >
            Download Markdown
          </a>
          <a
            href="/api/v1/export/doc?format=html"
            download
            className="inline-flex h-9 items-center justify-center rounded-md bg-slate-200 px-3.5 text-sm font-medium text-slate-900 hover:bg-slate-300 dark:bg-slate-800 dark:text-slate-100 dark:hover:bg-slate-700"
          >
            Download HTML
          </a>
        </div>
      </div>

      <p className="text-xs text-slate-400 dark:text-slate-500">
        The path simulator ("why can't VM A reach VM B?") lands in a later task.
      </p>
    </div>
  );
}
