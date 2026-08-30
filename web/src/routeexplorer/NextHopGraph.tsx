// SPDX-License-Identifier: Apache-2.0

// T-3903's visual next-hop graph: which interface each main-table route
// leaves on, and (for an indirect route) the gateway beyond it. This is a
// small, semantic, keyboard/screen-reader-friendly diagram — not a canvas
// render — deliberately not built on web/src/topology's canvasDraw.ts/
// canvasScene.ts primitives (owned by a concurrently-working agent this
// task must stay out of; see this task's completion report for the
// reasoning) but structured the same conceptual way a topology view is:
// grouped by device, one row per device, each row's destinations laid out
// as a simple flex chip list with an arrow between "device" and
// "destinations" columns.
import { ArrowRight } from "lucide-react";
import clsx from "clsx";
import type { FIBRoute } from "../api/types";

export interface NextHopGraphProps {
  /** The node's main-table FIB routes (every other table is noise for a
   * "where does traffic actually go" graph — the local/broadcast/anycast/
   * multicast pseudo-routes in the local table don't represent a
   * forwarding decision). */
  routes: FIBRoute[];
  /** The currently highlighted route (from a lookup result), if any —
   * compared by identity by (dst, dev, gateway) triple since FIBRoute
   * carries no id. */
  highlighted?: FIBRoute;
}

function routeKey(r: FIBRoute): string {
  return `${r.dst}|${r.dev}|${r.gateway ?? ""}`;
}

interface DeviceGroup {
  dev: string;
  routes: FIBRoute[];
}

function groupByDevice(routes: FIBRoute[]): DeviceGroup[] {
  const byDev = new Map<string, FIBRoute[]>();
  for (const r of routes) {
    const list = byDev.get(r.dev) ?? [];
    list.push(r);
    byDev.set(r.dev, list);
  }
  return Array.from(byDev.entries())
    .map(([dev, devRoutes]) => ({ dev, routes: devRoutes }))
    .sort((a, b) => a.dev.localeCompare(b.dev));
}

export function NextHopGraph({ routes, highlighted }: NextHopGraphProps) {
  const mainRoutes = routes.filter((r) => r.table === "main" && r.type === "unicast");
  const groups = groupByDevice(mainRoutes);
  const highlightedKey = highlighted ? routeKey(highlighted) : undefined;

  if (groups.length === 0) {
    return <p className="text-sm text-fg-muted">No forwarding routes in the main table.</p>;
  }

  return (
    <ul aria-label="Next-hop graph, grouped by outgoing interface" className="flex flex-col gap-2">
      {groups.map((group) => (
        <li
          key={group.dev}
          className="flex flex-wrap items-center gap-2 rounded-md border border-border bg-white p-2 dark:bg-slate-900"
        >
          <span className="rounded bg-slate-100 px-2 py-1 font-mono text-xs font-medium text-slate-800 dark:bg-slate-800 dark:text-slate-200">
            {group.dev}
          </span>
          <ArrowRight aria-hidden="true" className="h-4 w-4 shrink-0 text-fg-muted" />
          <ul className="flex flex-wrap gap-1.5" aria-label={`Destinations via ${group.dev}`}>
            {group.routes.map((r) => {
              const isHighlighted = highlightedKey !== undefined && routeKey(r) === highlightedKey;
              return (
                <li
                  key={routeKey(r)}
                  className={clsx(
                    "rounded-full border px-2 py-0.5 font-mono text-xs",
                    isHighlighted
                      ? "border-blue-500 bg-blue-50 font-semibold text-blue-800 dark:border-blue-400 dark:bg-blue-950/50 dark:text-blue-200"
                      : "border-border-strong bg-slate-50 text-slate-700 dark:bg-slate-800/60 dark:text-slate-300",
                  )}
                >
                  {r.dst}
                  {r.gateway ? ` via ${r.gateway}` : " (on-link)"}
                </li>
              );
            })}
          </ul>
        </li>
      ))}
    </ul>
  );
}
