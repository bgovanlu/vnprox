// SPDX-License-Identifier: Apache-2.0

// T-1006 acceptance criterion 4 (Vitest half): the analytics tab renders
// hit counts/top-blocked/unused list from a mocked GET /firewall/analytics
// response, and the "edit rule" link points at the correct deep link
// (guestRef+pos+origin — the cross-page navigation itself is proven by
// web/e2e/fwlog-analytics.spec.ts, since that's genuine cross-page
// interaction proof no component test can substitute for).
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import type { FwAnalyticsResponse } from "../api/types";
import { AnalyticsTab } from "./AnalyticsTab";

let queryResult: { data: FwAnalyticsResponse | undefined; isLoading: boolean; error: Error | null } = {
  data: undefined,
  isLoading: false,
  error: null,
};

vi.mock("./queries", () => ({
  useFwAnalyticsQuery: () => queryResult,
}));

function renderTab(): void {
  render(
    <MemoryRouter>
      <AnalyticsTab />
    </MemoryRouter>,
  );
}

function response(overrides: Partial<FwAnalyticsResponse> = {}): FwAnalyticsResponse {
  return {
    hitCounts: [],
    topBlocked: { sources: [], destinations: [] },
    unusedRules: [],
    ...overrides,
  };
}

describe("AnalyticsTab", () => {
  it("renders the hit-count table with an edit-rule deep link", async () => {
    queryResult = {
      data: response({
        hitCounts: [
          { rule: { guestRef: "guest:pve1:200", origin: "guest", pos: 2 }, hits: 7, lastSeenAt: 1_752_753_600 },
        ],
      }),
      isLoading: false,
      error: null,
    };
    renderTab();

    await waitFor(() => {
      expect(screen.getByText("7")).toBeInTheDocument();
    });
    expect(screen.getByText("guest:pve1:200")).toBeInTheDocument();

    const link = screen.getByRole("link", { name: /Guest/ });
    const href = link.getAttribute("href") ?? "";
    expect(href).toContain("scope=guest");
    expect(href).toContain("ref=guest%3Apve1%3A200");
    expect(href).toContain("pos=2");
    expect(href).toContain("origin=guest");
  });

  it("renders top-blocked source/destination charts", async () => {
    queryResult = {
      data: response({
        topBlocked: {
          sources: [{ value: "1.1.1.1", count: 5 }],
          destinations: [{ value: "9.9.9.9", count: 3 }],
        },
      }),
      isLoading: false,
      error: null,
    };
    renderTab();

    await waitFor(() => {
      expect(screen.getByTestId("analytics-chart-top-blocked-sources")).toBeInTheDocument();
    });
    expect(screen.getByTestId("analytics-chart-top-blocked-destinations")).toBeInTheDocument();
  });

  it("renders the unused-rule list with an honest 'never observed' label", async () => {
    queryResult = {
      data: response({
        unusedRules: [
          { rule: { guestRef: "guest:pve1:200", origin: "guest", pos: 1 }, daysSinceLastHit: 40 },
          { rule: { guestRef: "guest:pve1:200", origin: "cluster", pos: 0 }, daysSinceLastHit: -1 },
        ],
      }),
      isLoading: false,
      error: null,
    };
    renderTab();

    await waitFor(() => {
      expect(screen.getByText("40 days ago")).toBeInTheDocument();
    });
    expect(screen.getByText("no observed hit in the retained log")).toBeInTheDocument();
    expect(screen.queryByText("-1 days ago")).not.toBeInTheDocument();
  });

  it("shows empty states when every facet is empty", async () => {
    queryResult = { data: response(), isLoading: false, error: null };
    renderTab();

    await waitFor(() => {
      expect(screen.getByText("No rule hits in this window")).toBeInTheDocument();
    });
    expect(screen.getByText("No unused rules")).toBeInTheDocument();
  });

  it("shows a loading state and an error state", () => {
    queryResult = { data: undefined, isLoading: true, error: null };
    renderTab();
    expect(screen.getByText("Loading analytics…")).toBeInTheDocument();

    queryResult = { data: undefined, isLoading: false, error: new Error("boom") };
    renderTab();
    expect(screen.getByText("Could not load firewall analytics")).toBeInTheDocument();
  });
});
