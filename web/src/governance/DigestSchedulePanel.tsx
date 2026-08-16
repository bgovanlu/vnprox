// T-3002: the scheduled digest's cadence — `GET`/`PUT /digest/schedule`.
//
// T-2807 landed the runner, the renderer and the store and no route; T-2905's
// route landed with no reader. Until this panel the cadence was configurable
// only by writing to SQLite by hand, which is not "configurable schedule" in
// any sense an operator would recognise.
//
// The panel sits with the alert rules because a digest's recipients ARE the
// alert rules' targets — `ruleIds` is a filter over them, not a second address
// book — but it deliberately does not live inside `web/src/settings/`, which
// another card owns this wave.
import { useEffect, useState } from "react";
import { Button } from "../components/Button";
import { HelpAnchor } from "../help/HelpAnchor";
import { ApiError } from "../api/client";
import { MIN_DIGEST_EVERY_SEC } from "../api/digest";
import {
  CADENCE_CHOICES,
  cadenceError,
  cadenceLabel,
  formFromSchedule,
  ruleFilterLabel,
  updateFromForm,
  type DigestFormState,
} from "./digestSchedule";
import { useDigestScheduleQuery, usePutDigestScheduleMutation } from "./queries";

export function DigestSchedulePanel() {
  const query = useDigestScheduleQuery();
  const save = usePutDigestScheduleMutation();
  const [form, setForm] = useState<DigestFormState | undefined>(undefined);

  // Seed the form from the stored value once it arrives, and re-seed after a
  // successful save so the fields show what the daemon stored rather than what
  // was typed — which is the half of the round trip that is worth asserting.
  const stored = query.data;
  useEffect(() => {
    if (stored !== undefined) {
      setForm(formFromSchedule(stored));
    }
  }, [stored]);

  const notImplemented = query.error instanceof ApiError && query.error.status === 501;
  const unreadable = query.error !== null && !notImplemented;
  const invalid = form === undefined ? undefined : cadenceError(form);

  return (
    <section aria-label="Digest schedule" data-testid="digest-panel" className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <h2 className="text-base font-semibold">Digest schedule</h2>
        <HelpAnchor topic="digest-schedule-panel" />
      </div>
      <p className="text-sm text-slate-600 dark:text-slate-300">
        A periodic summary delivered through the alert rules you already have. A period with nothing to report renders
        as one line rather than a page of "none observed" — a digest that arrives full every week regardless is one
        people learn to delete unread.
      </p>

      {query.isLoading && <p className="text-sm text-slate-500 dark:text-slate-400">Reading the schedule…</p>}

      {notImplemented && (
        <p className="text-sm text-slate-600 dark:text-slate-300" data-testid="digest-unavailable">
          Scheduled digests are not available on this deployment. That is a property of the daemon, not an empty
          schedule — nothing is scheduled and nothing can be. The daemon said:{" "}
          {query.error instanceof Error ? query.error.message : ""}
        </p>
      )}

      {unreadable && (
        <p className="text-sm text-slate-700 dark:text-slate-200" role="status">
          The schedule could not be read, so whether a digest is running is unknown. The daemon said:{" "}
          {query.error instanceof Error ? query.error.message : "the read failed"}
        </p>
      )}

      {stored !== undefined && (
        <div className="rounded-md border border-slate-200 p-3 text-sm dark:border-slate-800" data-testid="digest-stored">
          <p className="font-medium">{stored.enabled ? "Enabled" : "Disabled"}</p>
          <p className="mt-0.5 text-slate-600 dark:text-slate-300">{cadenceLabel(stored.everySec, stored.enabled)}</p>
          <p className="mt-0.5 text-slate-600 dark:text-slate-300">{ruleFilterLabel(stored.ruleIds)}</p>
          <p className="mt-0.5 text-xs text-slate-500 dark:text-slate-400">
            {stored.updatedBy === "" && stored.updatedAt === 0
              ? "Nobody has ever written this schedule."
              : `Last written by ${stored.updatedBy === "" ? "an unrecorded principal" : stored.updatedBy}${
                  stored.updatedAt === 0 ? "" : ` on ${new Date(stored.updatedAt * 1000).toLocaleString()}`
                }.`}
          </p>
          <p className="mt-1 text-xs text-slate-600 dark:text-slate-300" data-testid="digest-last-run">
            {stored.lastRun === null
              ? "No tick has run yet, so there is no outcome to report."
              : `Last run ${stored.lastRun.status}${stored.lastRun.quiet ? " (quiet — nothing to report that period)" : ""}${
                  stored.lastRun.detail === "" ? "" : `: ${stored.lastRun.detail}`
                }`}
          </p>
        </div>
      )}

      {form !== undefined && (
        <div className="flex flex-col gap-2">
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={form.enabled}
              onChange={(e) => {
                setForm({ ...form, enabled: e.target.checked });
              }}
            />
            Send the digest on this cadence
          </label>

          <label className="flex flex-col gap-1 text-sm">
            <span>Cadence (seconds)</span>
            <div className="flex items-center gap-2">
              <input
                type="number"
                min={0}
                value={form.everySec}
                aria-label="Digest cadence seconds"
                onChange={(e) => {
                  setForm({ ...form, everySec: e.target.value });
                }}
                className="w-32 rounded border border-slate-300 px-1.5 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
              />
              {CADENCE_CHOICES.map((choice) => (
                <Button
                  key={choice.seconds}
                  variant="ghost"
                  size="sm"
                  onClick={() => {
                    setForm({ ...form, everySec: String(choice.seconds) });
                  }}
                >
                  {choice.label}
                </Button>
              ))}
            </div>
            <span className="text-xs text-slate-500 dark:text-slate-400">
              An enabled schedule needs at least {MIN_DIGEST_EVERY_SEC} seconds. A disabled one may carry any cadence,
              including none — disabling is how you silence a digest without losing the cadence you chose.
            </span>
          </label>

          <label className="flex flex-col gap-1 text-sm">
            <span>Alert rules to draw from (optional filter)</span>
            <input
              value={form.ruleIds}
              aria-label="Digest alert rule ids"
              onChange={(e) => {
                setForm({ ...form, ruleIds: e.target.value });
              }}
              className="w-full rounded border border-slate-300 px-1.5 py-1 font-mono text-sm dark:border-slate-700 dark:bg-slate-900"
            />
            <span className="text-xs text-slate-500 dark:text-slate-400">
              Comma separated. Leave empty for no filter, which means every alert rule — not "no recipients". A digest
              carries no target of its own; it is delivered through the rules' existing ones, with their quiet hours
              and retries.
            </span>
          </label>

          {invalid !== undefined && (
            <p className="text-xs text-red-700 dark:text-red-300" role="alert" data-testid="digest-cadence-error">
              {invalid}
            </p>
          )}
          {save.error !== null && (
            <p className="text-xs text-red-700 dark:text-red-300" role="alert" data-testid="digest-save-error">
              {save.error.message}
            </p>
          )}

          <div className="flex gap-2">
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                setForm(formFromSchedule(stored));
              }}
            >
              Revert to stored
            </Button>
            <Button
              variant="primary"
              size="sm"
              disabled={invalid !== undefined || save.isPending}
              onClick={() => {
                save.mutate(updateFromForm(form));
              }}
            >
              Save schedule
            </Button>
          </div>
        </div>
      )}
    </section>
  );
}
