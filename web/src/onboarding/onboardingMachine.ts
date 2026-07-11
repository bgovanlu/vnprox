// Pure state machine for the first-login onboarding walkthrough
// (docs/user-guide.md §1; T-605 AC1/AC2: "dismissible, resumable"). Mirrors
// changesets/drawerMachine.ts's split: this module owns every transition as
// a framework-free, independently Vitest-able function; the actual
// <OnboardingWalkthrough/> component is a thin renderer of whatever this
// module computes plus whatever the per-step data queries (topology,
// protected-interfaces suggest, lldp, drift) return.
//
// Unlike the changeset drawer, there is no server-side authoritative status
// to defer to here — the walkthrough's whole state (which step, what was
// skipped/completed, whether it's minimized) is itself the client-owned
// payload persisted via PUT /layouts/onboarding (api/onboarding.ts). This
// module is therefore the single source of truth for what that payload
// means and how it evolves, not just a view over server state.
import type { OnboardingProgress, OnboardingStep } from "../api/types";

/** The four walkthrough steps in the fixed order docs/user-guide.md §1
 * documents, terminated by "done". Exported so the component can render a
 * step indicator without duplicating this ordering. */
export const ONBOARDING_STEPS: readonly OnboardingStep[] = ["found-summary", "protected", "lldp", "health"];

export const ONBOARDING_PROGRESS_VERSION = 1 as const;

/** The state a brand-new install (or a GET 404 — "never run") starts from:
 * step 1, nothing skipped or completed, not dismissed. */
export function freshOnboardingProgress(): OnboardingProgress {
  return {
    version: ONBOARDING_PROGRESS_VERSION,
    dismissedAt: null,
    currentStep: "found-summary",
    skippedSteps: [],
    completedSteps: [],
  };
}

/** The step that follows `step`, per ONBOARDING_STEPS' fixed order. "done"
 * (and any step not found in the order, defensively) maps to "done" —
 * there is nothing after the end. */
export function stepAfter(step: OnboardingStep): OnboardingStep {
  const idx = ONBOARDING_STEPS.indexOf(step);
  if (idx === -1 || idx === ONBOARDING_STEPS.length - 1) {
    return "done";
  }
  const next = ONBOARDING_STEPS[idx + 1];
  return next ?? "done";
}

/** Advances past `progress.currentStep`, recording it in `completedSteps`
 * (deduplicated — re-completing a step is a no-op on the list). Does
 * nothing beyond returning `progress` unchanged if the walkthrough is
 * already "done". */
export function completeStep(progress: OnboardingProgress): OnboardingProgress {
  if (progress.currentStep === "done") return progress;
  const step = progress.currentStep;
  const completedSteps = progress.completedSteps.includes(step)
    ? progress.completedSteps
    : [...progress.completedSteps, step];
  return { ...progress, currentStep: stepAfter(step), completedSteps };
}

/** Advances past `progress.currentStep`, recording it in `skippedSteps`
 * instead of `completedSteps` (AC1: "Skipping and resuming works"). Same
 * done/dedup handling as completeStep. */
export function skipStep(progress: OnboardingProgress): OnboardingProgress {
  if (progress.currentStep === "done") return progress;
  const step = progress.currentStep;
  const skippedSteps = progress.skippedSteps.includes(step) ? progress.skippedSteps : [...progress.skippedSteps, step];
  return { ...progress, currentStep: stepAfter(step), skippedSteps };
}

/** Minimizes the walkthrough ("dismiss" in the task card's vocabulary) —
 * never discards progress, just hides the panel behind the AppShell's
 * reopen pill. `at` is injected (rather than read from Date.now() inside)
 * so this stays a pure function callers can test deterministically. */
export function dismissOnboarding(progress: OnboardingProgress, at: number): OnboardingProgress {
  return { ...progress, dismissedAt: at };
}

/** Reopens a minimized walkthrough exactly where it left off — `currentStep`
 * is untouched, only `dismissedAt` clears. */
export function resumeOnboarding(progress: OnboardingProgress): OnboardingProgress {
  return { ...progress, dismissedAt: null };
}

export function isOnboardingDismissed(progress: OnboardingProgress): boolean {
  return progress.dismissedAt !== null;
}

export function isOnboardingDone(progress: OnboardingProgress): boolean {
  return progress.currentStep === "done";
}

/** Whether the full-panel walkthrough should render at all right now — false
 * once finished (nothing left to walk through) or while minimized (the
 * reopen pill renders instead). Mirrors drawerMachine's computeDrawerView in
 * spirit: one function the component defers to instead of re-deriving this
 * logic inline. */
export function shouldShowPanel(progress: OnboardingProgress): boolean {
  return !isOnboardingDone(progress) && !isOnboardingDismissed(progress);
}

/** Whether the AppShell's small reopen affordance should render — only
 * while there's something to resume (not done) and it's currently
 * minimized. Once "done", neither the panel nor the pill renders again
 * (matching ChangesetDrawer's "render nothing once there's truly nothing to
 * show" convention). */
export function shouldShowReopenPill(progress: OnboardingProgress): boolean {
  return !isOnboardingDone(progress) && isOnboardingDismissed(progress);
}
