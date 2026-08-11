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

/** True when this changeset falls in a protected op class (T-2604) whose
 * distinct-approver requirement is not yet met and no emergency break-glass
 * override is on record. `approval` or `approval.twoPerson` undefined — a
 * deployment with no protected classes, or a changeset in none of them — is
 * not blocking. Exactly like blocksApply above this is UI convenience: the
 * server refuses the apply with `two_person_required` regardless of what
 * this returns, so an absent or stale hint can only under-warn, never
 * under-enforce. */
export function twoPersonBlocksApply(approval: ApprovalState | undefined): boolean {
  const tp = approval?.twoPerson;
  if (!tp || tp.satisfied || tp.breakGlass) return false;
  return tp.required > 0;
}

/** The Apply button's disabled-reason message when twoPersonBlocksApply is
 * true — it names the class and the shortfall, mirroring the server's own
 * refusal, so the operator is told what would satisfy it rather than only
 * that they may not proceed. */
export function twoPersonRequiredMessage(approval: ApprovalState | undefined): string {
  const tp = approval?.twoPerson;
  const have = tp?.approvers?.length ?? 0;
  const required = tp?.required ?? 0;
  // The binding class is the strictest matched one — the same rule the
  // server's own refusal message applies, so the two never name different
  // classes for the same changeset.
  const classes = tp?.classes ?? [];
  const strictest = classes.reduce<{ class: string; approvals: number } | undefined>(
    (best, c) => (best === undefined || c.approvals > best.approvals ? c : best),
    undefined,
  )?.class;
  const where = strictest ? ` (protected class ${strictest})` : "";
  return (
    `This change requires ${String(required)} different people to approve it${where}. ` +
    `${String(have)} of ${String(required)} so far — ask another reviewer to approve, ` +
    `or use break-glass if this is an emergency.`
  );
}
