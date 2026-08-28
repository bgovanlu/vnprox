// SPDX-License-Identifier: Apache-2.0

import * as RadixDropdown from "@radix-ui/react-dropdown-menu";
import * as RadixTabs from "@radix-ui/react-tabs";
import { EntityHistoryTab } from "./EntityHistoryTab";
import clsx from "clsx";
import { useNavigate } from "react-router-dom";
import { Drawer, DrawerContent, DrawerDescription, DrawerTitle } from "../components/Drawer";
import { EmptyState } from "../components/EmptyState";
import { HelpAnchor } from "../help/HelpAnchor";
import { Button } from "../components/Button";
import { Tooltip } from "../components/Tooltip";
import { useSession } from "../api/useSession";
import type { FDBRow } from "../api/types";
import type { WsClient } from "../api/ws";
import { useToast } from "../components/Toast";
import { capsForNode, missingCapTooltip } from "../changesets/capabilities";
import { useEditorLauncherStore, editorKindForInventoryKind } from "../changesets/editorLauncherStore";
import { buildBondDeleteOp, buildVlanDeleteOp } from "../changesets/opBuilders";
import { useDrawerActions } from "../changesets/useDrawerActions";
import { isTraceableEntityKind, traceFromPath, traceToExternalPath, traceToPath } from "../simulator/traceLink";
import { isCapturableEntityKind, useCaptureLauncherStore } from "../capture/captureLauncherStore";
import { diagnosePath, isDiagnosableEntityKind } from "../diagnose/diagnosePath";
import { resumeOnboarding } from "../onboarding/onboardingMachine";
import { useOnboardingProgressQuery, useSaveOnboardingProgressMutation } from "../onboarding/queries";
import { AnnotationsSection } from "./AnnotationsSection";
import { useAnnotationsForRef } from "./annotationsQueries";
import { BondLacpSection } from "./BondLacpSection";
import { fieldRows } from "./fields";
import { InteriorTab } from "./InteriorTab";
import { LacpHashSection } from "./LacpHashSection";
import { METRICS_KINDS } from "./metricsKinds";
import { MetricsTab } from "./MetricsTab";
import { MigrationPreflightTab } from "./MigrationPreflightTab";
import { useInventoryDetailQuery, useMgmtStatusQuery } from "./queries";
import { useTopologyStore } from "./store";
import { useMgmtWizardStore } from "../mgmt/mgmtWizardStore";
import { describeMgmtPathRedundancy } from "../mgmt/mgmtPath";
import { mgmtStrings } from "../mgmt/strings";

/** Runtime narrowing for `data.fields.FDB` (typed `unknown` — EntityDetail's
 * fields map is generic JSON, see fields.ts): internal/topology's Detail()
 * only ever populates this key with T-306's FDBRow shape for bridge kinds
 * (detail.go), but the frontend can't take that on faith across the wire. */
function isFDBRows(v: unknown): v is FDBRow[] {
  return (
    Array.isArray(v) &&
    v.every((row) => typeof row === "object" && row !== null && "mac" in row && "owner" in row)
  );
}

/** T-702: renders docs/features/topology.md §3's "Management path" tab
 * content — carrier, path chain, redundancy statement in plain English, and
 * (when the status is only vnprox's live detection, never onboarding-
 * confirmed) a caveat with a working link back to the onboarding
 * "protected interfaces" step. `ref`/`carrierRef` are node.badges-shaped
 * inventory ref strings; `label` resolves one to a friendlier display via
 * the already-loaded topology query cache — falling back to the raw ref
 * string when the entity isn't in cache (e.g. it scrolled out of view). */
function ManagementPathSection({ node }: { node: string }) {
  const { data: mgmtStatus } = useMgmtStatusQuery();
  const { data: onboardingProgress } = useOnboardingProgressQuery();
  const saveOnboarding = useSaveOnboardingProgressMutation();
  const openMgmtWizard = useMgmtWizardStore((s) => s.open);
  const { data: session } = useSession();
  const nodePaths = mgmtStatus?.nodes[node] ?? [];

  if (!mgmtStatus || nodePaths.length === 0) {
    return <p className="text-xs text-slate-600 dark:text-slate-400">No management/corosync path resolved for this node yet.</p>;
  }

  // Whether any mgmt-role carrier for this node is non-redundant — the
  // wizard's reason for being. Offered regardless (a redundant node can
  // still add headroom), but only when the user can actually write.
  const canWrite = capsForNode(session, node).netWrite;

  return (
    <div className="space-y-3 text-xs">
      {mgmtStatus.staleProtected && (
        <div className="rounded border border-amber-300 bg-amber-50 p-2 text-amber-800 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-200">
          The protected-interface list looks out of date — a management interface moved since it was last confirmed.
          Re-confirm it in the onboarding &quot;protected interfaces&quot; step.
        </div>
      )}
      {canWrite && (
        <Button
          size="sm"
          variant="secondary"
          onClick={() => {
            openMgmtWizard({ node });
          }}
        >
          {mgmtStrings.launch.button}
        </Button>
      )}
      {mgmtStatus.source === "detected" && (
        <div className="rounded border border-amber-300 bg-amber-50 p-2 text-amber-800 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-200">
          <p>
            This is vnprox&apos;s best-effort detection (Node.IP / corosync.conf) — no one has confirmed it during
            onboarding yet.
          </p>
          {onboardingProgress && (
            <button
              type="button"
              className="mt-1 font-medium underline hover:no-underline"
              onClick={() => {
                saveOnboarding.mutate({ ...resumeOnboarding(onboardingProgress), currentStep: "protected" });
              }}
            >
              Review protected interfaces
            </button>
          )}
        </div>
      )}
      {nodePaths.map((p) => (
        <div key={p.ref} className="rounded border border-slate-200 p-2 dark:border-slate-700">
          <div className="font-medium text-slate-700 dark:text-slate-200">
            {p.ref} <span className="font-normal text-slate-600 dark:text-slate-400">({p.roles.join(", ")})</span>
          </div>
          <div className="mt-1 text-slate-500 dark:text-slate-400">
            Path: {p.path.length > 0 ? p.path.join(" → ") : "(no physical interface resolved)"}
          </div>
          <div className="mt-1 text-slate-600 dark:text-slate-300">
            {describeMgmtPathRedundancy(p.path, p.redundant)}
          </div>
        </div>
      ))}
    </div>
  );
}

/** Only bridges/bonds/VLANs are deletable entities with their own delete
 * op in this task's scope (physnic delete isn't a documented op — a NIC is
 * removed by unplugging it, not a changeset op). */
const DELETABLE_KINDS = new Set(["bridge", "ovs-bridge", "bond", "ovs-bond", "vlan"]);

export interface InspectorPanelProps {
  /** A real inventory ref (never a guest-group id — see TopologyPage's
   * click handler, which routes guest-group clicks to expand/collapse
   * instead of ever setting this). undefined closes the panel. */
  selectedRef: string | undefined;
  onClose: () => void;
  onSelectRelated: (ref: string) => void;
  /** Injectable WS client for the Metrics tab (see MetricsTab's doc
   * comment) — tests only; production never passes this. */
  metricsWsClient?: WsClient;
  /** T-908: renders as a plain non-modal region (a card in
   * InspectorStack.tsx's side-by-side pane row) instead of the default
   * Radix Dialog drawer. Multiple embedded panes can be mounted
   * simultaneously without the focus-trap/overlay conflicts a second
   * concurrent modal `<Drawer>` would cause — the stack's whole reason for
   * threading this prop through rather than reusing `<Drawer>` unmodified.
   * Bare/default usage (every existing call site and test) omits this and
   * gets the original single-modal-drawer behavior unchanged. */
  embedded?: boolean;
  /** T-908 pin control, rendered in the header next to Trace/Edit/Delete
   * when the caller (InspectorStack) supplies it. A bare InspectorPanel —
   * every pre-T-908 call site — omits both and renders no pin button at
   * all, so existing markup/snapshots are unaffected. */
  pinned?: boolean;
  onTogglePin?: () => void;
}

const tabTriggerClass =
  "rounded-t px-3 py-1.5 text-xs font-medium text-slate-500 data-[state=active]:border-b-2 data-[state=active]:border-accent-600 data-[state=active]:text-accent-700 dark:text-slate-400 dark:data-[state=active]:text-accent-400";

/**
 * The click-to-inspect panel (docs/features/topology.md §2: "inspector
 * panel: normalized fields, live status, raw source, related entities").
 * "Raw source" is GET /inventory/{ref}'s `rawSource` map — the verbatim
 * interfaces(5) stanza / pretty-printed PVE API object / observed-state
 * JSON per contributing source (docs/api.md). Per-field provenance (which
 * source won each resolved field) stays visible on its own tab alongside
 * it: rawSource shows what each source said, provenance shows who won.
 */
export function InspectorPanel({
  selectedRef,
  onClose,
  onSelectRelated,
  metricsWsClient,
  embedded = false,
  pinned,
  onTogglePin,
}: InspectorPanelProps) {
  const { data, isLoading, isError } = useInventoryDetailQuery(selectedRef);
  const rawSourceEntries = Object.entries(data?.rawSource ?? {});
  const { data: session } = useSession();
  const openEditor = useEditorLauncherStore((s) => s.open);
  const openCapture = useCaptureLauncherStore((s) => s.open);
  const { addOps } = useDrawerActions();
  const { toast } = useToast();

  const editorKind = editorKindForInventoryKind(data?.kind);
  const deletable = data ? DELETABLE_KINDS.has(data.kind) : false;
  const editDisabledReason = data ? missingCapTooltip(session, data.node, "netWrite") : undefined;
  // T-1302: the "Capture" button — offered on the same bridge/bond/guest-NIC/
  // SDN-VNet kinds the map's right-click entry point uses (captureLauncherStore's
  // own gate), disabled with the missing-privilege tooltip's usual convention
  // when the session lacks the dedicated `capture` capability.
  const captureDisabledReason = data ? missingCapTooltip(session, data.node, "capture") : undefined;
  const canWrite = data ? capsForNode(session, data.node).netWrite : false;
  const isBridgeKind = data ? data.kind === "bridge" || data.kind === "ovs-bridge" : false;
  // T-804: the live LACP section applies to bonds of either flavor — a
  // plain Linux bond or an OVS bond declared 802.3ad — since both flow
  // through the same host-netlink Bond/SlaveDetail substrate.
  const isBondKind = data ? data.kind === "bond" || data.kind === "ovs-bond" : false;
  const fdbRows = isBridgeKind && data ? (isFDBRows(data.fields.FDB) ? data.fields.FDB : []) : [];
  const hasMetrics = data ? METRICS_KINDS.has(data.kind) : false;
  // T-1304: the guest network interior inspector applies to guest
  // entities only (qemu/lxc) — a bare bridge/bond/etc. has no "inside" to
  // read.
  const isGuestKind = data ? data.kind === "guest" : false;
  // T-702: node-scoped entities only (bridge/bond/vlan/physnic/guest/...) —
  // cluster-scoped SDN entities (data.node === "") never carry a management
  // IP or corosync link of their own.
  const showMgmtPath = data ? data.node !== "" : false;
  const navigate = useNavigate();
  const select = useTopologyStore((s) => s.select);
  const notes = useAnnotationsForRef(data?.ref);

  function handleDelete(): void {
    if (!data) return;
    if (isBridgeKind) {
      openEditor({ kind: "bridge-delete", node: data.node, target: data.ref });
      return;
    }
    // Bonds/VLANs have no reattach interlock — a plain confirm is enough
    // before drafting the delete op directly.
    if (!window.confirm(`Delete ${data.kind} ${data.label}? This only stages the change — nothing applies yet.`)) {
      return;
    }
    const op = data.kind === "vlan" ? buildVlanDeleteOp(data.ref) : buildBondDeleteOp(data.ref);
    void addOps([op], `Delete ${data.label}`)
      .then(() => {
        toast({ title: "Added to changeset" });
        onClose();
      })
      .catch(() => {
        toast({ title: "Could not add to changeset", variant: "error" });
      });
  }

  const body = (
    <>
      <div className="flex items-start justify-between gap-2">
        {embedded ? (
          // DrawerTitle/DrawerDescription are RadixDialog.Title/Description
          // — they require a Dialog.Root context, which embedded panes
          // (InspectorStack's side-by-side pane row) deliberately don't
          // mount (see this component's embedded doc comment). Plain
          // elements carry the same visible text; the surrounding region's
          // own aria-label (set by the embedded branch below) supplies the
          // accessible name instead of Radix's aria-labelledby wiring.
          <div>
            <h2 className="flex items-center gap-1.5 text-base font-semibold text-slate-900 dark:text-slate-100">
              {data?.label ?? "Inspector"}
              <HelpAnchor topic="topology-inspector" />
            </h2>
            <p id="inspector-description" className="text-sm text-slate-500 dark:text-slate-400">
              {data ? `${data.kind} on ${data.node || "cluster"}` : "Entity detail"}
            </p>
          </div>
        ) : (
          <div>
            <DrawerTitle>{data?.label ?? "Inspector"}</DrawerTitle>
            <DrawerDescription id="inspector-description">
              {data ? `${data.kind} on ${data.node || "cluster"}` : "Entity detail"}
            </DrawerDescription>
          </div>
        )}
        {data && (
          <div className="flex shrink-0 gap-1.5">
            {onTogglePin && (
              <Button size="sm" variant={pinned ? "primary" : "secondary"} aria-pressed={pinned} onClick={onTogglePin}>
                {pinned ? "Pinned" : "Pin"}
              </Button>
            )}
            {embedded && (
              <Button
                size="sm"
                variant="ghost"
                aria-label={`Close ${data.label} (${data.node || "cluster"})`}
                onClick={onClose}
              >
                Close
              </Button>
            )}
            {(editorKind !== undefined || isTraceableEntityKind(data.kind) || isCapturableEntityKind(data.kind) || isDiagnosableEntityKind(data.kind)) && (
              <>
              {isCapturableEntityKind(data.kind) && (
                <Tooltip content={captureDisabledReason}>
                  <span>
                    <Button
                      size="sm"
                      variant="secondary"
                      disabled={captureDisabledReason !== undefined}
                      onClick={() => {
                        openCapture({ targetRef: data.ref, node: data.node, label: data.label });
                      }}
                    >
                      Capture
                    </Button>
                  </span>
                </Tooltip>
              )}
              {isDiagnosableEntityKind(data.kind) && (
                <Button
                  size="sm"
                  variant="secondary"
                  onClick={() => {
                    const path = diagnosePath(data.kind, data.ref);
                    if (path) void navigate(path);
                  }}
                >
                  Diagnose
                </Button>
              )}
              {isTraceableEntityKind(data.kind) && (
                <RadixDropdown.Root>
                  <RadixDropdown.Trigger asChild>
                    <Button size="sm" variant="secondary">
                      Trace path ▾
                    </Button>
                  </RadixDropdown.Trigger>
                  <RadixDropdown.Portal>
                    <RadixDropdown.Content
                      align="end"
                      sideOffset={6}
                      // T-4203: a floating menu above the inspector — `surface-overlay`.
                      className="z-50 min-w-[12rem] rounded-md border border-slate-200 bg-surface-overlay p-1 shadow-lg dark:border-slate-700"
                    >
                      <RadixDropdown.Item
                        className="cursor-pointer rounded px-2 py-1.5 text-sm outline-none hover:bg-slate-100 dark:hover:bg-slate-800"
                        onSelect={() => {
                          const path = traceFromPath(data.kind, data.ref);
                          if (path) void navigate(path);
                        }}
                      >
                        Trace path from here
                      </RadixDropdown.Item>
                      <RadixDropdown.Item
                        className="cursor-pointer rounded px-2 py-1.5 text-sm outline-none hover:bg-slate-100 dark:hover:bg-slate-800"
                        onSelect={() => {
                          const path = traceToPath(data.kind, data.ref);
                          if (path) void navigate(path);
                        }}
                      >
                        Trace path to here
                      </RadixDropdown.Item>
                      <RadixDropdown.Item
                        className="cursor-pointer rounded px-2 py-1.5 text-sm outline-none hover:bg-slate-100 dark:hover:bg-slate-800"
                        onSelect={() => {
                          const path = traceToExternalPath(data.kind, data.ref);
                          if (path) void navigate(path);
                        }}
                      >
                        Trace path to external
                      </RadixDropdown.Item>
                    </RadixDropdown.Content>
                  </RadixDropdown.Portal>
                </RadixDropdown.Root>
              )}
              {editorKind && (
                <Tooltip content={editDisabledReason}>
                  <span>
                    <Button
                      size="sm"
                      variant="secondary"
                      disabled={!canWrite}
                      onClick={() => {
                        openEditor({ kind: editorKind, node: data.node, target: data.ref });
                      }}
                    >
                      Edit
                    </Button>
                  </span>
                </Tooltip>
              )}
              {deletable && (
                <Tooltip content={editDisabledReason}>
                  <span>
                    <Button
                      size="sm"
                      variant="secondary"
                      disabled={!canWrite}
                      onClick={() => {
                        openEditor({ kind: "iface-rename", node: data.node, target: data.ref });
                      }}
                    >
                      Rename
                    </Button>
                  </span>
                </Tooltip>
              )}
              {editorKind && deletable && (
                <Tooltip content={editDisabledReason}>
                  <span>
                    <Button size="sm" variant="destructive" disabled={!canWrite} onClick={handleDelete}>
                      Delete
                    </Button>
                  </span>
                </Tooltip>
              )}
              </>
            )}
          </div>
        )}
      </div>

      {isLoading && <p className="mt-4 text-sm text-slate-600 dark:text-slate-400">Loading…</p>}
      {isError && (
        <EmptyState className="mt-4" title="Could not load this entity" description="It may have just been removed." />
      )}

        {data && (
          <RadixTabs.Root defaultValue="fields" className="mt-4 flex flex-1 flex-col">
            <RadixTabs.List className="flex gap-1 border-b border-slate-200 dark:border-slate-700">
              <RadixTabs.Trigger value="fields" className={tabTriggerClass}>
                Fields
              </RadixTabs.Trigger>
              <RadixTabs.Trigger value="raw" className={tabTriggerClass}>
                Raw source
              </RadixTabs.Trigger>
              <RadixTabs.Trigger value="provenance" className={tabTriggerClass}>
                Provenance
              </RadixTabs.Trigger>
              <RadixTabs.Trigger value="related" className={tabTriggerClass}>
                Related ({data.related.length})
              </RadixTabs.Trigger>
              <RadixTabs.Trigger value="history" className={tabTriggerClass}>
                History
              </RadixTabs.Trigger>
              <RadixTabs.Trigger value="notes" className={tabTriggerClass}>
                Notes ({notes.length})
              </RadixTabs.Trigger>
              {showMgmtPath && (
                <RadixTabs.Trigger value="mgmt-path" className={tabTriggerClass}>
                  Management path
                </RadixTabs.Trigger>
              )}
              {isBridgeKind && (
                <RadixTabs.Trigger value="fdb" className={tabTriggerClass}>
                  FDB ({fdbRows.length})
                </RadixTabs.Trigger>
              )}
              {isBondKind && (
                <RadixTabs.Trigger value="lacp" className={tabTriggerClass}>
                  LACP
                </RadixTabs.Trigger>
              )}
              {hasMetrics && (
                <RadixTabs.Trigger value="metrics" className={tabTriggerClass}>
                  Metrics
                </RadixTabs.Trigger>
              )}
              {isGuestKind && (
                <RadixTabs.Trigger value="interior" className={tabTriggerClass}>
                  Interior
                </RadixTabs.Trigger>
              )}
              {isGuestKind && (
                <RadixTabs.Trigger value="migration" className={tabTriggerClass}>
                  Migration
                </RadixTabs.Trigger>
              )}
            </RadixTabs.List>

            <RadixTabs.Content value="fields" className="mt-3 flex-1 overflow-y-auto">
              <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5 text-xs">
                {fieldRows(data.fields).map(([k, v]) => (
                  <div className="contents" key={k}>
                    <dt className="text-slate-600 dark:text-slate-400">{k}</dt>
                    <dd className="break-all text-slate-700 dark:text-slate-200">{v}</dd>
                  </div>
                ))}
              </dl>
            </RadixTabs.Content>

            {/* T-2403: the audit trail, changesets and snapshots re-sliced by
                this entity. The fetch is deliberately NOT made until the tab
                is opened — the inspector is opened constantly, and a history
                query on every click would be a scan nobody asked for. */}
            <RadixTabs.Content value="history" className="mt-3 flex-1 overflow-y-auto">
              <EntityHistoryTab entityRef={selectedRef ?? ""} />
            </RadixTabs.Content>

            <RadixTabs.Content value="raw" className="mt-3 flex-1 overflow-y-auto">
              {rawSourceEntries.length === 0 ? (
                <p className="text-xs text-slate-600 dark:text-slate-400">
                  No raw source retained for this entity — none of its contributing sources kept original
                  config text (common for purely observed entities).
                </p>
              ) : (
                <div className="space-y-3">
                  {rawSourceEntries.map(([source, text]) => (
                    <section key={source}>
                      <h3 className="mb-1 text-xs font-medium text-slate-500 dark:text-slate-400">{source}</h3>
                      {/* T-4203: an inset code block — `surface-sunken`. */}
                      <pre className="overflow-x-auto rounded border border-slate-200 bg-surface-sunken p-2 font-mono text-[11px] leading-snug text-slate-700 dark:border-slate-700 dark:text-slate-200">
                        {text}
                      </pre>
                    </section>
                  ))}
                </div>
              )}
            </RadixTabs.Content>

            <RadixTabs.Content value="provenance" className="mt-3 flex-1 overflow-y-auto">
              <p className="mb-2 text-xs text-slate-600 dark:text-slate-400">
                Resolved-field provenance: which poll source won each field, and any disagreeing values.
              </p>
              <ul className="space-y-2 text-xs">
                {Object.entries(data.provenance).map(([field, prov]) => (
                  <li key={field} className="rounded border border-slate-200 p-2 dark:border-slate-700">
                    <div className="font-medium text-slate-700 dark:text-slate-200">
                      {field} <span className="font-normal text-slate-600 dark:text-slate-400">← {prov.owner}</span>
                    </div>
                    {prov.conflicts && prov.conflicts.length > 0 && (
                      <ul className="mt-1 space-y-0.5 pl-3 text-amber-600 dark:text-amber-400">
                        {prov.conflicts.map((c, i) => (
                          <li key={i}>
                            {c.source}: {c.value}
                          </li>
                        ))}
                      </ul>
                    )}
                  </li>
                ))}
                {Object.keys(data.provenance).length === 0 && (
                  <li className="text-slate-600 dark:text-slate-400">No per-field provenance recorded.</li>
                )}
              </ul>
            </RadixTabs.Content>

            <RadixTabs.Content value="related" className="mt-3 flex-1 overflow-y-auto">
              <ul className="space-y-1 text-xs">
                {data.related.map((r) => (
                  <li key={`${r.ref}-${r.edgeKind}-${r.direction}`}>
                    <button
                      type="button"
                      onClick={() => {
                        onSelectRelated(r.ref);
                      }}
                      className={clsx(
                        "flex w-full items-center justify-between rounded px-2 py-1 text-left",
                        "hover:bg-slate-100 dark:hover:bg-slate-800",
                      )}
                    >
                      <span className="truncate text-slate-700 dark:text-slate-200">{r.ref}</span>
                      <span className="shrink-0 text-slate-600 dark:text-slate-400">
                        {r.edgeKind} ({r.direction})
                      </span>
                    </button>
                  </li>
                ))}
                {data.related.length === 0 && <li className="text-slate-600 dark:text-slate-400">No related entities.</li>}
              </ul>
            </RadixTabs.Content>

            <RadixTabs.Content value="notes" className="mt-3 flex-1 overflow-y-auto">
              <AnnotationsSection entityRef={data.ref} />
            </RadixTabs.Content>

            {showMgmtPath && (
              <RadixTabs.Content value="mgmt-path" className="mt-3 flex-1 overflow-y-auto">
                <ManagementPathSection node={data.node} />
              </RadixTabs.Content>
            )}

            {isBridgeKind && (
              <RadixTabs.Content value="fdb" className="mt-3 flex-1 overflow-y-auto">
                <p className="mb-2 text-xs text-slate-600 dark:text-slate-400">
                  This bridge's forwarding-database table (docs/features/lldp-discovery.md §4), owner-labeled
                  against the cluster-wide inventory.
                </p>
                <ul className="space-y-1 text-xs">
                  {fdbRows.map((r) => (
                    <li
                      key={`${r.mac}-${r.port ?? ""}`}
                      className="flex items-center justify-between gap-2 rounded px-2 py-1 hover:bg-slate-100 dark:hover:bg-slate-800"
                    >
                      <span className="flex items-center gap-2">
                        <span className="font-mono text-slate-700 dark:text-slate-200">{r.mac}</span>
                        {r.port && <span className="text-slate-600 dark:text-slate-400">on {r.port}</span>}
                        {r.stale && <span className="text-amber-600 dark:text-amber-400">stale</span>}
                      </span>
                      {r.ownerRef ? (
                        <button
                          type="button"
                          onClick={() => {
                            select(r.ownerRef);
                            void navigate("/topology");
                          }}
                          className="shrink-0 text-slate-500 hover:underline dark:text-slate-400"
                        >
                          {r.owner}
                          {r.ownerLabel ? ` (${r.ownerLabel})` : ""}
                        </button>
                      ) : (
                        <span className="shrink-0 text-slate-600 dark:text-slate-400">{r.owner}</span>
                      )}
                    </li>
                  ))}
                  {fdbRows.length === 0 && <li className="text-slate-600 dark:text-slate-400">No FDB entries learned on this bridge.</li>}
                </ul>
              </RadixTabs.Content>
            )}

            {isBondKind && (
              <RadixTabs.Content value="lacp" className="mt-3 flex-1 overflow-y-auto">
                <BondLacpSection fields={data.fields} />
                <LacpHashSection node={data.node} fields={data.fields} wsClient={metricsWsClient} />
              </RadixTabs.Content>
            )}

            {hasMetrics && (
              <RadixTabs.Content value="metrics" className="mt-3 flex-1 overflow-y-auto">
                <MetricsTab entityRef={data.ref} kind={data.kind} wsClient={metricsWsClient} />
              </RadixTabs.Content>
            )}
            {isGuestKind && (
              <RadixTabs.Content value="interior" className="mt-3 flex-1 overflow-y-auto">
                <InteriorTab entityRef={data.ref} />
              </RadixTabs.Content>
            )}
            {isGuestKind && (
              <RadixTabs.Content value="migration" className="mt-3 flex-1 overflow-y-auto">
                <MigrationPreflightTab entityRef={data.ref} />
              </RadixTabs.Content>
            )}
          </RadixTabs.Root>
      )}
    </>
  );

  if (embedded) {
    return (
      <div
        role="region"
        aria-label={data ? `${data.label} inspector` : "Inspector"}
        // T-4203: an embedded pane floats over the canvas the same way the
        // single-pane Drawer variant below does — `surface-overlay`.
        className="flex h-full w-96 shrink-0 flex-col overflow-y-auto rounded-md border border-slate-200 bg-surface-overlay p-4 shadow dark:border-slate-700"
      >
        {body}
      </div>
    );
  }

  return (
    <Drawer open={selectedRef !== undefined} onOpenChange={(open) => { if (!open) onClose(); }}>
      <DrawerContent side="right" aria-describedby="inspector-description">
        {body}
      </DrawerContent>
    </Drawer>
  );
}
