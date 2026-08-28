// SPDX-License-Identifier: Apache-2.0

// T-4006: `POST /changesets/{id}/freeze-override` — the audited escape
// hatch for a declared freeze-window policy rule, following the exact
// shape T-2604's break-glass established (breakGlass.ts): a written reason
// is required server-side, the override is recorded and audited under its
// own action, and it is pinned to the ops it was taken for.
//
// UNLIKE break-glass, this override does not gate an authorization check —
// a freeze window is an ordinary VALIDATE-time policy finding, so
// overriding it does not remove the finding. It downgrades it to a visible
// warning naming the override. The finding staying on screen, annotated,
// IS the "audited, not silent" property: an operator applying under an
// override still sees exactly what they overrode.
import { apiFetch } from "../api/client";
import { readCsrfCookie } from "../api/auth";
import type { Finding, FreezeOverrideRecord } from "../api/types";
import type { PolicyResult } from "../api/policies";

/** The reserved policy-rule tag convention a freeze window declares itself
 * with (internal/change.PolicyTagFreeze). */
export const FREEZE_TAG = "freeze";

/** Whether the panel should be offered: the changeset carries a currently-
 * blocking (`severity: error`) `policy.violation` finding — the ONE ground
 * truth for "is a deny rule actually stopping this apply right now", since
 * an already-overridden rule's finding is downgraded to `warning`
 * server-side (policy_eval.go's policyFinding) — AND `POST /policies/test`
 * (usePolicyVerdictQuery, already fetched for PolicyVerdictPanel) shows a
 * freeze-tagged rule among the violations.
 *
 * The two are checked independently rather than correlated 1:1 by rule id:
 * the wire `Finding` carries no structured `ruleId` (policyVerdict.ts's own
 * doc comment explains why this client never parses one out of the message
 * string), so this is a best-effort UI HINT, never an enforcement decision
 * — offering the override when the real cause is a different deny rule
 * costs nothing (the block simply persists, visibly, if it does not help);
 * never offering it when a freeze rule truly is blocking would be the
 * failure that matters, so this deliberately leans toward offering it. */
export function freezeBlocksApply(findings: readonly Finding[], result: PolicyResult | undefined): boolean {
  const hasBlockingPolicyFinding = findings.some((f) => f.code === "policy.violation" && f.severity === "error");
  if (!hasBlockingPolicyFinding) return false;
  return (result?.rules ?? []).some(
    (r) => (r.tags ?? []).includes(FREEZE_TAG) && (r.violatingOps ?? []).length > 0,
  );
}

/** The server's own bound on a stored reason
 * (internal/change.maxFreezeOverrideReasonLen). */
export const MAX_FREEZE_OVERRIDE_REASON_LEN = 1000;

/** What invoking it does, stated before the confirm control — mirrors
 * BREAK_GLASS_CONSEQUENCES's role exactly, adjusted for the one real
 * difference: this does not touch an authorization gate, it downgrades a
 * validate-time finding to a visible warning. */
export const FREEZE_OVERRIDE_CONSEQUENCES: readonly string[] = [
  "An audit entry is written under its own action, `change.freeze_override`, naming you, the changeset and the reason you type.",
  "The freeze rule's finding stays on the changeset — it is downgraded from blocking to a visible warning that names the override, never silently removed.",
  "It affects ONLY the freeze-tagged rule(s) that matched. Any other policy deny, validation error, or approval requirement still blocks exactly as before.",
  "It is pinned to the operations it was taken for. Editing the draft afterwards does not carry it over — the freeze blocks again, and a fresh override has to be taken.",
];

/** Whether a typed reason is one the server will accept. */
export function freezeOverrideReasonError(reason: string): string | undefined {
  const trimmed = reason.trim();
  if (trimmed.length === 0) {
    return "A written reason is required. An override with no justification is exactly what this ceremony exists to prevent.";
  }
  if (trimmed.length > MAX_FREEZE_OVERRIDE_REASON_LEN) {
    return `The reason must be at most ${String(MAX_FREEZE_OVERRIDE_REASON_LEN)} characters; this one is ${String(trimmed.length)}.`;
  }
  return undefined;
}

/** POST /changesets/{id}/freeze-override — `netWrite` + CSRF. Records the
 * override; the very next validate/apply/diff decides, server-side, whether
 * it applies to the ops actually staged. `503 freeze_override_unavailable`
 * on a daemon with no freeze-override store, a deployment fact rather than
 * a refusal of this particular changeset. */
export function invokeFreezeOverride(id: string, reason: string): Promise<FreezeOverrideRecord> {
  return apiFetch<FreezeOverrideRecord>(`/changesets/${encodeURIComponent(id)}/freeze-override`, {
    method: "POST",
    json: { reason: reason.trim() },
    csrfToken: readCsrfCookie(),
  });
}
