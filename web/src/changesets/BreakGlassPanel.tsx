// SPDX-License-Identifier: Apache-2.0

// T-3002: the emergency break-glass affordance on the two-person rule
// (T-2604's `POST /changesets/{id}/break-glass`, which had no caller in
// web/src at all).
//
// The whole point of this component is that it is HARD to use, so the
// interaction is explicit about it:
//
//   step "closed"        one link, which reveals the consequences. Nothing
//                        is sent, nothing is recorded.
//   step "consequences"  the four things invoking it does, and a single
//                        control to proceed. There is deliberately no reason
//                        field and no record button on this step — an
//                        operator cannot arrive at the override without the
//                        consequences having been on screen first.
//   step "confirm"       the reason field and the record button, which stays
//                        disabled until a non-empty reason is typed.
//
// So the blocked state is three deliberate actions away from an override,
// never one click. The server enforces the reason regardless (a `400
// validation_failed` without one); this form is the echo.
import { useState } from "react";
import { Button } from "../components/Button";
import { HelpAnchor } from "../help/HelpAnchor";
import { BREAK_GLASS_CONSEQUENCES, reasonError } from "./breakGlass";
import type { BreakGlassRecord } from "../api/types";

export interface BreakGlassPanelProps {
  /** An override already on record for this changeset, from the server's own
   * two-person state. Present means the ceremony has happened. */
  record: BreakGlassRecord | undefined;
  /** Whether the two-person gate is currently what is blocking apply. The
   * panel offers the override only then: break-glass overrides the
   * distinct-approver count and nothing else, so offering it against a
   * validation failure or a policy deny would promise something it cannot
   * do. */
  blocked: boolean;
  onInvoke: (reason: string) => void;
  pending: boolean;
  /** The daemon's own refusal, when the POST failed. */
  error: string | undefined;
}

type Step = "closed" | "consequences" | "confirm";

function instant(seconds: number): string {
  if (seconds === 0) return "an unrecorded time";
  return new Date(seconds * 1000).toLocaleString();
}

export function BreakGlassPanel({ record, blocked, onInvoke, pending, error }: BreakGlassPanelProps) {
  const [step, setStep] = useState<Step>("closed");
  const [reason, setReason] = useState("");
  const invalid = reasonError(reason);

  if (record !== undefined) {
    return (
      <section
        className="mt-3 rounded-md border border-red-300 bg-red-50 p-3 text-xs text-red-900 dark:border-red-700 dark:bg-red-950 dark:text-red-100"
        aria-label="Break-glass override on record"
        data-testid="break-glass-record"
      >
        <div className="flex items-center gap-1.5">
          <p className="font-medium">An emergency break-glass override is on record for this changeset.</p>
          <HelpAnchor topic="break-glass" />
        </div>
        <dl className="mt-1 space-y-0.5">
          <div>
            <dt className="inline font-medium">Invoked by: </dt>
            <dd className="inline">{record.invokedBy === "" ? "an unrecorded principal" : record.invokedBy}</dd>
          </div>
          <div>
            <dt className="inline font-medium">At: </dt>
            <dd className="inline">{instant(record.invokedAt)}</dd>
          </div>
          <div>
            <dt className="inline font-medium">Reason: </dt>
            <dd className="inline">{record.reason}</dd>
          </div>
          <div>
            <dt className="inline font-medium">Its finding becomes acknowledgeable: </dt>
            <dd className="inline">{instant(record.ackableAt)}</dd>
          </div>
        </dl>
      </section>
    );
  }

  if (!blocked) {
    return null;
  }

  return (
    <section
      className="mt-3 rounded-md border border-red-300 p-3 text-xs text-red-900 dark:border-red-700 dark:text-red-100"
      aria-label="Emergency break-glass"
      data-testid="break-glass-panel"
    >
      <div className="flex items-center gap-1.5">
        <h3 className="font-medium">Emergency override</h3>
        <HelpAnchor topic="break-glass" />
      </div>

      {step === "closed" && (
        <>
          <p className="mt-1">
            If this is an emergency and the second approver cannot be reached, the distinct-approver requirement can be
            overridden. Doing so is audited and raises a finding nobody can clear for a day.
          </p>
          <Button
            variant="ghost"
            size="sm"
            className="mt-2"
            onClick={() => {
              setStep("consequences");
            }}
          >
            Read what break-glass does…
          </Button>
        </>
      )}

      {step !== "closed" && (
        <div className="mt-1" data-testid="break-glass-consequences">
          <p className="font-medium">Before you do this, here is exactly what it does:</p>
          <ul className="mt-1 list-disc space-y-1 pl-4">
            {BREAK_GLASS_CONSEQUENCES.map((line) => (
              <li key={line}>{line}</li>
            ))}
          </ul>
        </div>
      )}

      {step === "consequences" && (
        <div className="mt-2 flex gap-2">
          <Button
            variant="ghost"
            size="sm"
            onClick={() => {
              setStep("closed");
            }}
          >
            Cancel
          </Button>
          <Button
            variant="destructive"
            size="sm"
            onClick={() => {
              setStep("confirm");
            }}
          >
            I understand — continue to the override
          </Button>
        </div>
      )}

      {step === "confirm" && (
        <div className="mt-2">
          <label className="flex flex-col gap-1">
            <span className="font-medium">Why are you overriding the two-person rule? (required, recorded)</span>
            <textarea
              value={reason}
              onChange={(e) => {
                setReason(e.target.value);
              }}
              rows={3}
              aria-label="Break-glass reason"
              className="w-full rounded border border-red-300 px-1.5 py-1 text-xs dark:border-red-700 dark:bg-slate-900"
            />
          </label>
          {invalid !== undefined && reason.length > 0 && (
            <p className="mt-1" role="alert">
              {invalid}
            </p>
          )}
          {error !== undefined && (
            <p className="mt-1" role="alert" data-testid="break-glass-error">
              {error}
            </p>
          )}
          <div className="mt-2 flex gap-2">
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                setStep("closed");
                setReason("");
              }}
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              size="sm"
              disabled={invalid !== undefined || pending}
              onClick={() => {
                onInvoke(reason);
              }}
            >
              Record break-glass override
            </Button>
          </div>
        </div>
      )}
    </section>
  );
}
