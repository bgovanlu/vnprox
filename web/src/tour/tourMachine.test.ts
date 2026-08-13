import { describe, expect, it } from "vitest";

import {
  completeStep,
  dismissTour,
  freshTourProgress,
  isTourDismissed,
  isTourDone,
  normalizeTourProgress,
  previousStep,
  restartTour,
  resumeTour,
  shouldShowPanel,
  shouldShowReopenPill,
  skipStep,
  stepAfter,
  stepNumber,
  TOUR_DONE,
  type TourProgress,
} from "./tourMachine";
import { TOUR_STEP_IDS } from "./tourScript";

const first = TOUR_STEP_IDS[0] ?? "";
const second = TOUR_STEP_IDS[1] ?? "";
const last = TOUR_STEP_IDS[TOUR_STEP_IDS.length - 1] ?? "";

describe("freshTourProgress", () => {
  it("starts at the first step with nothing done", () => {
    const p = freshTourProgress();
    expect(p.currentStep).toBe(first);
    expect(p.completedSteps).toEqual([]);
    expect(p.skippedSteps).toEqual([]);
    expect(p.dismissedAt).toBeNull();
  });
});

describe("stepAfter", () => {
  it("walks the script's fixed order", () => {
    expect(stepAfter(first)).toBe(second);
  });
  it("terminates at done", () => {
    expect(stepAfter(last)).toBe(TOUR_DONE);
    expect(stepAfter(TOUR_DONE)).toBe(TOUR_DONE);
  });
  it("treats an unknown id as the end rather than looping", () => {
    expect(stepAfter("a-step-that-was-renamed")).toBe(TOUR_DONE);
  });
});

describe("completeStep / skipStep", () => {
  it("records a completed step and advances", () => {
    const p = completeStep(freshTourProgress());
    expect(p.currentStep).toBe(second);
    expect(p.completedSteps).toEqual([first]);
    expect(p.skippedSteps).toEqual([]);
  });

  it("records a skipped step separately — skipped is not completed", () => {
    const p = skipStep(freshTourProgress());
    expect(p.currentStep).toBe(second);
    expect(p.skippedSteps).toEqual([first]);
    expect(p.completedSteps).toEqual([]);
  });

  it("does not duplicate a step re-completed after going back", () => {
    let p = completeStep(freshTourProgress());
    p = previousStep(p);
    p = completeStep(p);
    expect(p.completedSteps).toEqual([first]);
  });

  it("is a no-op once done", () => {
    const done: TourProgress = { ...freshTourProgress(), currentStep: TOUR_DONE };
    expect(completeStep(done)).toEqual(done);
    expect(skipStep(done)).toEqual(done);
  });

  it("reaches done after stepping through every step", () => {
    let p = freshTourProgress();
    for (const _ of TOUR_STEP_IDS) {
      expect(isTourDone(p)).toBe(false);
      p = completeStep(p);
    }
    expect(isTourDone(p)).toBe(true);
    expect(p.completedSteps).toEqual([...TOUR_STEP_IDS]);
  });
});

describe("previousStep", () => {
  it("goes back, because a tour is an invitation and not a funnel", () => {
    const p = previousStep(completeStep(freshTourProgress()));
    expect(p.currentStep).toBe(first);
  });
  it("does nothing at the first step", () => {
    const p = freshTourProgress();
    expect(previousStep(p)).toEqual(p);
  });
  it("returns to the last step from done", () => {
    const done: TourProgress = { ...freshTourProgress(), currentStep: TOUR_DONE };
    expect(previousStep(done).currentStep).toBe(last);
  });
});

describe("dismiss / resume", () => {
  it("minimising keeps the place — the card's 'resumable'", () => {
    const mid = completeStep(freshTourProgress());
    const hidden = dismissTour(mid, 1_700_000_000_000);
    expect(isTourDismissed(hidden)).toBe(true);
    expect(shouldShowPanel(hidden)).toBe(false);
    expect(shouldShowReopenPill(hidden)).toBe(true);
    expect(hidden.currentStep).toBe(mid.currentStep);

    const back = resumeTour(hidden);
    expect(back.currentStep).toBe(mid.currentStep);
    expect(shouldShowPanel(back)).toBe(true);
  });

  it("offers a restart once finished, rather than disappearing forever", () => {
    const done: TourProgress = { ...freshTourProgress(), currentStep: TOUR_DONE };
    expect(shouldShowPanel(done)).toBe(false);
    expect(shouldShowReopenPill(done)).toBe(true);
    expect(restartTour().currentStep).toBe(first);
  });
});

describe("normalizeTourProgress", () => {
  it("accepts a round trip through the visitor scratch store", () => {
    const p = skipStep(completeStep(freshTourProgress()));
    expect(normalizeTourProgress(JSON.parse(JSON.stringify(p)) as unknown)).toEqual(p);
  });

  it("falls back to fresh for anything unusable", () => {
    for (const raw of [null, undefined, 7, "done", {}, { currentStep: 3 }, []]) {
      expect(normalizeTourProgress(raw).currentStep).toBe(first);
    }
  });

  it("restarts rather than stranding a visitor on a step that no longer exists", () => {
    const stale = { version: 1, currentStep: "a-step-that-was-renamed", completedSteps: [], skippedSteps: [], dismissedAt: null };
    expect(normalizeTourProgress(stale).currentStep).toBe(first);
  });

  it("keeps done, which is a real step id", () => {
    const p = normalizeTourProgress({ version: 1, currentStep: TOUR_DONE, completedSteps: ["map"], skippedSteps: [], dismissedAt: null });
    expect(p.currentStep).toBe(TOUR_DONE);
    expect(p.completedSteps).toEqual(["map"]);
  });

  it("drops non-string entries from the step lists", () => {
    const p = normalizeTourProgress({ currentStep: first, completedSteps: ["map", 3, null], skippedSteps: "nope", dismissedAt: "soon" });
    expect(p.completedSteps).toEqual(["map"]);
    expect(p.skippedSteps).toEqual([]);
    expect(p.dismissedAt).toBeNull();
  });
});

describe("stepNumber", () => {
  it("is 1-based and reports the count for done", () => {
    expect(stepNumber(first)).toBe(1);
    expect(stepNumber(last)).toBe(TOUR_STEP_IDS.length);
    expect(stepNumber(TOUR_DONE)).toBe(TOUR_STEP_IDS.length);
  });
});
