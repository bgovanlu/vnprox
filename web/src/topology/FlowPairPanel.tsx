// T-1003's guest-pair drill-down: opened when a Flows-layer overlay edge
// (flowEdges.ts's FlowEdge) is clicked on the map — a small, non-modal
// floating summary (mirrors InspectorStack.tsx's bottom-anchored floating
// panel convention, opposite corner so it never collides with an entity
// inspector open at the same time) plus a "view in Flow Explorer" deep
// link carrying the same pair as URL state (flows/urlState.ts).
import { Link } from "react-router-dom";
import { HelpAnchor } from "../help/HelpAnchor";
import type { FlowEdge } from "./flowEdges";
import { flowPairExplorerPath } from "../flows/urlState";
import { conntrackNodeLinkPath } from "../conntrack/urlState";

/** A Ref string's node segment, per inventory.Ref's "kind:node:id" encoding
 * (ParseRef splits on only the first two ':' — mirrors expand.ts's/
 * opSummary.ts's identical helper, kept local here rather than shared so
 * this small pure function doesn't force a cross-feature import). Empty for
 * a cluster-scoped ref (e.g. an SdnVnet ref's node segment is "") — the
 * caller (conntrackNodeLinkPath) treats that as "no node to scope to". */
function refNode(ref: string): string {
  const first = ref.indexOf(":");
  if (first === -1) return "";
  const second = ref.indexOf(":", first + 1);
  if (second === -1) return "";
  return ref.slice(first + 1, second);
}

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
      // top-right (below the page toolbar): Sidebar owns the full left
      // edge (z-50, docs/features/topology.md's map toolbar sits above the
      // canvas) and InspectorStack anchors bottom-right — this is the one
      // fixed-position corner neither of those already claims.
      className="fixed right-4 top-20 z-30 w-80 max-w-full rounded-lg border border-slate-200 bg-white/95 p-4 shadow-xl backdrop-blur dark:border-slate-700 dark:bg-slate-900/95"
    >
      <div className="flex items-start justify-between gap-2">
        <h3 className="flex items-center gap-1.5 text-sm font-semibold">
          Conversation
          <HelpAnchor topic="flow-pair-panel" />
        </h3>
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
      <div className="mt-3 flex flex-col gap-1">
        <Link
          to={flowPairExplorerPath("/flows", edge.from, edge.to)}
          className="inline-block text-sm text-accent-700 underline dark:text-accent-300"
        >
          View in Flow Explorer
        </Link>
        <Link
          to={conntrackNodeLinkPath("/conntrack", refNode(edge.from))}
          className="inline-block text-sm text-accent-700 underline dark:text-accent-300"
        >
          View live connections
        </Link>
      </div>
    </div>
  );
}
