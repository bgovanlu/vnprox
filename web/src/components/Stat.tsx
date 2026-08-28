// SPDX-License-Identifier: Apache-2.0

// T-4208, promoted from dashboard/PluginTile.tsx: "a big tabular-nums
// number, an optional status dot, an optional detail line under it" is
// exactly what every dashboard tile's own body (FindingsSeverityTile,
// DriftStatusTile, PluginTile itself) hand-rolls around its own number —
// PluginTile is the only one of the three that gives it a plugin-facing
// name (`Value`/`Detail`, docs/plugins/dashboard-tile.md), which is why it
// is the one this component is grounded in most directly. Deliberately NOT
// the tile shell itself (dashboard/DashboardTile.tsx already owns that,
// loading/error/empty states included) — Stat is the number atom that goes
// inside one, or inside a KeyValue row, or standalone in a header.
import type { ReactNode } from "react";
import clsx from "clsx";
import type { StatusTone } from "./statusTone";

const DOT_CLASSES: Record<StatusTone, string> = {
  ok: "bg-status-ok",
  degraded: "bg-status-degraded",
  critical: "bg-status-critical",
  info: "bg-status-info",
  unknown: "bg-status-unknown",
};

export interface StatProps {
  /** The headline number/short value. `ReactNode` (not `number`) because
   * PluginTile's own contract renders a plugin's `Value` string verbatim,
   * with no reformatting — the same "no server-side reinterpretation"
   * promise this component must not quietly break for a future adopter. */
  value: ReactNode;
  /** A short caption under the value (PluginTile's `Detail`). */
  description?: ReactNode;
  /** A caption ABOVE the value, for a Stat used standalone rather than
   * inside a card that already titles itself (DashboardTile does). */
  label?: ReactNode;
  status?: StatusTone;
  size?: "sm" | "md";
  className?: string;
}

const VALUE_SIZE: Record<"sm" | "md", string> = {
  sm: "text-lg",
  md: "text-2xl",
};

/** A prominent number with an optional status dot and caption(s) —
 * PluginTile's `Value`/`Detail` shape, generalized. */
export function Stat({ value, description, label, status, size = "md", className }: StatProps) {
  return (
    <div className={clsx("flex items-start gap-2", className)}>
      {status ? <span aria-hidden className={clsx("mt-1.5 h-2.5 w-2.5 shrink-0 rounded-full", DOT_CLASSES[status])} /> : null}
      <div className="flex flex-col gap-0.5">
        {label ? <p className="text-xs font-medium text-slate-600 dark:text-slate-400">{label}</p> : null}
        <p className={clsx("font-semibold tabular-nums text-slate-800 dark:text-slate-100", VALUE_SIZE[size])}>{value}</p>
        {description ? <p className="text-xs text-slate-600 dark:text-slate-400">{description}</p> : null}
      </div>
    </div>
  );
}
