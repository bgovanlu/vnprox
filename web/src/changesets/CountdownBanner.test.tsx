import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider, QueryClient } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ToastProvider } from "../components/Toast";
import type { Changeset } from "../api/types";
import { CountdownBanner } from "./CountdownBanner";
import { useChangesetDrawerStore } from "./store";

/** Minimal matchMedia fake — jsdom has no real implementation, mirroring
 * lib/useReducedMotion.test.ts's own fake. */
function stubPrefersReducedMotion(matches: boolean): void {
  vi.stubGlobal(
    "matchMedia",
    vi.fn().mockReturnValue({
      matches,
      media: "(prefers-reduced-motion: reduce)",
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }),
  );
}

vi.mock("../api/changesets", () => ({
  confirmChangeset: vi.fn((id: string) => Promise.resolve({ id, status: "committed" })),
  rollbackChangeset: vi.fn((id: string) => Promise.resolve({ id, status: "rolled_back" })),
}));

function base(overrides: Partial<Changeset>): Changeset {
  return {
    id: "cs1",
    title: "My draft",
    author: "root@pam",
    status: "applying",
    ops: [],
    findings: [],
    createdAt: 0,
    updatedAt: 0,
    ...overrides,
  };
}

function renderBanner(cs: Changeset) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <CountdownBanner changeset={cs} />
      </ToastProvider>
    </QueryClientProvider>,
  );
}

describe("CountdownBanner", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("shows the applying message while status is applying", () => {
    renderBanner(base({ status: "applying" }));
    expect(screen.getByRole("status")).toHaveTextContent("Applying changeset");
  });

  it("shows the countdown with Confirm/Roll back controls while awaiting_confirm", () => {
    const nowSec = Math.floor(Date.now() / 1000);
    renderBanner(base({ status: "awaiting_confirm", confirmDeadline: nowSec + 90 }));
    const banner = screen.getByRole("alert");
    expect(banner).toHaveTextContent("confirm you still have connectivity");
    expect(screen.getByRole("button", { name: "Confirm" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Roll back now" })).toBeInTheDocument();
  });

  it("renders the committed outcome banner with a Dismiss button that resets the drawer store", async () => {
    renderBanner(base({ status: "committed" }));
    const banner = screen.getByRole("status");
    expect(banner).toHaveTextContent("was applied and committed");

    useChangesetDrawerStore.setState({ activeId: "cs1", drawerOpen: true, reviewRequested: false, warningsAcknowledged: false });
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(useChangesetDrawerStore.getState().activeId).toBeUndefined();
  });

  it("renders the rolled_back outcome with the attributed actor", () => {
    renderBanner(base({ status: "rolled_back", applyLog: { steps: [], rolledBackBy: "system:rollback" } }));
    expect(screen.getByRole("status")).toHaveTextContent("rolled back by system:rollback");
  });

  it("renders the failed outcome pinpointing the failed step", () => {
    renderBanner(base({ status: "failed", applyLog: { steps: [], failedStep: 2 } }));
    expect(screen.getByRole("status")).toHaveTextContent("failed to apply at step 3");
  });

  it("renders nothing extra for a draft/validated status (the review screen owns that view)", () => {
    renderBanner(base({ status: "draft" }));
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  describe("T-905: the awaiting_confirm pulse respects prefers-reduced-motion", () => {
    it("pulses by default (motion allowed)", () => {
      stubPrefersReducedMotion(false);
      const nowSec = Math.floor(Date.now() / 1000);
      renderBanner(base({ status: "awaiting_confirm", confirmDeadline: nowSec + 90 }));
      expect(screen.getByTestId("countdown-pulse-dot").className).toContain("animate-pulse");
    });

    it("falls back to a static dot when prefers-reduced-motion: reduce is set", () => {
      stubPrefersReducedMotion(true);
      const nowSec = Math.floor(Date.now() / 1000);
      renderBanner(base({ status: "awaiting_confirm", confirmDeadline: nowSec + 90 }));
      expect(screen.getByTestId("countdown-pulse-dot").className).not.toContain("animate-pulse");
    });
  });
});
