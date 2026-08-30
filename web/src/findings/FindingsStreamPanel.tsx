// SPDX-License-Identifier: Apache-2.0

// T-602's unified findings stream view: fetches GET /findings, stays live
// via the findings.changed WS bridge, offers the source/severity/node
// filter controls (AC2), and wires "Create fixing changeset" to
// POST /findings/{id}/fix + opening the resulting draft in the changeset
// drawer for review — the same container/presentational split
// drift/DriftFindingsPanel.tsx established, now built on FindingsList
// directly rather than that drift-specific panel (which this component
// supersedes on ToolsPage — see that page's own doc comment).
//
// T-909: on a narrow viewport this is the read-only "Findings" surface —
// still shows the fix button (an explicit affordance beats silently hiding
// it, per the task card), but disabled with a tooltip via the same
// fixDisabledReason capability-gating pattern the read-only-session sweep
// already uses. The per-finding secondary action (the mgmt-redundancy
// wizard launch / "View in simulator" deep link) is redirected to an
// explanatory toast instead of actually opening a wizard or navigating —
// no new write capability (POST /findings/{id}/fix, opening the wizard) is
// reachable at narrow width.
import { useMemo, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearchParams } from "react-router-dom";
import { Button } from "../components/Button";
import { useChangesetDrawerStore } from "../changesets/store";
import { useToast } from "../components/Toast";
import type { FindingSource, Severity } from "../api/types";
import { HelpAnchor } from "../help/HelpAnchor";
import { useNarrowViewport } from "../lib/useNarrowViewport";
import { useMgmtWizardStore } from "../mgmt/mgmtWizardStore";
import { blastRadiusRequestFromFindingRefs } from "../topology/blastRadiusFocus";
import { useTopologyStore } from "../topology/store";
import { remediationAction, type RemediationContext } from "./remediation";
import { AckDialog } from "./AckDialog";
import { FindingsList } from "./FindingsList";
import { EMPTY_FILTER, filterFindings, nodesIn, type FindingsFilterState } from "./filters";
import {
  FINDINGS_QUERY_KEY,
  useAckFindingMutation,
  useBatchFixFindingsMutation,
  useFindingsQuery,
  useFindingsWsBridge,
  useFixFindingMutation,
  useUnackFindingMutation,
} from "./queries";

const NARROW_FIX_DISABLED_REASON = "Open on desktop to create a fixing changeset.";

// Debt sweep item 9 / `T-3004-followup-01` (2026-08-19): this Record's type
// (`Record<FindingSource, string>`) is what makes a source missing its label
// a compile error rather than a runtime `undefined` — TypeScript rejects an
// object literal assigned to a `Record<K, V>` that is missing (or adds) a
// key of `K`, so adding an 18th `internal/findings.Source` constant without
// adding its entry here fails `tsc`, it doesn't silently render
// `undefined · <check>` the way 11 of the 16 (now 17) sources previously did
// — see FindingSource's own doc comment in ../api/types.ts for how that
// happened.
const SOURCE_LABELS: Record<FindingSource, string> = {
  drift: "Drift",
  lldp: "LLDP",
  ipam: "IPAM",
  health: "Health",
  // T-806: sim_divergence findings from POST /simulate/verify's "Verify
  // live" action.
  probe: "Verify live",
  wireguard: "WireGuard",
  wan: "WAN",
  flow: "Flow",
  k8s: "Kubernetes",
  rogue: "Rogue",
  capacity: "Capacity",
  baseline: "Baseline",
  federation: "Federation",
  peer: "Peer",
  store: "Store",
  cert: "Certificates",
  gitsync: "Git sync",
};

const SEVERITY_LABELS: Record<Severity, string> = {
  error: "Error",
  warning: "Warning",
  info: "Info",
};

export function FindingsStreamPanel() {
  const narrow = useNarrowViewport();
  const { data: findings, isLoading, error } = useFindingsQuery();
  const fixMutation = useFixFindingMutation();
  const batchFixMutation = useBatchFixFindingsMutation();
  const ackMutation = useAckFindingMutation();
  const unackMutation = useUnackFindingMutation();
  const navigate = useNavigate();
  const openMgmtWizard = useMgmtWizardStore((s) => s.open);
  const setBlastRadiusRequest = useTopologyStore((s) => s.setBlastRadiusRequest);
  const setActiveId = useChangesetDrawerStore((s) => s.setActiveId);
  const queryClient = useQueryClient();
  const { toast } = useToast();
  const [fixingId, setFixingId] = useState<string | undefined>(undefined);
  // T-2005: a critical-finding push notification deep-links to
  // /tools?pushCategory=critical (internal/push's CriticalFindingNotification
  // — deliberately generic, carrying no specific finding id, see that
  // function's doc comment) rather than a specific finding, since a
  // finding's own id/detail can embed node/interface names and the push
  // payload must not. Landing here pre-filtered to error severity ("what
  // this app calls critical" — internal/push's Notifier doc comment) is
  // what makes the deep link land somewhere useful instead of an
  // unfiltered stream the reader has to search themselves.
  const [searchParams] = useSearchParams();
  const [filter, setFilter] = useState<FindingsFilterState>(() =>
    searchParams.get("pushCategory") === "critical" ? { ...EMPTY_FILTER, severity: "error" } : EMPTY_FILTER,
  );
  const [selectedIds, setSelectedIds] = useState<ReadonlySet<string>>(() => new Set());
  const [ackTarget, setAckTarget] = useState<{ id: string; detail: string } | undefined>(undefined);

  useFindingsWsBridge();

  const all = useMemo(() => findings ?? [], [findings]);
  const filtered = useMemo(() => filterFindings(all, filter), [all, filter]);
  const availableNodes = useMemo(() => nodesIn(all), [all]);

  function toggleSelected(id: string): void {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  /** T-2408. The batch is all-or-nothing server-side, so on failure the
   * selection is deliberately KEPT: the operator has to change something (drop
   * a conflicting finding, un-acknowledge one) and retry, and clearing their
   * selection would make them rebuild it first. */
  async function handleBatchFix(): Promise<void> {
    const ids = [...selectedIds];
    if (ids.length === 0) return;
    try {
      const changeset = await batchFixMutation.mutateAsync(ids);
      setActiveId(changeset.id);
      setSelectedIds(new Set());
      void queryClient.invalidateQueries({ queryKey: FINDINGS_QUERY_KEY });
      toast({
        title: `One changeset for ${String(ids.length)} findings`,
        description: "Review it in the drawer before applying.",
        variant: "success",
      });
    } catch (err) {
      toast({
        title: "Could not stage the batch",
        description: err instanceof Error ? err.message : "Nothing was staged.",
        variant: "error",
      });
    }
  }

  async function handleAck(reason: string, expiresAt?: number): Promise<void> {
    const target = ackTarget;
    if (!target) return;
    try {
      await ackMutation.mutateAsync({ id: target.id, reason, expiresAt });
      setAckTarget(undefined);
      toast({ title: "Finding acknowledged", description: "It stays in the stream, marked as deliberate.", variant: "success" });
    } catch {
      toast({ title: "Could not acknowledge the finding", variant: "error" });
    }
  }

  async function handleUnack(id: string): Promise<void> {
    try {
      await unackMutation.mutateAsync(id);
      toast({ title: "Acknowledgement cleared", variant: "success" });
    } catch {
      toast({ title: "Could not clear the acknowledgement", variant: "error" });
    }
  }

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

  // Phase 36: `runOperational` is deliberately omitted here. This panel has
  // no confirmation dialog for a mutating action, and remediation.ts
  // resolves an operational remedy to nothing without a runner — so the
  // findings stream keeps offering only navigation until a surface that can
  // confirm properly supplies one. Failing closed beats a button that
  // mutates a cluster with no ceremony.
  const remediationCtx: RemediationContext = {
    netWrite: false,
    navigate: (to) => { void navigate(to); },
    openMgmtWizard: (opts) => { openMgmtWizard(opts); },
  };

  /** T-909: at narrow width, a finding's secondary action (open the
   * mgmt-redundancy wizard / jump to the simulator) would land on a
   * desktop-only surface — rather than silently doing nothing or
   * navigating into a half-rendered page, it explains why instead. */
  function handleNarrowAction(): void {
    toast({ title: "Desktop only", description: "Open vnprox on a desktop or a wider window to use this action." });
  }

  /** T-3912: collapses the topology map to this finding's named entities
   * (plus the map path connecting them) — the same "Show blast radius"
   * navigation every other map-bound action here takes, so it is gated the
   * same way at narrow width. */
  function handleShowBlastRadius(refs: string[], label: string): void {
    setBlastRadiusRequest(blastRadiusRequestFromFindingRefs(refs, label));
    void navigate("/topology");
  }

  if (isLoading) {
    return <p className="text-sm text-fg-muted">Loading findings…</p>;
  }
  if (error) {
    return <p className="text-sm text-status-critical">Could not load findings.</p>;
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2" role="group" aria-label="Filter findings">
        <HelpAnchor topic="findings-stream" />
        <label className="flex items-center gap-1 text-xs text-fg-muted">
          Source
          <select
            aria-label="Filter by source"
            value={filter.source}
            onChange={(e) => {
              setFilter((f) => ({ ...f, source: e.target.value as FindingSource | "" }));
            }}
            className="rounded border border-border bg-transparent px-1.5 py-0.5 text-xs outline-none focus:border-accent-500"
          >
            <option value="">All</option>
            {(Object.keys(SOURCE_LABELS) as FindingSource[]).map((s) => (
              <option key={s} value={s}>
                {SOURCE_LABELS[s]}
              </option>
            ))}
          </select>
        </label>
        <label className="flex items-center gap-1 text-xs text-fg-muted">
          Severity
          <select
            aria-label="Filter by severity"
            value={filter.severity}
            onChange={(e) => {
              setFilter((f) => ({ ...f, severity: e.target.value as Severity | "" }));
            }}
            className="rounded border border-border bg-transparent px-1.5 py-0.5 text-xs outline-none focus:border-accent-500"
          >
            <option value="">All</option>
            {(Object.keys(SEVERITY_LABELS) as Severity[]).map((s) => (
              <option key={s} value={s}>
                {SEVERITY_LABELS[s]}
              </option>
            ))}
          </select>
        </label>
        <label className="flex items-center gap-1 text-xs text-fg-muted">
          Node
          <select
            aria-label="Filter by node"
            value={filter.node}
            onChange={(e) => {
              setFilter((f) => ({ ...f, node: e.target.value }));
            }}
            className="rounded border border-border bg-transparent px-1.5 py-0.5 text-xs outline-none focus:border-accent-500"
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
            className="text-xs text-accent-fg underline"
          >
            Clear filters
          </button>
        )}
      </div>

      {selectedIds.size > 0 && (
        <div
          className="flex items-center justify-between gap-3 rounded-md border border-accent-border bg-accent-soft px-3 py-2 text-sm"
          role="status"
        >
          <span>
            {selectedIds.size} finding{selectedIds.size === 1 ? "" : "s"} selected — these will be
            staged into <strong>one</strong> changeset.
          </span>
          <span className="flex shrink-0 items-center gap-2">
            <Button
              variant="secondary"
              size="sm"
              onClick={() => { setSelectedIds(new Set()); }}
            >
              Clear
            </Button>
            <Button
              size="sm"
              disabled={batchFixMutation.isPending}
              onClick={() => { void handleBatchFix(); }}
            >
              {batchFixMutation.isPending ? "Staging…" : "Create one fixing changeset"}
            </Button>
          </span>
        </div>
      )}

      <AckDialog
        finding={ackTarget}
        pending={ackMutation.isPending}
        onCancel={() => { setAckTarget(undefined); }}
        onConfirm={(reason, expiresAt) => { void handleAck(reason, expiresAt); }}
      />

      <FindingsList
        findings={filtered.map((f) => {
          // Phase 36: the producer declares its own remedy and this resolves
          // it. Previously this block tested `f.check` against two literals
          // (mgmt_single_path → the redundancy wizard, sim_divergence → the
          // simulator deep link), which put the vocabulary in the renderer:
          // every new remedy meant editing every surface that displays
          // findings, and a surface that was missed showed the finding with
          // no button and no error. See remediation.ts.
          const resolved = remediationAction(f.remedy, remediationCtx);
          return {
            id: f.id,
            severity: f.severity,
            detail: f.detail,
            nodes: f.nodes,
            refs: f.refs,
            fixable: f.fixable,
            ack: f.ack,
            category: `${SOURCE_LABELS[f.source]} · ${f.check}`,
            // T-909: at narrow width these land on desktop-only surfaces,
            // so the action explains itself rather than half-navigating.
            action: resolved
              ? { label: resolved.label, onClick: narrow ? handleNarrowAction : resolved.onClick }
              : undefined,
          };
        })}
        onFix={(id) => {
          void handleFix(id);
        }}
        onShowBlastRadius={
          narrow ? () => { handleNarrowAction(); } : (refs, label) => { handleShowBlastRadius(refs, label); }
        }
        onAck={narrow ? undefined : (id) => {
          const f = all.find((x) => x.id === id);
          if (f) setAckTarget({ id, detail: f.detail });
        }}
        onUnack={narrow ? undefined : (id) => {
          void handleUnack(id);
        }}
        selectedIds={narrow ? undefined : selectedIds}
        onToggleSelected={narrow ? undefined : toggleSelected}
        fixDisabledReason={narrow ? NARROW_FIX_DISABLED_REASON : undefined}
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
