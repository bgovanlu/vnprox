// SPDX-License-Identifier: Apache-2.0

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
import { EmptyState, type EmptyStateVariant } from "../components/EmptyState";
import { Tooltip } from "../components/Tooltip";
import type { PictogramKind } from "../icons/registry";
import type { FindingAck, Severity } from "../api/types";

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
  /** T-2402: this finding's currently-active acknowledgement, if any. The
   * server evaluates expiry, so anything present here is still in force. */
  ack?: FindingAck;
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
  /** T-2402: called with a finding's id when "Acknowledge" is clicked. The
   * caller collects the reason (required) and the optional expiry — this
   * component deliberately owns no dialog, matching its "presentational,
   * source-agnostic" contract above. Omitted = no acknowledge affordance. */
  onAck?: (id: string) => void;
  /** T-2402: called with a finding's id when "Un-acknowledge" is clicked. */
  onUnack?: (id: string) => void;
  /** T-2408: when provided, each FIXABLE finding renders a checkbox and the
   * caller drives a batch action. Acknowledged findings are never selectable —
   * the server refuses them in a batch, so offering the checkbox would build
   * a selection that can only fail. */
  selectedIds?: ReadonlySet<string>;
  onToggleSelected?: (id: string) => void;
  /** Called with a fixable finding's id when its "Create fixing changeset"
   * button is clicked. Omitted entirely (no button rendered) if the caller
   * has no fix action to offer. */
  onFix?: (id: string) => void;
  /** T-3912: when provided, a finding that names at least one `refs` entry
   * renders a "Show blast radius" button beside its ref list, calling this
   * with the finding's own `refs` and `detail` (as a ready-made label — the
   * one piece of finding-specific context this generic component already
   * carries). The caller (blastRadiusFocus.ts's
   * `blastRadiusRequestFromFindingRefs`) turns that into a topology-page
   * focus request — this component itself stays source-agnostic and owns
   * no navigation, matching every other action slot here. Omitted = no
   * such button, so a caller with nothing to show a map for is unaffected. */
  onShowBlastRadius?: (refs: string[], label: string) => void;
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
  /** T-4209: which EmptyIllustration to show when `findings` is empty.
   * Defaults to "node" (a cluster-wide "nothing to report" glyph, matching
   * AuditPage's own choice for the same "no findings recorded" shape) since
   * this component's findings sources span drift, LLDP mismatch and IPAM
   * conflicts, not one domain noun. */
  emptyIcon?: PictogramKind;
  /** T-4209: defaults to "empty". A caller whose empty state is actually a
   * filtered-to-nothing result (e.g. FindingsStreamPanel's own
   * "No findings match this filter" title) can pass "filtered" here to get
   * the matching badge/tone without this component losing its
   * source-agnostic contract. */
  emptyVariant?: EmptyStateVariant;
  className?: string;
}

const SEVERITY_CLASSES: Record<Severity, string> = {
  error: "border-status-critical bg-status-critical-soft text-status-critical",
  warning: "border-status-degraded bg-status-degraded-soft text-status-degraded",
  info: "border-status-info bg-status-info-soft text-status-info",
};

const SEVERITY_LABEL: Record<Severity, string> = {
  error: "Error",
  warning: "Warning",
  info: "Info",
};

export function FindingsList({
  findings,
  onFix,
  onAck,
  onUnack,
  onShowBlastRadius,
  selectedIds,
  onToggleSelected,
  fixingId,
  fixDisabledReason,
  emptyTitle = "No findings",
  emptyDescription = "Nothing to report right now.",
  emptyIcon = "node",
  emptyVariant = "empty",
  className,
}: FindingsListProps) {
  if (findings.length === 0) {
    return (
      <EmptyState
        icon={emptyIcon}
        variant={emptyVariant}
        title={emptyTitle}
        description={emptyDescription}
        className={className}
      />
    );
  }

  return (
    <ul className={clsx("flex flex-col gap-2", className)} aria-label="Findings">
      {findings.map((f) => (
        <li
          key={f.id}
          className={clsx("rounded-md border p-3 text-sm", SEVERITY_CLASSES[f.severity])}
        >
          <div className="flex items-start justify-between gap-3">
            <div className="flex items-start gap-2">
              {/* T-2408: only fixable, un-acknowledged findings are
                  selectable. The server refuses an acknowledged finding in a
                  batch, so offering its checkbox would only build a selection
                  that fails on submit. */}
              {onToggleSelected && f.fixable && !f.ack && (
                <input
                  type="checkbox"
                  className="mt-1 h-3.5 w-3.5 shrink-0 accent-accent-600"
                  checked={selectedIds?.has(f.id) ?? false}
                  aria-label={`Select finding: ${f.detail}`}
                  onChange={() => { onToggleSelected(f.id); }}
                />
              )}
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
                    <span className="text-[10px] text-slate-600 dark:text-slate-400">
                      {f.nodes.join(", ")}
                    </span>
                  )}
                  {f.ack && (
                    <span className="rounded bg-black/10 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide dark:bg-white/15">
                      Acknowledged
                    </span>
                  )}
                </div>
                <p>{f.detail}</p>
                {f.refs && f.refs.length > 0 && (
                  <p className="text-[10px] text-slate-600 dark:text-slate-400">
                    {f.refs.join(" · ")}
                  </p>
                )}
                {/* The reason is shown, not just the fact — an
                    acknowledgement with no visible justification is the
                    unexplained silence T-2402 exists to avoid. */}
                {f.ack && (
                  <p className="text-[10px] text-slate-600 dark:text-slate-300">
                    {f.ack.reason} — {f.ack.ackedBy}
                    {f.ack.expiresAt ? `, until ${new Date(f.ack.expiresAt * 1000).toLocaleDateString()}` : ""}
                  </p>
                )}
              </div>
            </div>
            <div className="flex shrink-0 items-center gap-2">
              {onShowBlastRadius && f.refs && f.refs.length > 0 && (
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={() => {
                    onShowBlastRadius(f.refs ?? [], f.detail);
                  }}
                >
                  Show blast radius
                </Button>
              )}
              {f.action && (
                <Button variant="secondary" size="sm" onClick={f.action.onClick}>
                  {f.action.label}
                </Button>
              )}
              {f.ack
                ? onUnack && (
                    <Button variant="secondary" size="sm" onClick={() => { onUnack(f.id); }}>
                      Un-acknowledge
                    </Button>
                  )
                : onAck && (
                    <Button variant="secondary" size="sm" onClick={() => { onAck(f.id); }}>
                      Acknowledge
                    </Button>
                  )}
              {/* An acknowledged finding keeps no fix button: fixing what you
                  have just declared intentional is a contradiction, and the
                  server refuses it in a batch for the same reason. */}
              {f.fixable && !f.ack && onFix && (
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
