// T-602's unified findings stream view: fetches GET /findings, stays live
// via the findings.changed WS bridge, offers the source/severity/node
// filter controls (AC2), and wires "Create fixing changeset" to
// POST /findings/{id}/fix + opening the resulting draft in the changeset
// drawer for review — the same container/presentational split
// drift/DriftFindingsPanel.tsx established, now built on FindingsList
// directly rather than that drift-specific panel (which this component
// supersedes on ToolsPage — see that page's own doc comment).
import { useMemo, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { useChangesetDrawerStore } from "../changesets/store";
import { useToast } from "../components/Toast";
import type { FindingSource, Severity } from "../api/types";
import { useMgmtWizardStore } from "../mgmt/mgmtWizardStore";
import { mgmtStrings } from "../mgmt/strings";
import { FindingsList } from "./FindingsList";
import { EMPTY_FILTER, filterFindings, nodesIn, type FindingsFilterState } from "./filters";
import { FINDINGS_QUERY_KEY, useFindingsQuery, useFindingsWsBridge, useFixFindingMutation } from "./queries";

const SOURCE_LABELS: Record<FindingSource, string> = {
  drift: "Drift",
  lldp: "LLDP",
  ipam: "IPAM",
  health: "Health",
  // T-806: sim_divergence findings from POST /simulate/verify's "Verify
  // live" action.
  probe: "Verify live",
};

const SEVERITY_LABELS: Record<Severity, string> = {
  error: "Error",
  warning: "Warning",
  info: "Info",
};

export function FindingsStreamPanel() {
  const { data: findings, isLoading, error } = useFindingsQuery();
  const fixMutation = useFixFindingMutation();
  const navigate = useNavigate();
  const openMgmtWizard = useMgmtWizardStore((s) => s.open);
  const setActiveId = useChangesetDrawerStore((s) => s.setActiveId);
  const queryClient = useQueryClient();
  const { toast } = useToast();
  const [fixingId, setFixingId] = useState<string | undefined>(undefined);
  const [filter, setFilter] = useState<FindingsFilterState>(EMPTY_FILTER);

  useFindingsWsBridge();

  const all = useMemo(() => findings ?? [], [findings]);
  const filtered = useMemo(() => filterFindings(all, filter), [all, filter]);
  const availableNodes = useMemo(() => nodesIn(all), [all]);

  async function handleFix(id: string): Promise<void> {
    setFixingId(id);
    try {
      const changeset = await fixMutation.mutateAsync(id);
      setActiveId(changeset.id);
      void queryClient.invalidateQueries({ queryKey: FINDINGS_QUERY_KEY });
      toast({ title: "Fixing changeset created", description: "Review it in the drawer before applying.", variant: "success" });
    } catch {
      toast({ title: "Could not create fixing changeset", variant: "error" });
    } finally {
      setFixingId(undefined);
    }
  }

  if (isLoading) {
    return <p className="text-sm text-slate-500 dark:text-slate-400">Loading findings…</p>;
  }
  if (error) {
    return <p className="text-sm text-red-600 dark:text-red-400">Could not load findings.</p>;
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2" role="group" aria-label="Filter findings">
        <label className="flex items-center gap-1 text-xs text-slate-500 dark:text-slate-400">
          Source
          <select
            aria-label="Filter by source"
            value={filter.source}
            onChange={(e) => {
              setFilter((f) => ({ ...f, source: e.target.value as FindingSource | "" }));
            }}
            className="rounded border border-slate-200 bg-transparent px-1.5 py-0.5 text-xs outline-none focus:border-accent-500 dark:border-slate-700"
          >
            <option value="">All</option>
            {(Object.keys(SOURCE_LABELS) as FindingSource[]).map((s) => (
              <option key={s} value={s}>
                {SOURCE_LABELS[s]}
              </option>
            ))}
          </select>
        </label>
        <label className="flex items-center gap-1 text-xs text-slate-500 dark:text-slate-400">
          Severity
          <select
            aria-label="Filter by severity"
            value={filter.severity}
            onChange={(e) => {
              setFilter((f) => ({ ...f, severity: e.target.value as Severity | "" }));
            }}
            className="rounded border border-slate-200 bg-transparent px-1.5 py-0.5 text-xs outline-none focus:border-accent-500 dark:border-slate-700"
          >
            <option value="">All</option>
            {(Object.keys(SEVERITY_LABELS) as Severity[]).map((s) => (
              <option key={s} value={s}>
                {SEVERITY_LABELS[s]}
              </option>
            ))}
          </select>
        </label>
        <label className="flex items-center gap-1 text-xs text-slate-500 dark:text-slate-400">
          Node
          <select
            aria-label="Filter by node"
            value={filter.node}
            onChange={(e) => {
              setFilter((f) => ({ ...f, node: e.target.value }));
            }}
            className="rounded border border-slate-200 bg-transparent px-1.5 py-0.5 text-xs outline-none focus:border-accent-500 dark:border-slate-700"
          >
            <option value="">All</option>
            {availableNodes.map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </select>
        </label>
        {(filter.source || filter.severity || filter.node) && (
          <button
            type="button"
            onClick={() => {
              setFilter(EMPTY_FILTER);
            }}
            className="text-xs text-accent-600 underline dark:text-accent-400"
          >
            Clear filters
          </button>
        )}
      </div>

      <FindingsList
        findings={filtered.map((f) => {
          // T-703: the mgmt_single_path finding (detection-only, so never
          // "fixable" via a computed changeset) launches the guided
          // management-redundancy wizard for the named node instead.
          const mgmtNode = f.check === "mgmt_single_path" ? f.nodes[0] : undefined;
          // T-806: sim_divergence's DocsLink is deliberately the simulator
          // deep link, not a docs page (see cmd/vnproxd's
          // simDivergenceDeepLink doc comment) — surface it as a "View in
          // simulator" action rather than the generic docs-link styling
          // (which this component doesn't render anywhere today) so the
          // deep link is actually reachable from the unified stream, not
          // just from the simulator's own result panel.
          const simDivergenceLink = f.check === "sim_divergence" && f.docsLink ? f.docsLink : undefined;
          return {
            id: f.id,
            severity: f.severity,
            detail: f.detail,
            nodes: f.nodes,
            refs: f.refs,
            fixable: f.fixable,
            category: `${SOURCE_LABELS[f.source]} · ${f.check}`,
            action: mgmtNode
              ? { label: mgmtStrings.launch.button, onClick: () => { openMgmtWizard({ node: mgmtNode }); } }
              : simDivergenceLink
                ? { label: "View in simulator", onClick: () => { void navigate(simDivergenceLink); } }
                : undefined,
          };
        })}
        onFix={(id) => {
          void handleFix(id);
        }}
        fixingId={fixingId}
        emptyTitle={all.length === 0 ? "No findings" : "No findings match this filter"}
        emptyDescription={
          all.length === 0
            ? "The cluster is healthy: no drift, LLDP mismatches, or health-check breaches detected."
            : "Try clearing a filter."
        }
      />
    </div>
  );
}
