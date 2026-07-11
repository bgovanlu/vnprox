// T-306 MAC/FDB browser: query-driven list-vs-search behavior and the
// guest/vnprox-known deep-link.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import type { FDBRow } from "../api/types";
import { MacFdbBrowser } from "./MacFdbBrowser";

const guestRow: FDBRow = {
  node: "pve1", bridge: "vmbr0", bridgeRef: "bridge:pve1:vmbr0", mac: "BC:24:11:AA:02:C8",
  port: "tap200i0", vlan: 100, owner: "guest", ownerRef: "guest:pve1:200", ownerLabel: "app01", stale: false,
};
const unknownRow: FDBRow = {
  node: "pve1", bridge: "vmbr0", bridgeRef: "bridge:pve1:vmbr0", mac: "DE:AD:BE:EF:00:01",
  port: "bond0", vlan: 1, owner: "unknown", stale: true,
};
const allRows: FDBRow[] = [guestRow, unknownRow];
const searchRows: FDBRow[] = [guestRow];

const fetchFDB = vi.fn((mac: string) => Promise.resolve(mac.trim() ? searchRows : allRows));

vi.mock("../api/fdb", () => ({
  fetchFDB: (mac: string) => fetchFDB(mac),
}));

const select = vi.fn();
vi.mock("../topology/store", () => ({
  useTopologyStore: (selector: (s: { select: typeof select }) => unknown) => selector({ select }),
}));

function renderBrowser(): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <MacFdbBrowser />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("MacFdbBrowser", () => {
  it("lists every cluster-wide entry with no query typed", async () => {
    renderBrowser();
    await waitFor(() => {
      expect(screen.getByText("BC:24:11:AA:02:C8")).toBeInTheDocument();
    });
    expect(screen.getByText("DE:AD:BE:EF:00:01")).toBeInTheDocument();
    expect(fetchFDB).toHaveBeenLastCalledWith("");
  });

  it("switches to ranked search once a query is typed", async () => {
    const user = userEvent.setup();
    renderBrowser();
    await waitFor(() => screen.getByText("DE:AD:BE:EF:00:01"));

    await user.type(screen.getByLabelText("Search MAC address"), "BC24");

    await waitFor(() => {
      expect(fetchFDB).toHaveBeenLastCalledWith("BC24");
    });
    await waitFor(() => {
      expect(screen.queryByText("DE:AD:BE:EF:00:01")).not.toBeInTheDocument();
    });
    expect(screen.getByText("BC:24:11:AA:02:C8")).toBeInTheDocument();
  });

  it("shows a guest owner badge with a deep-link that selects the guest ref", async () => {
    const user = userEvent.setup();
    renderBrowser();
    await waitFor(() => screen.getByText("app01"));

    await user.click(screen.getByText("app01"));
    expect(select).toHaveBeenCalledWith("guest:pve1:200");
  });

  it("shows an unlinked Unknown badge for entries with no owner ref", async () => {
    renderBrowser();
    await waitFor(() => screen.getByText("Unknown"));
    expect(screen.getByText("Unknown").closest("button")).toBeNull();
  });
});
