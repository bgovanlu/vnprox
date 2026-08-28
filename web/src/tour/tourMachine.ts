// SPDX-License-Identifier: Apache-2.0

// T-2802: the guided tour's state, as pure functions.
//
// Structurally this is onboarding/onboardingMachine.ts's twin, and that is
// on purpose: the walkthrough T-605 built IS the tour engine the card points
// at, and this module keeps its semantics — a fixed step order, "done" as a
// terminal step, skip and complete as separate lists, dismiss as minimise
// rather than discard — so the two panels behave identically.
//
// It is a sibling module rather than a reuse of that one for a reason worth
// stating: the walkthrough's steps are not a script, they are four bespoke
// components, and two of them WRITE (PUT /protected-interfaces, POST
// /lldp/install). Those are the right steps for someone who has just
// installed vnprox on their own cluster and exactly the wrong ones for a
// public read-only demo, where both would be refused at the edge. Generalising
// onboardingMachine's closed OnboardingStep union into a generic would have
// changed a T-605-owned type that four other modules import, to share about
// forty lines. So: same semantics, its own steps, its own storage key.
import { TOUR_STEP_IDS } from "./tourScript";

/** Terminal step id. Not a step in TOUR_STEPS — there is nothing after the
 * end. */
export const TOUR_DONE = "done";

export const TOUR_PROGRESS_VERSION = 1 as const;

/** The visitor's own place in the tour. Persisted verbatim into the edge's
 * per-visitor scratch store (T-2802 AC3), never into the daemon's. */
export interface TourProgress {
  version: number;
  currentStep: string;
  completedSteps: string[];
  skippedSteps: string[];
  dismissedAt: number | null;
}

/** Where a visitor who has never taken the tour starts. */
export function freshTourProgress(): TourProgress {
  return {
    version: TOUR_PROGRESS_VERSION,
    currentStep: TOUR_STEP_IDS[0] ?? TOUR_DONE,
    completedSteps: [],
    skippedSteps: [],
    dismissedAt: null,
  };
}

/** Narrows whatever came back from the visitor scratch store — which is
 * opaque JSON the edge never validated — to a TourProgress, falling back to
 * a fresh one.
 *
 * Unknown step ids are treated as "start again" rather than passed through:
 * a saved id that no longer exists in the script would otherwise render an
 * empty panel with no way forward, which is a worse outcome than losing a
 * visitor's place in a demo. */
export function normalizeTourProgress(raw: unknown): TourProgress {
  if (typeof raw !== "object" || raw === null) return freshTourProgress();
  const value = raw as Partial<Record<keyof TourProgress, unknown>>;
  const currentStep = value.currentStep;
  if (typeof currentStep !== "string") return freshTourProgress();
  if (currentStep !== TOUR_DONE && !TOUR_STEP_IDS.includes(currentStep)) return freshTourProgress();
  const dismissedAt = value.dismissedAt;
  return {
    version: typeof value.version === "number" ? value.version : TOUR_PROGRESS_VERSION,
    currentStep,
    completedSteps: stringList(value.completedSteps),
    skippedSteps: stringList(value.skippedSteps),
    dismissedAt: typeof dismissedAt === "number" ? dismissedAt : null,
  };
}

function stringList(raw: unknown): string[] {
  return Array.isArray(raw) ? raw.filter((v): v is string => typeof v === "string") : [];
}

/** The step after `step` in TOUR_STEPS' fixed order. The last step, "done",
 * and any id not in the script all map to "done". */
export function stepAfter(step: string): string {
  const idx = TOUR_STEP_IDS.indexOf(step);
  if (idx === -1 || idx === TOUR_STEP_IDS.length - 1) return TOUR_DONE;
  return TOUR_STEP_IDS[idx + 1] ?? TOUR_DONE;
}

/** Advances past the current step, recording it as completed (deduplicated). */
export function completeStep(progress: TourProgress): TourProgress {
  if (progress.currentStep === TOUR_DONE) return progress;
  const step = progress.currentStep;
  const completedSteps = progress.completedSteps.includes(step) ? progress.completedSteps : [...progress.completedSteps, step];
  return { ...progress, currentStep: stepAfter(step), completedSteps };
}

/** Advances past the current step, recording it as skipped instead — the
 * card's "skippable". */
export function skipStep(progress: TourProgress): TourProgress {
  if (progress.currentStep === TOUR_DONE) return progress;
  const step = progress.currentStep;
  const skippedSteps = progress.skippedSteps.includes(step) ? progress.skippedSteps : [...progress.skippedSteps, step];
  return { ...progress, currentStep: stepAfter(step), skippedSteps };
}

/** Steps back, so a visitor who clicked past something can go back to it.
 * The tour is an invitation, not a funnel. */
export function previousStep(progress: TourProgress): TourProgress {
  const idx = progress.currentStep === TOUR_DONE ? TOUR_STEP_IDS.length : TOUR_STEP_IDS.indexOf(progress.currentStep);
  if (idx <= 0) return progress;
  return { ...progress, currentStep: TOUR_STEP_IDS[idx - 1] ?? progress.currentStep };
}

/** Minimises the panel without discarding progress — the card's "resumable". */
export function dismissTour(progress: TourProgress, at: number): TourProgress {
  return { ...progress, dismissedAt: at };
}

/** Reopens a minimised tour exactly where it was. */
export function resumeTour(progress: TourProgress): TourProgress {
  return { ...progress, dismissedAt: null };
}

/** Starts the whole tour again, from the top, forgetting what was skipped. */
export function restartTour(): TourProgress {
  return freshTourProgress();
}

export function isTourDone(progress: TourProgress): boolean {
  return progress.currentStep === TOUR_DONE;
}

export function isTourDismissed(progress: TourProgress): boolean {
  return progress.dismissedAt !== null;
}

/** Whether the full panel should render right now. */
export function shouldShowPanel(progress: TourProgress): boolean {
  return !isTourDone(progress) && !isTourDismissed(progress);
}

/** Whether the small reopen affordance should render instead. Unlike the
 * onboarding walkthrough, this stays available after the tour is finished:
 * a demo visitor who reached the end and wants a second look should not
 * have to clear a cookie to get one. */
export function shouldShowReopenPill(progress: TourProgress): boolean {
  return isTourDone(progress) || isTourDismissed(progress);
}

/** 1-based position of a step, for the "3/6" indicator. "done" reports the
 * step count. */
export function stepNumber(step: string): number {
  const idx = TOUR_STEP_IDS.indexOf(step);
  return idx === -1 ? TOUR_STEP_IDS.length : idx + 1;
}
