// T-3002: `POST /changesets/{id}/break-glass` (T-2604's emergency override of
// the two-person rule) and the copy that has to be on screen before it is
// invoked.
//
// The API call lives here rather than in `web/src/api/changesets.ts` because
// this card owns `web/src/changesets/**` and not that file; it is an ordinary
// apiFetch either way, and no component calls fetch directly.
//
// The ceremony is deliberately high friction, and the friction is the
// feature:
//
//   * a written reason is REQUIRED. The server refuses without one
//     (`change.ErrBreakGlassReasonRequired` -> `400 validation_failed`), so
//     the form insisting on it is an echo, never the enforcement.
//   * the consequence is stated BEFORE the confirm control exists, not after
//     it is used.
//   * it takes three deliberate actions to reach: open, acknowledge the
//     consequence, then type a reason and record. One click from the blocked
//     state must never produce an override.
import { apiFetch } from "../api/client";
import { readCsrfCookie } from "../api/auth";
import type { BreakGlassRecord } from "../api/types";

/** internal/change.BreakGlassAckFloor — the finding this override raises
 * cannot be acknowledged for 24 hours. Mirrored here only so the consequence
 * text can name the number; the server owns the rule. */
export const BREAK_GLASS_ACK_FLOOR_HOURS = 24;

/** The server's own bound on a stored reason
 * (internal/change.maxBreakGlassReasonLen). */
export const MAX_BREAK_GLASS_REASON_LEN = 1000;

/** Every consequence of invoking it, stated before the confirm control is
 * rendered. Each line is a fact about what the daemon does, taken from
 * docs/api.md's break-glass paragraph and internal/change/twoperson.go — none
 * of it is this UI's opinion. */
export const BREAK_GLASS_CONSEQUENCES: readonly string[] = [
  "An audit entry is written under its own action, `change.breakglass`, naming you, the changeset and the reason you type. It is not a result value on the apply — an auditor filtering for overrides finds it whether or not the apply that follows succeeds.",
  `An error-severity finding is raised, and nobody — including you — can acknowledge it for ${String(BREAK_GLASS_ACK_FLOOR_HOURS)} hours. It is meant to be reviewed by someone who was not in the room.`,
  "It overrides the distinct-approver count and nothing else. Validation, the review-approval requirement and every other gate still run, and the apply is still refused if any of them refuses.",
  "It is pinned to the operations it was taken for. Editing the draft afterwards does not carry it over — the apply is refused again and a fresh override has to be taken.",
];

/** Whether a typed reason is one the server will accept. Trimmed-empty is
 * refused server-side; over-length is refused server-side; this only stops
 * the operator discovering either as a 400. */
export function reasonError(reason: string): string | undefined {
  const trimmed = reason.trim();
  if (trimmed.length === 0) {
    return "A written reason is required. An override with no justification is the thing this ceremony exists to prevent.";
  }
  if (trimmed.length > MAX_BREAK_GLASS_REASON_LEN) {
    return `The reason must be at most ${String(MAX_BREAK_GLASS_REASON_LEN)} characters; this one is ${String(trimmed.length)}.`;
  }
  return undefined;
}

/** POST /changesets/{id}/break-glass — `netWrite` + CSRF. Records the
 * override; it applies nothing. `503 break_glass_unavailable` on a daemon
 * with no break-glass store, which is a deployment fact and not a refusal of
 * this particular changeset. */
export function invokeBreakGlass(id: string, reason: string): Promise<BreakGlassRecord> {
  return apiFetch<BreakGlassRecord>(`/changesets/${encodeURIComponent(id)}/break-glass`, {
    method: "POST",
    json: { reason: reason.trim() },
    csrfToken: readCsrfCookie(),
  });
}
