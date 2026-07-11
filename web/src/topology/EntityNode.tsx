// A single custom React Flow node component covering every rendered entity
// kind (bridge, bond, physnic, vlan, sdn-zone/vnet, guest, guest-nic, and
// the synthetic guest-group pill), branching its look by `data.kind` rather
// than one component file per kind — deliberately, given the number of
// kinds (a dozen-plus inventory Kinds project onto only a handful of
// visually distinct shapes: a plain box, the SDN "cloud" look, and the
// guest-group pill) and that every one of them shares the same status-
// painting/dim/highlight/selection behavior (docs/features/topology.md §2).
import { Handle, Position, type NodeProps, type Node } from "@xyflow/react";
import clsx from "clsx";
import type { EntityStatus } from "../api/types";

export interface EntityNodeData extends Record<string, unknown> {
  label: string;
  kind: string;
  status: EntityStatus;
  badges: string[];
  dimmed: boolean;
  /** This node's band is rendering last-known data because a node-scoped
   * collector source is stale (docs/features/topology.md §5: "its band
   * renders greyed") — greyscale, distinct from `dimmed` (VLAN filter). */
  stale?: boolean;
  highlighted: boolean;
  isGuestGroup: boolean;
  collapsedCount?: number;
}

export type EntityFlowNode = Node<EntityNodeData, "entity">;

// Status painting (docs/features/topology.md §2: "link down = red edge;
// degraded bond (missing slave) = amber; ... drift = dashed outline").
// "unknown" gets a neutral dashed treatment — it's a legitimate, common
// state for peer nodes' host-only fields (see api/types.ts's EntityStatus
// doc comment), not something to visually alarm on.
const STATUS_CLASSES: Record<EntityStatus, string> = {
  ok: "border-slate-300 dark:border-slate-600",
  down: "border-red-500 dark:border-red-500 ring-1 ring-red-500/40",
  degraded: "border-amber-500 dark:border-amber-500 ring-1 ring-amber-500/40",
  unknown: "border-slate-400 border-dashed dark:border-slate-500",
};

const KIND_ACCENT: Record<string, string> = {
  physnic: "bg-slate-50 dark:bg-slate-800",
  bond: "bg-sky-50 dark:bg-sky-950",
  "ovs-bond": "bg-sky-50 dark:bg-sky-950",
  bridge: "bg-indigo-50 dark:bg-indigo-950",
  "ovs-bridge": "bg-indigo-50 dark:bg-indigo-950",
  vlan: "bg-violet-50 dark:bg-violet-950",
  "sdn-zone": "bg-teal-50 dark:bg-teal-950",
  "sdn-vnet": "bg-teal-50 dark:bg-teal-950",
  "sdn-subnet": "bg-teal-50 dark:bg-teal-950",
  guest: "bg-emerald-50 dark:bg-emerald-950",
  "guest-nic": "bg-emerald-50 dark:bg-emerald-950",
  "guest-group": "bg-emerald-100 dark:bg-emerald-900",
  "lldp-neighbor": "bg-slate-100 dark:bg-slate-800",
};

export function EntityNode({ data, selected }: NodeProps<EntityFlowNode>) {
  const isPill = data.isGuestGroup;
  return (
    <div
      role="button"
      aria-label={data.label}
      className={clsx(
        "flex flex-col gap-1 border px-3 py-2 text-xs shadow-sm transition-opacity",
        isPill ? "rounded-full text-center" : "rounded-md",
        KIND_ACCENT[data.kind] ?? "bg-white dark:bg-slate-900",
        STATUS_CLASSES[data.status],
        // One opacity class per node (never two competing ones, whose CSS
        // order would decide): the VLAN filter's dim wins over staleness.
        data.dimmed && !data.highlighted ? "opacity-25" : data.stale ? "opacity-60" : "opacity-100",
        data.stale && "grayscale",
        data.highlighted && "ring-2 ring-blue-500",
        selected && "outline outline-2 outline-offset-1 outline-blue-600",
        // drift = dashed outline (docs/features/topology.md §2), additive
        // to (not replacing) the status-driven border color above — a
        // "down"/"degraded" node can also carry an open drift finding.
        data.badges.includes("drift") && "border-dashed",
      )}
      style={{ minWidth: 140 }}
    >
      <Handle type="target" position={Position.Top} className="opacity-0" />
      <Handle type="source" position={Position.Bottom} className="opacity-0" />
      <div className="flex items-center justify-between gap-2">
        <span className="truncate font-medium text-slate-800 dark:text-slate-100">{data.label}</span>
        {!isPill && (
          <span className="shrink-0 text-[10px] uppercase tracking-wide text-slate-400 dark:text-slate-500">
            {data.kind}
          </span>
        )}
      </div>
      {data.badges.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {data.badges.map((b) => (
            <span
              key={b}
              className="rounded bg-slate-200/70 px-1 py-0.5 text-[10px] text-slate-600 dark:bg-slate-700/70 dark:text-slate-300"
            >
              {b}
            </span>
          ))}
        </div>
      )}
      {isPill && (
        <span className="text-[10px] text-slate-500 dark:text-slate-400">click to expand</span>
      )}
    </div>
  );
}
