// SPDX-License-Identifier: Apache-2.0

// One `spec_reconciliation` finding, rendered as the three positions it
// actually reports rather than as a two-way diff.
//
// The two actions live side by side here and are never merged into one
// "reconcile" control. They point in opposite directions:
//
//   Restore intent   moves the CLUSTER to the document. Stages a draft
//                    changeset; applying it changes the network.
//   Adopt reality    moves the DOCUMENT to the cluster. Opens a pull
//                    request; the network is untouched.
//
// Each opens its own confirmation (the parent owns that state, so only one can
// ever be open) and each is audited separately by the daemon. A single click
// here starts one confirmation and calls nothing.
import { Button } from "../components/Button";
import { HelpAnchor } from "../help/HelpAnchor";
import type { DriftFinding, Reconciliation, SpecPosition } from "../api/types";
import { useAdoptionQuery } from "./queries";
import {
  POSITIONS,
  POSITION_LABEL,
  POSITION_MEANING,
  cellText,
  oddPositionOut,
  pairLabel,
  pairSummary,
  presenceLabel,
  valueAt,
} from "./positions";

function severityClass(severity: DriftFinding["severity"]): string {
  switch (severity) {
    case "error":
      return "border-status-critical bg-status-critical-soft";
    case "warning":
      return "border-status-degraded bg-status-degraded-soft";
    default:
      return "border-slate-200 bg-slate-50 dark:border-slate-800 dark:bg-slate-900/60";
  }
}

function PresenceChip({ position, present }: { position: SpecPosition; present: boolean }) {
  return (
    <span
      title={POSITION_MEANING[position]}
      className={
        present
          ? "rounded border border-slate-300 px-2 py-0.5 text-xs text-slate-800 dark:border-slate-600 dark:text-slate-100"
          : "rounded border border-dashed border-slate-400 px-2 py-0.5 text-xs text-slate-500 dark:border-slate-600 dark:text-slate-400"
      }
    >
      {POSITION_LABEL[position]}: {presenceLabel(position, present)}
    </span>
  );
}

/** The pull request an earlier "adopt reality" already opened for this
 * finding, if any. A failed read renders as "could not check" — never as
 * "not adopted", which is a definite answer this component would not have. */
function AdoptionLink({ findingId }: { findingId: string }) {
  const { data, isLoading, error } = useAdoptionQuery(findingId);

  if (isLoading) {
    return <p className="text-xs text-fg-subtle">Checking for an existing proposal…</p>;
  }
  if (error) {
    return (
      <p className="text-xs text-fg-subtle">
        Could not check whether this finding was already adopted.
      </p>
    );
  }
  if (data === null || data === undefined) {
    return null;
  }
  const label =
    data.pullRequestId === undefined || data.pullRequestId === ""
      ? "the open pull request"
      : `pull request #${data.pullRequestId}`;
  return (
    <p className="text-xs text-slate-600 dark:text-slate-300">
      Already adopted as{" "}
      {data.pullRequestUrl === undefined || data.pullRequestUrl === "" ? (
        <span className="font-mono">{label}</span>
      ) : (
        <a
          href={data.pullRequestUrl}
          target="_blank"
          rel="noreferrer"
          className="font-medium text-accent-700 underline dark:text-accent-300"
        >
          {label}
        </a>
      )}{" "}
      on <code className="font-mono">{data.branch}</code>. Merging it is your repository&apos;s business, not
      vnprox&apos;s.
    </p>
  );
}

export interface ReconciliationCardProps {
  finding: DriftFinding;
  reconciliation: Reconciliation;
  /** `"unavailable"` only when it is CERTAIN adopting cannot work (no
   * `[gitsync]` section at all). Otherwise `"unknown"`: no route reports
   * whether a push credential is configured, so the daemon's own `501` is
   * what settles it. */
  adoptAvailability: "unavailable" | "unknown";
  /** Tooltip naming the missing capability, or undefined when allowed. */
  writeDisabledReason?: string;
  onRestoreIntent: () => void;
  onAdoptReality: () => void;
}

export function ReconciliationCard({
  finding,
  reconciliation,
  adoptAvailability,
  writeDisabledReason,
  onRestoreIntent,
  onAdoptReality,
}: ReconciliationCardProps) {
  const odd = oddPositionOut(reconciliation);
  const offersRestore = reconciliation.actions.restoreIntent;
  const offersAdopt = reconciliation.actions.adoptReality;

  return (
    <li className={`rounded-md border p-3 ${severityClass(finding.severity)}`}>
      <div className="flex flex-wrap items-baseline gap-2">
        <code className="font-mono text-sm font-semibold text-slate-900 dark:text-slate-100">
          {reconciliation.ref}
        </code>
        <span className="text-xs uppercase tracking-wide text-slate-600 dark:text-slate-300">
          {finding.severity}
        </span>
        {finding.nodes.length > 0 && (
          <span className="text-xs text-fg-subtle">on {finding.nodes.join(", ")}</span>
        )}
      </div>
      <p className="mt-1 text-sm text-slate-800 dark:text-slate-100">{finding.detail}</p>

      <div className="mt-2 flex flex-wrap gap-2">
        <PresenceChip position="spec" present={reconciliation.inSpec} />
        <PresenceChip position="config" present={reconciliation.inConfig} />
        <PresenceChip position="live" present={reconciliation.inLive} />
      </div>

      {odd !== undefined && (
        <p className="mt-2 text-sm text-slate-700 dark:text-slate-200">
          The odd one out is <strong>{POSITION_LABEL[odd]}</strong> — the other two agree.
        </p>
      )}

      {reconciliation.fields.length > 0 && (
        <div className="mt-3 overflow-x-auto">
          <table className="w-full table-auto text-left">
            <caption className="sr-only">Field values at each position</caption>
            <thead>
              <tr className="text-xs uppercase tracking-wide text-fg-subtle">
                <th className="pb-1 pr-4 font-semibold">Field</th>
                {POSITIONS.map((p) => (
                  <th key={p} className="pb-1 pr-4 font-semibold" title={POSITION_MEANING[p]}>
                    {POSITION_LABEL[p]}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {reconciliation.fields.map((field) => (
                <tr key={field.field} className="border-t border-slate-200 align-top dark:border-slate-800">
                  <td className="py-1 pr-4 font-mono text-xs text-slate-700 dark:text-slate-200">{field.field}</td>
                  {POSITIONS.map((p) => {
                    const cell = cellText(valueAt(field, p));
                    return (
                      <td
                        key={p}
                        className={
                          cell.known
                            ? "py-1 pr-4 font-mono text-xs text-slate-900 dark:text-slate-100"
                            : "py-1 pr-4 text-xs italic text-fg-subtle"
                        }
                      >
                        {cell.text}
                      </td>
                    );
                  })}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <ul className="mt-3 flex flex-col gap-1">
        {reconciliation.pairs.map((pair) => (
          <li key={`${pair.a}/${pair.b}`} className="text-xs text-slate-600 dark:text-slate-300">
            <span className="font-semibold">{pairLabel(pair)}:</span> {pairSummary(pair)}
          </li>
        ))}
      </ul>

      <div className="mt-3 border-t border-slate-200 pt-3 dark:border-slate-800">
        <h4 className="flex items-center gap-2 text-sm font-semibold">
          Reconcile
          <HelpAnchor topic="spec-reconciliation" />
        </h4>
        <div className="mt-2 flex flex-wrap items-center gap-2">
          {offersRestore ? (
            <Button
              size="sm"
              disabled={writeDisabledReason !== undefined}
              title={writeDisabledReason}
              onClick={onRestoreIntent}
            >
              Restore intent…
            </Button>
          ) : (
            <span className="text-xs text-fg-subtle">
              Restoring intent is not offered: the document declares nothing this cluster would have to change.
            </span>
          )}

          {adoptAvailability === "unavailable" ? (
            <span className="text-xs text-fg-subtle">
              Adopting reality is unavailable: with no <code className="font-mono">[gitsync]</code> repository
              there is nowhere to commit a document to.
            </span>
          ) : offersAdopt ? (
            <Button
              size="sm"
              variant="secondary"
              disabled={writeDisabledReason !== undefined}
              title={writeDisabledReason}
              onClick={onAdoptReality}
            >
              Adopt reality…
            </Button>
          ) : (
            <span className="text-xs text-fg-subtle">
              Adopting reality is not offered: the document already describes this entity as the cluster has it.
            </span>
          )}
        </div>
        {!offersRestore && !offersAdopt && adoptAvailability !== "unavailable" && (
          <p className="mt-2 text-xs text-fg-subtle">
            Neither action is offered. That is a real answer: this divergence exists between the file and the
            kernel, and no spec commit resolves it — a reload or an ordinary changeset does.
          </p>
        )}
        <div className="mt-2">
          <AdoptionLink findingId={finding.id} />
        </div>
      </div>
    </li>
  );
}
