// SPDX-License-Identifier: Apache-2.0

// T-4006: the freeze-window override affordance, mirroring BreakGlassPanel's
// exact interaction shape (T-2604) — the ceremony is deliberately high
// friction, and the friction is the feature: open, read the consequences,
// then type a reason and record. One click from the blocked state must
// never produce an override.
import { useState } from "react";
import { Button } from "../components/Button";
import { HelpAnchor } from "../help/HelpAnchor";
import { FREEZE_OVERRIDE_CONSEQUENCES, freezeOverrideReasonError } from "./freezeOverride";
import type { FreezeOverrideRecord } from "../api/types";

export interface FreezeOverridePanelProps {
  /** An override already on record for this changeset, from a recent
   * InvokeFreezeOverride response. Present means the ceremony has happened
   * — but note (unlike break-glass) it does not by itself prove the
   * blocking finding is currently gone: an edit since invoking it (a
   * different ops fingerprint) un-pins it server-side, and the very next
   * validate/findings read is what actually shows that. */
  record: FreezeOverrideRecord | undefined;
  /** Whether a freeze-tagged deny rule currently appears to be blocking
   * this changeset (freezeBlocksApply). The panel offers the override only
   * then. */
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

export function FreezeOverridePanel({ record, blocked, onInvoke, pending, error }: FreezeOverridePanelProps) {
  const [step, setStep] = useState<Step>("closed");
  const [reason, setReason] = useState("");
  const invalid = freezeOverrideReasonError(reason);

  if (record !== undefined && blocked) {
    // An override is on record, but the freeze is STILL what is blocking —
    // most likely the draft was edited since (the ops fingerprint moved),
    // which un-pins it server-side. Show the prior record for context and
    // still offer to take a fresh one, rather than looking like nothing
    // happened.
    return (
      <section
        className="mt-3 rounded-md border border-amber-300 bg-amber-50 p-3 text-xs text-amber-900 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-100"
        aria-label="Prior freeze override no longer applies"
        data-testid="freeze-override-stale"
      >
        <p className="font-medium">
          A freeze override was recorded by {record.invokedBy === "" ? "an unrecorded principal" : record.invokedBy} at{" "}
          {instant(record.invokedAt)}, but the freeze is blocking again — the draft was likely edited since. A fresh
          override is required.
        </p>
        <FreezeOverrideForm
          step={step}
          setStep={setStep}
          reason={reason}
          setReason={setReason}
          invalid={invalid}
          error={error}
          pending={pending}
          onInvoke={onInvoke}
        />
      </section>
    );
  }

  if (record !== undefined) {
    return (
      <section
        className="mt-3 rounded-md border border-amber-300 bg-amber-50 p-3 text-xs text-amber-900 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-100"
        aria-label="Freeze-window override on record"
        data-testid="freeze-override-record"
      >
        <div className="flex items-center gap-1.5">
          <p className="font-medium">A freeze-window override is on record for this changeset.</p>
          <HelpAnchor topic="freeze-windows" />
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
        </dl>
      </section>
    );
  }

  if (!blocked) {
    return null;
  }

  return (
    <section
      className="mt-3 rounded-md border border-amber-300 p-3 text-xs text-amber-900 dark:border-amber-700 dark:text-amber-100"
      aria-label="Freeze-window override"
      data-testid="freeze-override-panel"
    >
      <div className="flex items-center gap-1.5">
        <h3 className="font-medium">Freeze-window override</h3>
        <HelpAnchor topic="freeze-windows" />
      </div>
      <p className="mt-1">
        A declared freeze window is refusing this changeset. If this is a genuine incident, the freeze can be overridden.
        Doing so is audited and stays visible on the finding it defeats.
      </p>
      <FreezeOverrideForm
        step={step}
        setStep={setStep}
        reason={reason}
        setReason={setReason}
        invalid={invalid}
        error={error}
        pending={pending}
        onInvoke={onInvoke}
      />
    </section>
  );
}

interface FreezeOverrideFormProps {
  step: Step;
  setStep: (s: Step) => void;
  reason: string;
  setReason: (r: string) => void;
  invalid: string | undefined;
  error: string | undefined;
  pending: boolean;
  onInvoke: (reason: string) => void;
}

function FreezeOverrideForm({ step, setStep, reason, setReason, invalid, error, pending, onInvoke }: FreezeOverrideFormProps) {
  return (
    <>
      {step === "closed" && (
        <Button
          variant="ghost"
          size="sm"
          className="mt-2"
          onClick={() => {
            setStep("consequences");
          }}
        >
          Read what a freeze override does…
        </Button>
      )}

      {step !== "closed" && (
        <div className="mt-1" data-testid="freeze-override-consequences">
          <p className="font-medium">Before you do this, here is exactly what it does:</p>
          <ul className="mt-1 list-disc space-y-1 pl-4">
            {FREEZE_OVERRIDE_CONSEQUENCES.map((line) => (
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
            <span className="font-medium">Why are you overriding the freeze? (required, recorded)</span>
            <textarea
              value={reason}
              onChange={(e) => {
                setReason(e.target.value);
              }}
              rows={3}
              aria-label="Freeze override reason"
              className="w-full rounded border border-amber-300 px-1.5 py-1 text-xs dark:border-amber-700 dark:bg-slate-900"
            />
          </label>
          {invalid !== undefined && reason.length > 0 && (
            <p className="mt-1" role="alert">
              {invalid}
            </p>
          )}
          {error !== undefined && (
            <p className="mt-1" role="alert" data-testid="freeze-override-error">
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
              Record freeze override
            </Button>
          </div>
        </div>
      )}
    </>
  );
}
