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
import { Tooltip } from "../components/Tooltip";
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
  /** An optional finding-specific secondary action (T-703: the
   * mgmt_single_path finding launches the management-redundancy wizard —
   * this is not a fixing-changeset action, so it's distinct from onFix).
   * Rendered as a secondary button alongside (or instead of) the fix
   * button. */
  action?: { label: string; onClick: () => void };
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
  /** When set, every "Create fixing changeset" button is disabled and
   * shows this as a tooltip (T-605 read-only sweep: `POST /drift/{id}/fix`
   * creates a brand-new changeset regardless of any already-active draft,
   * so it needs the same disabled-with-tooltip capability gating every
   * other write affordance in this codebase uses — this component stays
   * deliberately capability-agnostic per its own doc comment, so the
   * caller computes this from the session, the same pattern ParamForm's
   * `submitDisabledReason` prop already established). */
  fixDisabledReason?: string;
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
  fixDisabledReason,
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
            <div className="flex shrink-0 items-center gap-2">
              {f.action && (
                <Button variant="secondary" size="sm" onClick={f.action.onClick}>
                  {f.action.label}
                </Button>
              )}
              {f.fixable && onFix && (
                <Tooltip content={fixDisabledReason}>
                  <span>
                    <Button
                      variant="secondary"
                      size="sm"
                      disabled={fixingId === f.id || fixDisabledReason !== undefined}
                      onClick={() => { onFix(f.id); }}
                    >
                      {fixingId === f.id ? "Creating…" : "Create fixing changeset"}
                    </Button>
                  </span>
                </Tooltip>
              )}
            </div>
          </div>
        </li>
      ))}
    </ul>
  );
}
