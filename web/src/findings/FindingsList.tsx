// A shared findings-stream list — T-305's drift findings today, T-602's
// continuous-checks findings stream (docs/features/monitoring.md §2, "one
// findings stream shared with drift, LLDP mismatch, IPAM conflicts") is
// meant to reuse this same component per that task's own card. Kept
// intentionally decoupled from any one findings source's API types (see
// FindingItem below) so a second source with the same conceptual shape
// (severity, plain-English explanation, affected refs, remediation) never
// needs this file to change.
import clsx from "clsx";
import { Button } from "../components/Button";
import { EmptyState } from "../components/EmptyState";
import type { Severity } from "../api/types";

/** The shape every findings-stream item needs — a structural subset both
 * DriftFinding (api/types.ts) and any future source can satisfy without
 * this component importing that type directly. */
export interface FindingItem {
  id: string;
  severity: Severity;
  detail: string;
  nodes: string[];
  refs?: string[];
  fixable: boolean;
  /** Optional grouping/category label shown as a small pill (T-305's
   * `check` field; a future source's own category concept). */
  category?: string;
}

export interface FindingsListProps {
  findings: FindingItem[];
  /** Called with a fixable finding's id when its "Create fixing changeset"
   * button is clicked. Omitted entirely (no button rendered) if the caller
   * has no fix action to offer. */
  onFix?: (id: string) => void;
  /** The id currently being fixed (disables its button, shows a pending
   * label) — callers own their own mutation's pending state. */
  fixingId?: string;
  emptyTitle?: string;
  emptyDescription?: string;
  className?: string;
}

const SEVERITY_CLASSES: Record<Severity, string> = {
  error: "border-red-300 bg-red-50 text-red-800 dark:border-red-700 dark:bg-red-950 dark:text-red-200",
  warning: "border-amber-300 bg-amber-50 text-amber-800 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-200",
  info: "border-slate-300 bg-slate-50 text-slate-700 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300",
};

const SEVERITY_LABEL: Record<Severity, string> = {
  error: "Error",
  warning: "Warning",
  info: "Info",
};

export function FindingsList({
  findings,
  onFix,
  fixingId,
  emptyTitle = "No findings",
  emptyDescription = "Nothing to report right now.",
  className,
}: FindingsListProps) {
  if (findings.length === 0) {
    return <EmptyState title={emptyTitle} description={emptyDescription} className={className} />;
  }

  return (
    <ul className={clsx("flex flex-col gap-2", className)} aria-label="Findings">
      {findings.map((f) => (
        <li
          key={f.id}
          className={clsx("rounded-md border p-3 text-sm", SEVERITY_CLASSES[f.severity])}
        >
          <div className="flex items-start justify-between gap-3">
            <div className="flex flex-col gap-1">
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-[10px] font-semibold uppercase tracking-wide">
                  {SEVERITY_LABEL[f.severity]}
                </span>
                {f.category && (
                  <span className="rounded bg-black/5 px-1.5 py-0.5 text-[10px] dark:bg-white/10">
                    {f.category}
                  </span>
                )}
                {f.nodes.length > 0 && (
                  <span className="text-[10px] text-slate-500 dark:text-slate-400">
                    {f.nodes.join(", ")}
                  </span>
                )}
              </div>
              <p>{f.detail}</p>
              {f.refs && f.refs.length > 0 && (
                <p className="text-[10px] text-slate-500 dark:text-slate-400">
                  {f.refs.join(" · ")}
                </p>
              )}
            </div>
            {f.fixable && onFix && (
              <Button
                variant="secondary"
                size="sm"
                disabled={fixingId === f.id}
                onClick={() => { onFix(f.id); }}
              >
                {fixingId === f.id ? "Creating…" : "Create fixing changeset"}
              </Button>
            )}
          </div>
        </li>
      ))}
    </ul>
  );
}
