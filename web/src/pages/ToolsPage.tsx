// Tools page: T-504's path simulator (docs/user-guide.md §3: "Tools ->
// Path simulator"), T-208's raw interfaces editor (the power-user escape
// hatch, docs/features/change-management.md §7), T-602's unified findings
// stream (T-305's drift-only panel is superseded here — the unified stream
// already includes every drift finding, source-tagged, plus LLDP/health),
// and T-505's firewall log viewer.
//
// T-909: this is one of the two routes (with Dashboard) reachable on a
// narrow viewport — but only its "Findings" facet, per the task card
// ("the reachable page set on narrow viewports is restricted to Dashboard,
// Findings (/tools), and the changeset detail/confirm/rollback view"). The
// simulator, raw editor, MAC/FDB browser, firewall log viewer, and doc
// export are all desktop-only surfaces bundled onto this same route, so
// narrow width renders a restricted view instead of gating the whole
// route: the findings stream stays (read-only — see
// FindingsStreamPanel.tsx's own narrow handling, which disables the
// fix/wizard-launch actions rather than hiding the findings themselves),
// and everything else is replaced by one DesktopOnlyNotice rather than
// silently vanishing.
import { useEffect, useMemo, useState } from "react";
import { RawEditorPanel } from "../changesets/rawEditor/RawEditorPanel";
import { EmptyState } from "../components/EmptyState";
import { FindingsStreamPanel } from "../findings/FindingsStreamPanel";
import { FwLogViewer } from "../fwlog/FwLogViewer";
import { useNarrowViewport } from "../lib/useNarrowViewport";
import { SimulatorPage } from "../simulator/SimulatorPage";
import { MacFdbBrowser } from "../tools/MacFdbBrowser";
import { useTopologyQuery } from "../topology/queries";

export function ToolsPage() {
  const narrow = useNarrowViewport();
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

  if (narrow) {
    return (
      <div className="flex h-full flex-col gap-4">
        <div>
          <h1 className="text-xl font-semibold">Findings</h1>
          <p className="text-sm text-slate-500 dark:text-slate-400">
            Drift, LLDP VLAN mismatches, IPAM conflicts, and continuous health checks, read-only from here.
          </p>
        </div>
        <FindingsStreamPanel />
        {/* T-909: unlike DesktopOnlyRoute's full-page notice (which links
         * back to Dashboard/Findings — useful when you've landed on a page
         * you can't use at all), this is already the Findings page, so it
         * just names what's hidden instead of dangling a redundant
         * "go to Findings" link. */}
        <EmptyState
          title="The rest of Tools needs a larger screen"
          description="The path simulator, raw interfaces editor, MAC/FDB search, firewall log viewer, and documentation export need a desktop-sized screen. Creating a fixing changeset or launching a redundancy wizard from a finding does too — a pending changeset's confirm/roll back controls still work from here."
          density="compact"
        />
      </div>
    );
  }

  return (
    <div className="flex h-full flex-col gap-4">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h1 className="text-xl font-semibold">Tools</h1>
          <p className="text-sm text-slate-500 dark:text-slate-400">
            Path simulator, the raw <code>/etc/network/interfaces</code> editor, drift findings, MAC/FDB search, and
            the firewall log viewer. The node picker below applies to the raw editor only — saving there still goes
            through the normal changeset review/apply flow, never a bypass.
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

      <SimulatorPage />

      <hr className="border-slate-200 dark:border-slate-800" />

      {node ? (
        <RawEditorPanel key={node} node={node} />
      ) : (
        <EmptyState title="No nodes yet" description="The raw editor needs at least one cluster node to be discovered." />
      )}

      <hr className="border-slate-200 dark:border-slate-800" />

      <div>
        <h2 className="text-lg font-semibold">Findings</h2>
        <p className="mb-3 text-sm text-slate-500 dark:text-slate-400">
          One stream for drift, LLDP VLAN mismatches, IPAM conflicts, and continuous health checks
          (docs/features/monitoring.md §5), re-evaluated on a ~30s cycle. Affected entities are also outlined with a
          dashed border on the topology map.
        </p>
        <FindingsStreamPanel />
      </div>

      <hr className="border-slate-200 dark:border-slate-800" />

      <MacFdbBrowser />

      <hr className="border-slate-200 dark:border-slate-800" />

      <FwLogViewer />

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
    </div>
  );
}
