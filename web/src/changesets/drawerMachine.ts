// Pure state machine mapping a changeset's server-side Status (+ the
// drawer's own "review requested" UI flag) onto what the drawer should
// currently show. Framework-free and independently Vitest-able (T-207
// acceptance criterion 5: "Vitest on drawer state machine") — the actual
// <ChangesetDrawer/> component is a thin renderer of whatever this
// function returns, not a second place transition logic lives.
//
// The authoritative lifecycle state itself lives server-side
// (internal/change.Changeset's Status — see changeset.go's
// allowedTransitions); this module never invents a client-side status, it
// only decides which *view* of that status the drawer/review UI renders,
// plus the one piece of state that's genuinely client-only: whether the
// user has asked to move from "still editing" to "reviewing".
import type { Changeset, ChangesetStatus } from "../api/types";

export type DrawerView =
  /** Nothing being drafted right now. */
  | "empty"
  /** A draft (or a validated draft the user is still editing) with the op
   * list open for accumulate/reorder/remove. */
  | "drafting"
  /** The Review & apply screen: Summary/File diff/Plan tabs. */
  | "reviewing"
  /** Apply is in flight (server status "applying"); UI should be
   * non-interactive except waiting. */
  | "applying"
  /** The commit-confirm countdown window; renders the full-width banner. */
  | "awaiting_confirm"
  /** A terminal status (committed/rolled_back/failed/discarded): the
   * outcome banner, not the drafting UI. */
  | "done";

const TERMINAL_STATUSES: ReadonlySet<ChangesetStatus> = new Set([
  "committed",
  "rolled_back",
  "failed",
  "discarded",
]);

/**
 * `changeset` is undefined when there's no active draft at all (drawer
 * empty). `reviewRequested` is true once the user has clicked "Review &
 * apply" and false again once they close that screen (going "back" to
 * drafting) or discard/apply — see useChangesetDrawerStore for where this
 * flag actually lives.
 */
export function computeDrawerView(changeset: Changeset | undefined, reviewRequested: boolean): DrawerView {
  if (!changeset) return "empty";
  if (TERMINAL_STATUSES.has(changeset.status)) return "done";
  if (changeset.status === "applying") return "applying";
  if (changeset.status === "awaiting_confirm") return "awaiting_confirm";
  // draft | validated
  return reviewRequested ? "reviewing" : "drafting";
}

/** Whether the drawer's op list may still be edited (reorder/remove/add) —
 * mirrors internal/change.Changeset.Editable() so the frontend never lets a
 * user attempt a PUT the backend would reject with 409 invalid_transition. */
export function isDraftEditable(changeset: Changeset | undefined): boolean {
  if (!changeset) return false;
  return changeset.status === "draft" || changeset.status === "validated";
}

/** Whether "Review & apply" should even be clickable: there must be at
 * least one op, and no *error*-severity finding may exist unless every
 * error-severity finding has a one-click `fix` (the drawer still lets the
 * user open the review screen either way — findings actually block the
 * apply button there per docs/features/change-management.md §2: "Errors
 * block apply" — but there is nothing productive to review with zero ops). */
export function canReview(changeset: Changeset | undefined): boolean {
  return !!changeset && changeset.ops.length > 0;
}

/** Whether the review screen's Apply button should be enabled: no blocking
 * (error-severity) findings, and — if there are any warnings — the
 * "apply with warnings" checkbox has been ticked (docs/features/
 * change-management.md §2: "warnings require an explicit checkbox"). */
export function canApply(changeset: Changeset | undefined, warningsAcknowledged: boolean): boolean {
  if (!changeset || changeset.ops.length === 0) return false;
  const hasError = changeset.findings.some((f) => f.severity === "error");
  if (hasError) return false;
  const hasWarning = changeset.findings.some((f) => f.severity === "warning");
  return !hasWarning || warningsAcknowledged;
}
