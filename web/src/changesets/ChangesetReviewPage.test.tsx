import { afterEach, describe, expect, it } from "vitest";
import { render } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { ChangesetReviewPage } from "./ChangesetReviewPage";
import { useChangesetDrawerStore } from "./store";

function renderAt(id: string) {
  return render(
    <MemoryRouter initialEntries={[`/changesets/${id}/review`]}>
      <Routes>
        <Route path="/changesets/:id/review" element={<ChangesetReviewPage />} />
        <Route path="/" element={<div data-testid="home">home</div>} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("ChangesetReviewPage", () => {
  afterEach(() => {
    useChangesetDrawerStore.getState().reset();
  });

  it("sets the shared drawer store's activeId and requests review, then redirects to /", async () => {
    const { findByTestId } = renderAt("cs-shared-123");
    await findByTestId("home");
    const state = useChangesetDrawerStore.getState();
    expect(state.activeId).toBe("cs-shared-123");
    expect(state.reviewRequested).toBe(true);
  });

  it("renders nothing of its own (hands off to the app-wide drawer, never a second dialog)", () => {
    const { container } = renderAt("cs-shared-456");
    // Only the redirect target's placeholder ends up mounted — this
    // component itself contributes no DOM, so a second <ReviewApplyScreen/>
    // (the app-wide ChangesetDrawer's own) can never collide with one this
    // page might otherwise have rendered.
    expect(container.textContent).not.toContain("Review & apply");
  });
});
