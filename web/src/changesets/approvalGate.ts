// T-2003's client-side echo of the server's approval gate. This is UI
// convenience ONLY: internal/change's beginApply is the actual
// authorization decision, re-checked server-side on every apply attempt
// from stored state. blocksApply below exists so the Apply button can be
// disabled with an explanatory message instead of letting the operator
// click it and get a surprise 422 approval_required — it must never be the
// only place this is enforced (see ReviewApplyScreen.tsx's own doc note and
// ApprovalPanel.tsx's doc comment).
import type { ApprovalState } from "../api/types";

/** True when this deployment's policy requires an approved decision before
 * apply, and the changeset does not currently have one. `approval`
 * undefined (older response, or the review surface not decorated onto this
 * particular read) is treated as "not blocking" — the server is still the
 * one that actually enforces this, so an absent/stale client-side hint
 * never widens what the server will accept; it can only under-warn, never
 * under-enforce. */
export function blocksApply(approval: ApprovalState | undefined): boolean {
  if (!approval) return false;
  return approval.required && approval.status !== "approved";
}

/** The Apply button's disabled-reason message when blocksApply is true. */
export const APPROVAL_REQUIRED_MESSAGE =
  "This deployment requires review approval before a changeset can apply. Ask a reviewer to approve it above.";
