// The digest-schedule form's logic, kept out of the component so the two
// things worth pinning are testable on their own:
//
//   1. `everySec: 0` is not a cadence. docs/api.md is explicit: a schedule
//      nobody has written has no cadence, the route reports `0` rather than
//      inventing one, and the runner's weekly fallback is reached only for an
//      ENABLED schedule — "so a client must not read this 0 as 'weekly is
//      already configured'". Rendering `0` as "every 0 seconds", or as
//      "weekly", are both the same bug in different directions.
//   2. The enabled-cadence floor is the server's (3600s), echoed here only so
//      the operator is not told about it by a 400.
import { MIN_DIGEST_EVERY_SEC, type DigestSchedule, type DigestScheduleUpdate } from "../api/digest";

/** A cadence offered in the picker. Values are the daemon's own units
 * (seconds); the weekly entry matches `digest.DefaultEvery`. */
export const CADENCE_CHOICES: readonly { seconds: number; label: string }[] = [
  { seconds: 3600, label: "Hourly (the floor)" },
  { seconds: 21600, label: "Every 6 hours" },
  { seconds: 86400, label: "Daily" },
  { seconds: 604800, label: "Weekly" },
];

export interface DigestFormState {
  enabled: boolean;
  /** Held as a string so a half-typed number is not coerced to 0 — which
   * would be indistinguishable from "no cadence has ever been set". */
  everySec: string;
  /** Comma/whitespace separated alert-rule ids. Empty means "no filter", i.e.
   * every rule, which is what the API's empty list means. */
  ruleIds: string;
}

export function formFromSchedule(schedule: DigestSchedule | undefined): DigestFormState {
  if (schedule === undefined) {
    return { enabled: false, everySec: "", ruleIds: "" };
  }
  return {
    enabled: schedule.enabled,
    everySec: schedule.everySec === 0 ? "" : String(schedule.everySec),
    ruleIds: schedule.ruleIds.join(", "),
  };
}

export function parseRuleIds(raw: string): string[] {
  return raw
    .split(/[,\s]+/)
    .map((s) => s.trim())
    .filter((s) => s.length > 0);
}

/** The refusal the server would give, computed before sending it. `undefined`
 * means the form is sendable. */
export function cadenceError(form: DigestFormState): string | undefined {
  const trimmed = form.everySec.trim();
  if (trimmed === "") {
    if (!form.enabled) return undefined;
    return `An enabled schedule needs a cadence of at least ${String(MIN_DIGEST_EVERY_SEC)} seconds (one hour).`;
  }
  const parsed = Number(trimmed);
  if (!Number.isFinite(parsed) || !Number.isInteger(parsed) || parsed < 0) {
    return "The cadence must be a whole number of seconds.";
  }
  if (form.enabled && parsed < MIN_DIGEST_EVERY_SEC) {
    return `An enabled schedule needs a cadence of at least ${String(MIN_DIGEST_EVERY_SEC)} seconds (one hour); a disabled one may carry any cadence, including none.`;
  }
  return undefined;
}

/** The `PUT` body. Every field is sent — the panel reads the whole object,
 * shows the whole object and writes the whole object back, which is the
 * full-replace contract the route documents. A blank cadence is sent as `0`,
 * the same "no cadence" the route itself reports, never as a substituted
 * default. */
export function updateFromForm(form: DigestFormState): DigestScheduleUpdate {
  const trimmed = form.everySec.trim();
  return {
    enabled: form.enabled,
    everySec: trimmed === "" ? 0 : Number(trimmed),
    ruleIds: parseRuleIds(form.ruleIds),
  };
}

/** How to describe a stored cadence, honestly.
 *
 * `0` on an ENABLED schedule is a real case (the store can hold it, and the
 * runner substitutes weekly at tick time) and must say so, because "weekly"
 * is a substitution the schedule does not itself record. */
export function cadenceLabel(everySec: number, enabled: boolean): string {
  if (everySec === 0) {
    return enabled
      ? "No cadence is stored. The runner substitutes its weekly default at each tick — that substitution is not recorded here, so set a cadence if you want one that is."
      : "No cadence has ever been stored. That is not the same as a weekly default, and not the same as a cadence of zero seconds.";
  }
  const named = CADENCE_CHOICES.find((c) => c.seconds === everySec);
  if (named !== undefined) {
    return `${named.label} (${String(everySec)}s)`;
  }
  return `Every ${String(everySec)} seconds`;
}

/** How to describe the recipients filter. An empty list is "every rule", the
 * API's own no-filter convention — never "no recipients". */
export function ruleFilterLabel(ruleIds: readonly string[]): string {
  if (ruleIds.length === 0) {
    return "No filter set, so every alert rule's targets receive the digest.";
  }
  return `Filtered to ${String(ruleIds.length)} alert rule${ruleIds.length === 1 ? "" : "s"}: ${ruleIds.join(", ")}`;
}
