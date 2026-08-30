// SPDX-License-Identifier: Apache-2.0

// T-3001: the plan — `POST /spec/import`.
//
// There is no `plan` route and no `diff` route in this family. The plan IS
// this call's response, and two things about it have to be said in the UI
// rather than discovered:
//
//   1. It is a `netWrite` action with a side effect. `internal/api/spec.go`
//      calls the change engine's Create unconditionally, so every plan —
//      including a clean one with zero ops — leaves a DRAFT CHANGESET behind.
//      A button labelled "plan" that quietly stages something is the kind of
//      surprise this product exists to not have, so the label says it.
//   2. `ops` and `notInSpec` are two different facts. A clean plan is
//      "no operations" AND "nothing undeclared", and both are stated even when
//      both are zero — `notInSpec` is reported, never deleted, so an operator
//      needs to see it was looked for.
import { useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "../components/Button";
import { HelpAnchor } from "../help/HelpAnchor";
import type { SpecImportResult } from "../api/spec";
import { notInSpecSummary, opsSummary } from "./positions";
import { useImportSpecMutation } from "./queries";

interface SpecPlanPanelProps {
  /** The working document from the document panel. */
  content: string;
  /** Tooltip naming the missing capability, or undefined when allowed. */
  writeDisabledReason?: string;
}

export function SpecPlanPanel({ content, writeDisabledReason }: SpecPlanPanelProps) {
  const importMutation = useImportSpecMutation();
  const [result, setResult] = useState<SpecImportResult | undefined>(undefined);
  const [planError, setPlanError] = useState<string | undefined>(undefined);

  const canWrite = writeDisabledReason === undefined;
  const empty = content.trim() === "";

  async function plan(): Promise<void> {
    setPlanError(undefined);
    setResult(undefined);
    try {
      setResult(await importMutation.mutateAsync(content));
    } catch (err) {
      // The daemon's own text: a `specVersion` mismatch and an unparseable
      // document are both `400 validation_failed` with different messages.
      setPlanError(err instanceof Error ? err.message : "could not plan this document against live state");
    }
  }

  return (
    <section aria-labelledby="spec-plan-heading">
      <h2 id="spec-plan-heading" className="flex items-center gap-2 text-lg font-semibold">
        The plan
        <HelpAnchor topic="spec-plan" />
      </h2>
      <p className="mt-1 text-sm text-fg-muted">
        Diffs the working document against live state. This is the same primitive the automation contract
        specifies for a <code className="font-mono">terraform plan</code>-shaped check — and it stages a draft
        changeset for the result, every time, including when the result is empty. It never applies anything.
      </p>

      <div className="mt-3">
        <Button
          size="sm"
          disabled={!canWrite || empty || importMutation.isPending}
          title={writeDisabledReason}
          onClick={() => {
            void plan();
          }}
        >
          {importMutation.isPending ? "Planning…" : "Plan against live state (stages a draft)"}
        </Button>
        {empty && (
          <p className="mt-1 text-xs text-fg-muted">
            Load or paste a document above first — there is nothing to plan.
          </p>
        )}
      </div>

      {planError !== undefined && (
        <div
          role="alert"
          className="mt-3 rounded-md border border-status-critical bg-status-critical-soft p-3 text-sm"
        >
          <p className="font-semibold text-fg">The daemon refused this document</p>
          <p className="mt-1 text-slate-800 dark:text-slate-100">{planError}</p>
        </div>
      )}

      {result !== undefined && (
        <div className="mt-3 rounded-md border border-border p-3">
          {/* Two facts, two paragraphs, always both — a clean plan is the
           * conjunction of them and reads as one sentence about ops only if
           * they are collapsed. */}
          <p className="text-sm text-slate-800 dark:text-slate-100">{opsSummary(result.ops.length)}</p>
          <p className="mt-1 text-sm text-slate-800 dark:text-slate-100">
            {notInSpecSummary(result.notInSpec.length)}
          </p>

          {result.ops.length > 0 && (
            <ul className="mt-2 flex flex-col gap-1">
              {result.ops.map((op, i) => (
                // Keyed by the server-assigned op id where there is one; the
                // index is the fallback for the (transient, never-reordered)
                // case of a response whose ops carry none.
                <li
                  key={op.id !== undefined && op.id !== "" ? op.id : `${op.op}:${op.target ?? ""}:${String(i)}`}
                  className="font-mono text-xs text-fg-body"
                >
                  {op.op} {op.target ?? ""}
                </li>
              ))}
            </ul>
          )}

          {result.notInSpec.length > 0 && (
            <ul className="mt-2 flex flex-col gap-1">
              {result.notInSpec.map((ref) => (
                <li key={ref} className="font-mono text-xs text-fg-body">
                  {ref}
                </li>
              ))}
            </ul>
          )}

          <p className="mt-3 text-xs text-fg-muted">
            The draft this plan created is <code className="font-mono">{result.id}</code>. Nothing has been
            applied; discard it from the review screen if the plan was only a question.
          </p>
          <Link
            to={`/changesets/${encodeURIComponent(result.id)}/review`}
            className="mt-1 inline-block text-sm font-medium text-accent-fg underline"
          >
            Open the draft in the review screen
          </Link>
        </div>
      )}
    </section>
  );
}
