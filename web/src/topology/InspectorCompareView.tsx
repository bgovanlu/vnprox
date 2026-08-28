// SPDX-License-Identifier: Apache-2.0

// T-908: side-by-side compare layout for InspectorStack's two-pane case.
// Two same-kind entities (e.g. two Bonds, or two nodes' vmbr0) render a
// single shared tab strip (Fields | Metrics) with each tab's content laid
// out in two aligned columns, one per entity — "fields and metrics tabs
// aligned column-wise" per the task card. A differing-kind pair (or either
// entity still loading) falls back to two plain, independent InspectorPanel
// panes instead of a broken/misaligned grid — "either disables compare or
// states why, not a broken layout."
import * as RadixTabs from "@radix-ui/react-tabs";
import clsx from "clsx";
import type { WsClient } from "../api/ws";
import { Button } from "../components/Button";
import { HelpAnchor } from "../help/HelpAnchor";
import { fieldRows } from "./fields";
import { InspectorPanel } from "./InspectorPanel";
import { METRICS_KINDS } from "./metricsKinds";
import { MetricsTab } from "./MetricsTab";
import { useInventoryDetailQuery } from "./queries";

export interface InspectorCompareViewProps {
  refA: string;
  refB: string;
  pinnedA: boolean;
  pinnedB: boolean;
  onCloseA: () => void;
  onCloseB: () => void;
  onTogglePinA: () => void;
  onTogglePinB: () => void;
  onSelectRelated: (ref: string) => void;
  /** Injectable WS client for both panes' Metrics tabs — tests only. */
  metricsWsClient?: WsClient;
}

const tabTriggerClass =
  "rounded-t px-3 py-1.5 text-xs font-medium text-slate-500 data-[state=active]:border-b-2 data-[state=active]:border-accent-600 data-[state=active]:text-accent-700 dark:text-slate-400 dark:data-[state=active]:text-accent-400";

/** Union of both entities' field keys, A's order first then any B-only
 * keys, each row carrying both sides' displayed value ("—" when that side
 * doesn't have the field at all) so the grid is exhaustive of both entities
 * without silently dropping a field only one of them reports. */
function unionFieldRows(
  fieldsA: Record<string, unknown>,
  fieldsB: Record<string, unknown>,
): [string, string, string][] {
  const rowsA = new Map(fieldRows(fieldsA));
  const rowsB = new Map(fieldRows(fieldsB));
  const keys: string[] = [];
  for (const k of rowsA.keys()) keys.push(k);
  for (const k of rowsB.keys()) if (!rowsA.has(k)) keys.push(k);
  return keys.map((k) => [k, rowsA.get(k) ?? "—", rowsB.get(k) ?? "—"]);
}

function ComparePaneHeader({
  label,
  node,
  kind,
  pinned,
  onTogglePin,
  onClose,
}: {
  label: string;
  node: string;
  kind: string;
  pinned: boolean;
  onTogglePin: () => void;
  onClose: () => void;
}) {
  return (
    <div className="flex items-center justify-between gap-1.5 rounded border border-slate-200 p-1.5 dark:border-slate-700">
      <div className="min-w-0">
        <div className="truncate text-sm font-medium text-slate-800 dark:text-slate-100">{label}</div>
        <div className="truncate text-[11px] text-slate-600 dark:text-slate-400">
          {kind} on {node || "cluster"}
        </div>
      </div>
      <div className="flex shrink-0 gap-1">
        <Button size="sm" variant={pinned ? "primary" : "secondary"} aria-pressed={pinned} onClick={onTogglePin}>
          {pinned ? "Pinned" : "Pin"}
        </Button>
        <Button size="sm" variant="ghost" aria-label={`Close ${label} (${node || "cluster"})`} onClick={onClose}>
          Close
        </Button>
      </div>
    </div>
  );
}

export function InspectorCompareView({
  refA,
  refB,
  pinnedA,
  pinnedB,
  onCloseA,
  onCloseB,
  onTogglePinA,
  onTogglePinB,
  onSelectRelated,
  metricsWsClient,
}: InspectorCompareViewProps) {
  const { data: dataA } = useInventoryDetailQuery(refA);
  const { data: dataB } = useInventoryDetailQuery(refB);

  const bothLoaded = dataA !== undefined && dataB !== undefined;
  const sameKind = bothLoaded && dataA.kind === dataB.kind;

  if (!sameKind) {
    // Falls back to two ordinary embedded panes — still "two open
    // inspectors simultaneously" (acceptance criterion 1's baseline), just
    // without column alignment, which only makes sense for matching shapes.
    return (
      <div className="flex h-full flex-col gap-2" data-testid="inspector-compare-mismatch">
        {bothLoaded && (
          <p role="status" className="text-xs text-amber-600 dark:text-amber-400">
            Compare is only available for two entities of the same kind — this pane holds a {dataA.kind} and a{" "}
            {dataB.kind}. Showing both independently instead.
          </p>
        )}
        <div className="flex flex-1 gap-3 overflow-x-auto">
          <InspectorPanel
            embedded
            selectedRef={refA}
            onClose={onCloseA}
            onSelectRelated={onSelectRelated}
            metricsWsClient={metricsWsClient}
            pinned={pinnedA}
            onTogglePin={onTogglePinA}
          />
          <InspectorPanel
            embedded
            selectedRef={refB}
            onClose={onCloseB}
            onSelectRelated={onSelectRelated}
            metricsWsClient={metricsWsClient}
            pinned={pinnedB}
            onTogglePin={onTogglePinB}
          />
        </div>
      </div>
    );
  }

  const hasMetrics = METRICS_KINDS.has(dataA.kind);
  const rows = unionFieldRows(dataA.fields, dataB.fields);

  return (
    <div
      className="flex h-full w-full min-w-[36rem] flex-col"
      data-testid="inspector-compare"
      aria-label={`Compare ${dataA.label} vs ${dataB.label}`}
    >
      <div className="mb-2 grid grid-cols-[minmax(6rem,auto)_1fr_1fr] items-start gap-2">
        <div className="pt-1">
          <HelpAnchor topic="inspector-compare" />
        </div>
        <ComparePaneHeader
          label={dataA.label}
          node={dataA.node}
          kind={dataA.kind}
          pinned={pinnedA}
          onTogglePin={onTogglePinA}
          onClose={onCloseA}
        />
        <ComparePaneHeader
          label={dataB.label}
          node={dataB.node}
          kind={dataB.kind}
          pinned={pinnedB}
          onTogglePin={onTogglePinB}
          onClose={onCloseB}
        />
      </div>

      <RadixTabs.Root defaultValue="fields" className="flex flex-1 flex-col">
        <RadixTabs.List className="flex gap-1 border-b border-slate-200 dark:border-slate-700">
          <RadixTabs.Trigger value="fields" className={tabTriggerClass}>
            Fields
          </RadixTabs.Trigger>
          {hasMetrics && (
            <RadixTabs.Trigger value="metrics" className={tabTriggerClass}>
              Metrics
            </RadixTabs.Trigger>
          )}
        </RadixTabs.List>

        <RadixTabs.Content value="fields" className="mt-3 flex-1 overflow-y-auto">
          <div className="grid grid-cols-[minmax(6rem,auto)_1fr_1fr] gap-x-3 gap-y-1.5 text-xs">
            {rows.map(([k, a, b]) => (
              <div className="contents" key={k}>
                <dt className="text-slate-600 dark:text-slate-400">{k}</dt>
                <dd
                  className={clsx(
                    "break-all text-slate-700 dark:text-slate-200",
                    a !== b && "font-semibold text-amber-600 dark:text-amber-400",
                  )}
                >
                  {a}
                </dd>
                <dd
                  className={clsx(
                    "break-all text-slate-700 dark:text-slate-200",
                    a !== b && "font-semibold text-amber-600 dark:text-amber-400",
                  )}
                >
                  {b}
                </dd>
              </div>
            ))}
            {rows.length === 0 && <p className="col-span-3 text-slate-600 dark:text-slate-400">No fields to compare.</p>}
          </div>
        </RadixTabs.Content>

        {hasMetrics && (
          <RadixTabs.Content value="metrics" className="mt-3 flex-1 overflow-y-auto">
            <div className="grid grid-cols-2 gap-4">
              <div>
                <h3 className="mb-1 text-xs font-medium text-slate-500 dark:text-slate-400">
                  {dataA.label} ({dataA.node || "cluster"})
                </h3>
                <MetricsTab entityRef={refA} kind={dataA.kind} wsClient={metricsWsClient} />
              </div>
              <div>
                <h3 className="mb-1 text-xs font-medium text-slate-500 dark:text-slate-400">
                  {dataB.label} ({dataB.node || "cluster"})
                </h3>
                <MetricsTab entityRef={refB} kind={dataB.kind} wsClient={metricsWsClient} />
              </div>
            </div>
          </RadixTabs.Content>
        )}
      </RadixTabs.Root>
    </div>
  );
}
