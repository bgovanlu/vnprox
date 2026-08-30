// SPDX-License-Identifier: Apache-2.0

// T-2802: the guided tour panel for the hosted read-only demo.
//
// Renders nothing unless this instance is the public demo (usePublicDemo),
// so a normal daemon — and a local `vnproxd --demo`, which is not a public
// instance — is byte-identical to what it was.
//
// Shaped after OnboardingWalkthrough: an always-mounted panel in normal
// document flow between the top bar and <main>, never a modal, never a
// focus trap, minimisable to a pill. The card requires the tour to be
// "resumable and skippable", and both come from tourMachine plus one
// scratch key in the edge's per-visitor store — which is also what makes
// one visitor's progress invisible to another (AC3).
//
// Every step is data (tourScript.ts). This component has no per-step
// branch, so adding a stop to the tour is a diff in a script file that
// reads like prose.
import { useEffect, useRef, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";

import { Button } from "../components/Button";
import { TOUR_STEPS, tourStep } from "./tourScript";
import {
  completeStep,
  dismissTour,
  isTourDone,
  normalizeTourProgress,
  previousStep,
  restartTour,
  resumeTour,
  shouldShowPanel,
  shouldShowReopenPill,
  skipStep,
  stepNumber,
  type TourProgress,
} from "./tourMachine";
import { useIsPublicDemo } from "./usePublicDemo";
import { fetchVisitorState, saveVisitorState } from "./visitorApi";

/** The scratch key this visitor's progress is stored under. */
export const TOUR_STATE_KEY = "tour";

export function GuidedTour() {
  const isPublicDemo = useIsPublicDemo();
  const navigate = useNavigate();
  const location = useLocation();
  const [progress, setProgress] = useState<TourProgress | null>(null);
  const loaded = useRef(false);

  useEffect(() => {
    if (!isPublicDemo || loaded.current) return;
    loaded.current = true;
    void (async () => {
      // A read failure is not worth surfacing: the tour is an offer, and a
      // visitor who cannot reach their own scratch state is better served
      // by a tour that starts over than by an error about a feature they
      // did not ask for.
      const stored: unknown = await fetchVisitorState<unknown>(TOUR_STATE_KEY).catch(() => null);
      setProgress(normalizeTourProgress(stored));
    })();
  }, [isPublicDemo]);

  if (!isPublicDemo || progress === null) return null;

  function apply(next: TourProgress, goTo?: string): void {
    setProgress(next);
    // Fire and forget, deliberately: the panel must advance at click speed,
    // and the only cost of a failed save is that a reload restarts the
    // tour. Rejections are swallowed for the same reason the load above is.
    void saveVisitorState(TOUR_STATE_KEY, next).catch(() => undefined);
    if (goTo !== undefined && goTo !== location.pathname) {
      void navigate(goTo);
    }
  }

  if (shouldShowPanel(progress)) {
    const step = tourStep(progress.currentStep);
    if (step === undefined) return null;
    const onRoute = location.pathname === step.route;
    const index = stepNumber(progress.currentStep);
    const last = index === TOUR_STEPS.length;
    const current = progress;

    return (
      <section
        aria-label="Guided tour"
        data-testid="guided-tour"
        className="flex w-full max-w-2xl shrink-0 flex-col overflow-hidden border-b border-border bg-white shadow-sm dark:bg-slate-950"
      >
        <div className="flex items-center justify-between gap-2 bg-slate-50 px-3 py-2 dark:bg-slate-800/60">
          <span className="flex items-baseline gap-1.5 text-sm font-medium">
            <span className="text-xs font-normal text-fg-muted" data-testid="guided-tour-step">
              {index}/{TOUR_STEPS.length}
            </span>
            <span>{step.title}</span>
          </span>
          <button
            type="button"
            aria-label="Minimize guided tour"
            onClick={() => { apply(dismissTour(current, Date.now())); }}
            className="text-fg-muted hover:text-slate-700 dark:hover:text-slate-200"
          >
            ▾
          </button>
        </div>
        <div className="flex flex-col gap-3 p-3 text-sm">
          <p className="text-fg-muted">{step.body}</p>
          <p className="text-xs text-fg-subtle">
            <span className="font-semibold">Look for:</span> {step.lookFor}
          </p>
          <div className="flex flex-wrap gap-2">
            {onRoute ? (
              <Button size="sm" variant="primary" onClick={() => {
                const next = completeStep(current);
                apply(next, tourStep(next.currentStep)?.route);
              }}>
                {last ? "Finish" : "Next"}
              </Button>
            ) : (
              <Button size="sm" variant="primary" onClick={() => { void navigate(step.route); }}>
                Show me
              </Button>
            )}
            <Button size="sm" variant="ghost" onClick={() => {
              const next = skipStep(current);
              apply(next, tourStep(next.currentStep)?.route);
            }}>
              Skip
            </Button>
            {index > 1 && (
              <Button size="sm" variant="ghost" onClick={() => {
                const next = previousStep(current);
                apply(next, tourStep(next.currentStep)?.route);
              }}>
                Back
              </Button>
            )}
          </div>
        </div>
      </section>
    );
  }

  if (shouldShowReopenPill(progress)) {
    const done = isTourDone(progress);
    const current = progress;
    return (
      <div className="shrink-0 border-b border-border bg-white px-4 py-1.5 dark:bg-slate-950">
        <button
          type="button"
          data-testid="guided-tour-reopen"
          onClick={() => {
            const next = done ? restartTour() : resumeTour(current);
            apply(next, tourStep(next.currentStep)?.route);
          }}
          className="rounded-full border border-border bg-slate-50 px-3 py-1 text-xs font-medium hover:bg-slate-100 dark:bg-slate-900 dark:hover:bg-slate-800"
        >
          {done
            ? "Take the tour again"
            : `Resume the tour (${String(stepNumber(progress.currentStep))}/${String(TOUR_STEPS.length)})`}
        </button>
      </div>
    );
  }

  return null;
}
