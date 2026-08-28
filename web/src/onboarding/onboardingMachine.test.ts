// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import type { OnboardingProgress } from "../api/types";
import {
  completeStep,
  dismissOnboarding,
  freshOnboardingProgress,
  isOnboardingDismissed,
  isOnboardingDone,
  resumeOnboarding,
  shouldShowPanel,
  shouldShowReopenPill,
  skipStep,
  stepAfter,
} from "./onboardingMachine";

function progress(overrides: Partial<OnboardingProgress> = {}): OnboardingProgress {
  return { ...freshOnboardingProgress(), ...overrides };
}

describe("freshOnboardingProgress", () => {
  it("starts at step 1, nothing skipped/completed, not dismissed", () => {
    expect(freshOnboardingProgress()).toEqual({
      version: 1,
      dismissedAt: null,
      currentStep: "found-summary",
      skippedSteps: [],
      completedSteps: [],
    });
  });
});

describe("stepAfter", () => {
  it("walks the fixed order found-summary -> protected -> lldp -> health -> done", () => {
    expect(stepAfter("found-summary")).toBe("protected");
    expect(stepAfter("protected")).toBe("lldp");
    expect(stepAfter("lldp")).toBe("health");
    expect(stepAfter("health")).toBe("done");
  });

  it("done stays done", () => {
    expect(stepAfter("done")).toBe("done");
  });
});

describe("completeStep / skipStep", () => {
  it("completeStep records the step completed and advances", () => {
    const next = completeStep(progress({ currentStep: "found-summary" }));
    expect(next.completedSteps).toEqual(["found-summary"]);
    expect(next.skippedSteps).toEqual([]);
    expect(next.currentStep).toBe("protected");
  });

  it("skipStep records the step skipped and advances (AC1: skipping works)", () => {
    const next = skipStep(progress({ currentStep: "protected" }));
    expect(next.skippedSteps).toEqual(["protected"]);
    expect(next.completedSteps).toEqual([]);
    expect(next.currentStep).toBe("lldp");
  });

  it("chains through all four steps to done, mixing skip and complete", () => {
    let p = freshOnboardingProgress();
    p = completeStep(p); // found-summary done -> protected
    p = skipStep(p); // protected skipped -> lldp
    p = completeStep(p); // lldp done -> health
    p = completeStep(p); // health done -> done
    expect(p.currentStep).toBe("done");
    expect(p.completedSteps).toEqual(["found-summary", "lldp", "health"]);
    expect(p.skippedSteps).toEqual(["protected"]);
  });

  it("does not duplicate a step already recorded, and is a no-op once done", () => {
    const done = progress({ currentStep: "done", completedSteps: ["found-summary", "protected", "lldp", "health"] });
    expect(completeStep(done)).toBe(done);
    expect(skipStep(done)).toBe(done);
  });
});

describe("dismiss / resume", () => {
  it("dismiss sets dismissedAt to the given timestamp without touching currentStep", () => {
    const p = progress({ currentStep: "lldp" });
    const dismissed = dismissOnboarding(p, 12345);
    expect(dismissed.dismissedAt).toBe(12345);
    expect(dismissed.currentStep).toBe("lldp");
    expect(isOnboardingDismissed(dismissed)).toBe(true);
  });

  it("resume clears dismissedAt, leaving currentStep exactly where it was (resuming works, AC1)", () => {
    const dismissed = dismissOnboarding(progress({ currentStep: "health" }), 999);
    const resumed = resumeOnboarding(dismissed);
    expect(resumed.dismissedAt).toBeNull();
    expect(resumed.currentStep).toBe("health");
    expect(isOnboardingDismissed(resumed)).toBe(false);
  });
});

describe("isOnboardingDone / shouldShowPanel / shouldShowReopenPill", () => {
  it("is not done and shows the panel for a fresh install", () => {
    const p = freshOnboardingProgress();
    expect(isOnboardingDone(p)).toBe(false);
    expect(shouldShowPanel(p)).toBe(true);
    expect(shouldShowReopenPill(p)).toBe(false);
  });

  it("shows the reopen pill instead of the panel while dismissed and not done", () => {
    const p = dismissOnboarding(progress({ currentStep: "protected" }), 1);
    expect(shouldShowPanel(p)).toBe(false);
    expect(shouldShowReopenPill(p)).toBe(true);
  });

  it("shows neither once done, dismissed or not", () => {
    const done = progress({ currentStep: "done" });
    expect(isOnboardingDone(done)).toBe(true);
    expect(shouldShowPanel(done)).toBe(false);
    expect(shouldShowReopenPill(done)).toBe(false);

    const doneAndDismissed = dismissOnboarding(done, 5);
    expect(shouldShowPanel(doneAndDismissed)).toBe(false);
    expect(shouldShowReopenPill(doneAndDismissed)).toBe(false);
  });
});
