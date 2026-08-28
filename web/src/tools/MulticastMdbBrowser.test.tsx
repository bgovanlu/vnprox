// SPDX-License-Identifier: Apache-2.0

// T-3902 Multicast/MDB browser: query-driven list-vs-search behavior, the
// per-bridge snooping summary, and — per the task card, since a real PVE
// 9.2.4 host's MDB table was empty for most bridges — the empty-table
// state, which is the case real users will hit most.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { MDBFilter } from "../api/mdb";
import type { MDBBridge, MDBEntry, MDBResponse } from "../api/types";
import { MulticastMdbBrowser } from "./MulticastMdbBrowser";

const mdnsEntry: MDBEntry = {
  node: "pve1",
  bridge: "vmbr0",
  group: "ff02::fb",
  port: "enp1s0",
  state: "temp",
  protocol: "kernel",
};
const secondEntry: MDBEntry = {
  node: "pve2",
  bridge: "vmbr1",
  group: "224.0.0.251",
  port: "eno2",
  vlan: 100,
  state: "temp",
};
const allEntries: MDBEntry[] = [mdnsEntry, secondEntry];
const searchEntries: MDBEntry[] = [mdnsEntry];

const bridgeSnooping: MDBBridge = { node: "pve1", bridge: "vmbr0", snooping: true, querier: false, routerMode: 1 };

const fetchMDB = vi.fn((filter: MDBFilter): Promise<MDBResponse> => {
  if (!filter.group) {
    return Promise.resolve({ entries: allEntries, bridges: [bridgeSnooping] });
  }
  if (filter.group === "239.255") {
    // A search term with deliberately no matches, distinct from the
    // "browse everything" blank-query empty case exercised separately.
    return Promise.resolve({ entries: [], bridges: [bridgeSnooping] });
  }
  return Promise.resolve({ entries: searchEntries, bridges: [bridgeSnooping] });
});

vi.mock("../api/mdb", () => ({
  fetchMDB: (filter: MDBFilter) => fetchMDB(filter),
}));

function renderBrowser(): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <MulticastMdbBrowser />
    </QueryClientProvider>,
  );
}

describe("MulticastMdbBrowser", () => {
  it("lists every cluster-wide entry with no query typed", async () => {
    renderBrowser();
    await waitFor(() => {
      expect(screen.getByText("ff02::fb")).toBeInTheDocument();
    });
    expect(screen.getByText("224.0.0.251")).toBeInTheDocument();
    expect(fetchMDB).toHaveBeenLastCalledWith({ group: "" });
  });

  it("switches to a narrowed group search once a query is typed", async () => {
    const user = userEvent.setup();
    renderBrowser();
    await waitFor(() => screen.getByText("224.0.0.251"));

    await user.type(screen.getByLabelText("Search multicast group"), "ff02");

    await waitFor(() => {
      expect(fetchMDB).toHaveBeenLastCalledWith({ group: "ff02" });
    });
    await waitFor(() => {
      expect(screen.queryByText("224.0.0.251")).not.toBeInTheDocument();
    });
    expect(screen.getByText("ff02::fb")).toBeInTheDocument();
  });

  it("shows node/bridge/port/vlan columns for each entry", async () => {
    renderBrowser();
    await waitFor(() => screen.getByText("224.0.0.251"));
    expect(screen.getByText("pve2")).toBeInTheDocument();
    expect(screen.getByText("vmbr1")).toBeInTheDocument();
    expect(screen.getByText("eno2")).toBeInTheDocument();
    expect(screen.getByText("100")).toBeInTheDocument();
  });

  it("renders the per-bridge snooping summary table", async () => {
    renderBrowser();
    await waitFor(() => screen.getByText("Bridge snooping state"));
    await waitFor(() => {
      expect(screen.getByText("enabled")).toBeInTheDocument();
    });
    expect(screen.getByText("Auto (learn)")).toBeInTheDocument();
  });

  it("shows a partial-fan-out banner naming unreachable nodes", async () => {
    fetchMDB.mockResolvedValueOnce({ entries: allEntries, bridges: [bridgeSnooping], partial: true, failedNodes: ["pve3"] });
    renderBrowser();
    await waitFor(() => {
      expect(screen.getByText(/Could not reach: pve3/)).toBeInTheDocument();
    });
  });

  // Per the task card: "expect the MDB table to be empty or near-empty on
  // a host with no multicast traffic — that is itself a finding worth
  // recording" — the real pvecube evidence transcript shows most
  // individual bridges (vmbr1, vmbr3, vmbr99) have zero MDB rows. This is
  // the case real users will hit most, so it gets its own sensible empty
  // state rather than a blank table.
  it("renders a sensible empty state when the cluster-wide MDB table is empty", async () => {
    fetchMDB.mockResolvedValueOnce({ entries: [], bridges: [] });
    renderBrowser();
    await waitFor(() => {
      expect(screen.getByText("No MDB entries")).toBeInTheDocument();
    });
    expect(
      screen.getByText(
        "Bridges report multicast group membership once IGMP/MLD-snooping has learned some — an empty table is the common state on a host with no active multicast traffic.",
      ),
    ).toBeInTheDocument();
    expect(screen.getByText("No bridge snooping state reported")).toBeInTheDocument();
    // No stray table rows/empty markup for either section.
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });

  it("renders a distinct empty state for a no-match search", async () => {
    const user = userEvent.setup();
    renderBrowser();
    await waitFor(() => screen.getByText("224.0.0.251"));

    await user.type(screen.getByLabelText("Search multicast group"), "239.255");

    await waitFor(() => {
      expect(screen.getByText("No matches")).toBeInTheDocument();
    });
    expect(screen.getByText('Nothing matched "239.255" on any reachable node\'s bridges.')).toBeInTheDocument();
  });
});
