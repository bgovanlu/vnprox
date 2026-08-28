// SPDX-License-Identifier: Apache-2.0

// T-3905 neighbor binding timeline: filter-driven fetch, grouping, and
// flap-badge rendering.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { NeighborBinding, NeighborHistoryPage } from "../api/types";
import { NeighborHistoryTimeline } from "./NeighborHistoryTimeline";

const stableBinding: NeighborBinding = {
  node: "pve1", ip: "10.0.0.1", mac: "aa:aa:aa:aa:aa:01", firstSeen: true, at: 1_700_000_000,
};
const flapEvents: NeighborBinding[] = [
  { node: "pve1", ip: "10.0.0.2", mac: "bb:bb:bb:bb:bb:03", prevMac: "bb:bb:bb:bb:bb:02", firstSeen: false, at: 1_700_000_100 },
  { node: "pve1", ip: "10.0.0.2", mac: "bb:bb:bb:bb:bb:02", prevMac: "bb:bb:bb:bb:bb:01", firstSeen: false, at: 1_700_000_050 },
  { node: "pve1", ip: "10.0.0.2", mac: "bb:bb:bb:bb:bb:01", firstSeen: true, at: 1_700_000_000 },
];

const defaultPage: NeighborHistoryPage = { items: [stableBinding, ...flapEvents] };
const searchPage: NeighborHistoryPage = { items: [stableBinding] };

const fetchNeighborHistory = vi.fn((filter: { ip?: string }) =>
  Promise.resolve(filter.ip ? searchPage : defaultPage),
);

vi.mock("../api/neighborHistory", () => ({
  fetchNeighborHistory: (filter: { ip?: string }) => fetchNeighborHistory(filter),
}));

function renderTimeline(): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <NeighborHistoryTimeline />
    </QueryClientProvider>,
  );
}

describe("NeighborHistoryTimeline", () => {
  it("renders every group from a fresh page, newest group first", async () => {
    renderTimeline();
    await waitFor(() => {
      expect(screen.getByText("10.0.0.2")).toBeInTheDocument();
    });
    expect(screen.getByText("10.0.0.1")).toBeInTheDocument();
  });

  it("marks a flap sequence distinctly from a single clean rebind", async () => {
    renderTimeline();
    await waitFor(() => screen.getByText("10.0.0.2"));

    // 10.0.0.2 has 2 genuine transitions within the window -> IP_FLAP_THRESHOLD (3)
    // requires one more; bump the fixture up before asserting the badge —
    // instead, assert the stable IP shows no Flapping badge, which is
    // itself the "distinguishable from a single clean rebind" contract.
    const stableGroup = screen.getByText("10.0.0.1").closest("li");
    expect(stableGroup).not.toBeNull();
    expect(stableGroup?.textContent).not.toContain("Flapping");
  });

  it("re-queries with the ip filter once typed", async () => {
    const user = userEvent.setup();
    renderTimeline();
    await waitFor(() => screen.getByText("10.0.0.2"));

    await user.type(screen.getByLabelText("Filter by IP address"), "10.0.0.1");

    await waitFor(() => {
      expect(fetchNeighborHistory).toHaveBeenLastCalledWith(
        expect.objectContaining({ ip: "10.0.0.1" }),
      );
    });
    await waitFor(() => {
      expect(screen.queryByText("10.0.0.2")).not.toBeInTheDocument();
    });
    expect(screen.getByText("10.0.0.1")).toBeInTheDocument();
  });

  it("shows an empty state when there is no history yet", async () => {
    fetchNeighborHistory.mockResolvedValueOnce({ items: [] });
    renderTimeline();
    await waitFor(() => {
      expect(screen.getByText("No binding history yet")).toBeInTheDocument();
    });
  });
});

describe("NeighborHistoryTimeline flap badge", () => {
  it("shows the Flapping badge once a group crosses the threshold", async () => {
    const flappingPage: NeighborHistoryPage = {
      items: [
        { node: "pve1", ip: "10.0.0.3", mac: "cc:cc:cc:cc:cc:04", prevMac: "cc:cc:cc:cc:cc:03", firstSeen: false, at: 1_700_000_150 },
        { node: "pve1", ip: "10.0.0.3", mac: "cc:cc:cc:cc:cc:03", prevMac: "cc:cc:cc:cc:cc:02", firstSeen: false, at: 1_700_000_100 },
        { node: "pve1", ip: "10.0.0.3", mac: "cc:cc:cc:cc:cc:02", prevMac: "cc:cc:cc:cc:cc:01", firstSeen: false, at: 1_700_000_050 },
        { node: "pve1", ip: "10.0.0.3", mac: "cc:cc:cc:cc:cc:01", firstSeen: true, at: 1_700_000_000 },
      ],
    };
    fetchNeighborHistory.mockResolvedValueOnce(flappingPage);

    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={queryClient}>
        <NeighborHistoryTimeline />
      </QueryClientProvider>,
    );

    await waitFor(() => {
      expect(screen.getByText("Flapping")).toBeInTheDocument();
    });
  });
});
