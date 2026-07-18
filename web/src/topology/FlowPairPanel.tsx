// T-1003's guest-pair drill-down: opened when a Flows-layer overlay edge
// (flowEdges.ts's FlowEdge) is clicked on the map — a small, non-modal
// floating summary (mirrors InspectorStack.tsx's bottom-anchored floating
// panel convention, opposite corner so it never collides with an entity
// inspector open at the same time) plus a "view in Flow Explorer" deep
// link carrying the same pair as URL state (flows/urlState.ts).
import { Link } from "react-router-dom";
import type { FlowEdge } from "./flowEdges";
import { flowPairExplorerPath } from "../flows/urlState";

export interface FlowPairPanelProps {
  edge: FlowEdge;
  onClose: () => void;
}

function formatBytes(n: number): string {
  if (n < 1024) return `${String(n)} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let value = n / 1024;
  let unitIndex = 0;
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex += 1;
  }
  return `${value.toFixed(1)} ${units[unitIndex] ?? "TB"}`;
}

export function FlowPairPanel({ edge, onClose }: FlowPairPanelProps) {
  return (
    <div
      role="region"
      aria-label="Flow conversation"
      // top-right (below the page toolbar): NavRail owns the full left
      // edge (z-50, docs/features/topology.md's map toolbar sits above the
      // canvas) and InspectorStack anchors bottom-right — this is the one
      // fixed-position corner neither of those already claims.
      className="fixed right-4 top-20 z-30 w-80 max-w-full rounded-lg border border-slate-200 bg-white/95 p-4 shadow-xl backdrop-blur dark:border-slate-700 dark:bg-slate-900/95"
    >
      <div className="flex items-start justify-between gap-2">
        <h3 className="text-sm font-semibold">Conversation</h3>
        <button
          type="button"
          aria-label="Close"
          onClick={onClose}
          className="rounded px-1.5 text-slate-400 hover:bg-slate-100 hover:text-slate-700 dark:hover:bg-slate-800 dark:hover:text-slate-200"
        >
          ×
        </button>
      </div>
      <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-2 gap-y-1 text-xs">
        <dt className="text-slate-500 dark:text-slate-400">Source</dt>
        <dd className="truncate font-mono" title={edge.from}>{edge.from}</dd>
        <dt className="text-slate-500 dark:text-slate-400">Destination</dt>
        <dd className="truncate font-mono" title={edge.to}>{edge.to}</dd>
        <dt className="text-slate-500 dark:text-slate-400">Bytes (window)</dt>
        <dd>{formatBytes(edge.bytes)}</dd>
        <dt className="text-slate-500 dark:text-slate-400">Packets</dt>
        <dd>{edge.packets.toLocaleString()}</dd>
        <dt className="text-slate-500 dark:text-slate-400">Records</dt>
        <dd>{edge.recordCount.toLocaleString()}</dd>
      </dl>
      <Link
        to={flowPairExplorerPath("/flows", edge.from, edge.to)}
        className="mt-3 inline-block text-sm text-accent-700 underline dark:text-accent-300"
      >
        View in Flow Explorer
      </Link>
    </div>
  );
}
