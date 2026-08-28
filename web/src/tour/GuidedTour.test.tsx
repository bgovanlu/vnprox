// SPDX-License-Identifier: Apache-2.0

// Component-level tests for the guided tour, with the edge's visitor
// surface mocked at its api boundary (tour/visitorApi.ts) — the same
// convention OnboardingWalkthrough.test.tsx uses.
//
// The behaviours here are the ones the card names: the panel renders only
// on a public instance, the tour completes, a step can be skipped, and
// progress survives a remount (which is what "resumable" means once the
// state lives in the visitor's own scratch store rather than in the
// component).
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { GuidedTour, TOUR_STATE_KEY } from "./GuidedTour";
import { TOUR_STEPS } from "./tourScript";
import { usePublicDemoStore } from "./usePublicDemo";
import { fetchVisitorState, saveVisitorState } from "./visitorApi";

vi.mock("./visitorApi", () => ({
  fetchVisitorState: vi.fn(() => Promise.resolve(null)),
  saveVisitorState: vi.fn(() => Promise.resolve()),
  fetchVisitorSession: vi.fn(() => Promise.resolve(null)),
}));

const mockedFetch = vi.mocked(fetchVisitorState);
const mockedSave = vi.mocked(saveVisitorState);

function asPublicDemo(): void {
  usePublicDemoStore.getState().setSession({
    publicDemo: true,
    visitor: "visitor-under-test",
    caps: { requestBurst: 120, maxStateBytes: 1024, maxStateEntries: 8 },
  });
}

beforeEach(() => {
  usePublicDemoStore.setState({ session: undefined });
  mockedFetch.mockReset();
  mockedFetch.mockResolvedValue(null);
  mockedSave.mockReset();
  mockedSave.mockResolvedValue(undefined);
});

afterEach(() => {
  usePublicDemoStore.setState({ session: undefined });
});

function renderTour() {
  return render(
    <MemoryRouter initialEntries={["/topology"]}>
      <GuidedTour />
    </MemoryRouter>,
  );
}

describe("GuidedTour", () => {
  it("renders nothing on an instance that is not the public demo", async () => {
    usePublicDemoStore.getState().setSession(null);
    renderTour();
    await waitFor(() => {
      expect(screen.queryByTestId("guided-tour")).toBeNull();
    });
    expect(mockedFetch).not.toHaveBeenCalled();
  });

  it("renders nothing before the edge has answered", () => {
    renderTour();
    expect(screen.queryByTestId("guided-tour")).toBeNull();
  });

  it("opens at the first step on a public instance", async () => {
    asPublicDemo();
    renderTour();
    expect(await screen.findByTestId("guided-tour")).toBeVisible();
    expect(screen.getByTestId("guided-tour-step")).toHaveTextContent(`1/${String(TOUR_STEPS.length)}`);
    expect(screen.getByText(TOUR_STEPS[0]?.title ?? "")).toBeVisible();
  });

  it("completes end to end, persisting each step to the visitor's own scratch store", async () => {
    asPublicDemo();
    const user = userEvent.setup();
    renderTour();
    await screen.findByTestId("guided-tour");

    for (let i = 0; i < TOUR_STEPS.length; i++) {
      const isLast = i === TOUR_STEPS.length - 1;
      await user.click(screen.getByRole("button", { name: isLast ? "Finish" : "Next" }));
    }

    await waitFor(() => {
      expect(screen.queryByTestId("guided-tour")).toBeNull();
    });
    // Finished, but still offered — a visitor who wants a second look
    // should not have to clear a cookie.
    expect(screen.getByTestId("guided-tour-reopen")).toBeVisible();

    expect(mockedSave).toHaveBeenCalledTimes(TOUR_STEPS.length);
    const lastCall = mockedSave.mock.calls.at(-1);
    expect(lastCall?.[0]).toBe(TOUR_STATE_KEY);
    expect(lastCall?.[1]).toMatchObject({
      currentStep: "done",
      completedSteps: TOUR_STEPS.map((s) => s.id),
    });
  });

  it("skips a step, recording it as skipped rather than completed", async () => {
    asPublicDemo();
    const user = userEvent.setup();
    renderTour();
    await screen.findByTestId("guided-tour");

    await user.click(screen.getByRole("button", { name: "Skip" }));

    expect(screen.getByTestId("guided-tour-step")).toHaveTextContent(`2/${String(TOUR_STEPS.length)}`);
    expect(mockedSave.mock.calls[0]?.[1]).toMatchObject({
      skippedSteps: [TOUR_STEPS[0]?.id],
      completedSteps: [],
    });
  });

  it("resumes where the visitor left off", async () => {
    asPublicDemo();
    mockedFetch.mockResolvedValue({
      version: 1,
      currentStep: TOUR_STEPS[2]?.id,
      completedSteps: [TOUR_STEPS[0]?.id, TOUR_STEPS[1]?.id],
      skippedSteps: [],
      dismissedAt: null,
    });

    renderTour();
    await screen.findByTestId("guided-tour");
    expect(screen.getByTestId("guided-tour-step")).toHaveTextContent(`3/${String(TOUR_STEPS.length)}`);
    expect(screen.getByText(TOUR_STEPS[2]?.title ?? "")).toBeVisible();
  });

  it("minimises to a pill that resumes at the same step", async () => {
    asPublicDemo();
    const user = userEvent.setup();
    renderTour();
    await screen.findByTestId("guided-tour");

    await user.click(screen.getByRole("button", { name: "Minimize guided tour" }));
    const pill = await screen.findByTestId("guided-tour-reopen");
    expect(pill).toHaveTextContent(`1/${String(TOUR_STEPS.length)}`);

    await user.click(pill);
    expect(await screen.findByTestId("guided-tour")).toBeVisible();
    expect(screen.getByTestId("guided-tour-step")).toHaveTextContent(`1/${String(TOUR_STEPS.length)}`);
  });

  it("starts over rather than breaking when stored progress names a step that no longer exists", async () => {
    asPublicDemo();
    mockedFetch.mockResolvedValue({ version: 1, currentStep: "a-step-that-was-renamed", completedSteps: [], skippedSteps: [], dismissedAt: null });
    renderTour();
    expect(await screen.findByTestId("guided-tour")).toBeVisible();
    expect(screen.getByTestId("guided-tour-step")).toHaveTextContent(`1/${String(TOUR_STEPS.length)}`);
  });

  it("still shows the tour when the visitor's scratch store cannot be read", async () => {
    asPublicDemo();
    mockedFetch.mockRejectedValue(new Error("edge unreachable"));
    renderTour();
    expect(await screen.findByTestId("guided-tour")).toBeVisible();
  });

  it("keeps advancing when a save fails — losing your place is not worth blocking on", async () => {
    asPublicDemo();
    mockedSave.mockRejectedValue(new Error("413"));
    const user = userEvent.setup();
    renderTour();
    await screen.findByTestId("guided-tour");

    await user.click(screen.getByRole("button", { name: "Next" }));
    expect(screen.getByTestId("guided-tour-step")).toHaveTextContent(`2/${String(TOUR_STEPS.length)}`);
  });

  it("offers a way to the step's own screen when the visitor is elsewhere", async () => {
    asPublicDemo();
    render(
      <MemoryRouter initialEntries={["/somewhere-else"]}>
        <GuidedTour />
      </MemoryRouter>,
    );
    await screen.findByTestId("guided-tour");
    expect(screen.getByRole("button", { name: "Show me" })).toBeVisible();
    expect(screen.queryByRole("button", { name: "Next" })).toBeNull();
  });
});
