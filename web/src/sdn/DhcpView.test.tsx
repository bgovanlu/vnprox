// SPDX-License-Identifier: Apache-2.0

import { afterEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { DhcpView as DhcpViewData, SdnTree } from "../api/types";
import { DhcpView } from "./DhcpView";

const tree: SdnTree = {
  generatedAt: 1_752_000_000,
  zones: [{ id: "labz", type: "vlan", nodeStatus: [], vnets: [] }],
  fabrics: [],
  controllers: [],
  ipams: [],
  prefixLists: [],
  routeMaps: [],
};

const view: DhcpViewData = {
  generatedAt: 1_752_000_000,
  reservations: [
    {
      cidr: "10.50.0.0/24", zone: "labz", vnet: "vnet10",
      ip: "10.50.0.10", mac: "AA:BB:CC:DD:EE:01", hostname: "web1",
      guestRef: "guest:pve1:300",
    },
    {
      cidr: "10.50.0.0/24", zone: "labz", vnet: "vnet10",
      ip: "10.50.0.11", mac: "CC:CC:CC:CC:CC:CC", hostname: "ghost",
    },
  ],
  leases: [
    {
      cidr: "10.50.0.0/24", zone: "labz", vnet: "vnet10",
      ip: "10.50.0.150", mac: "aa:bb:cc:dd:ee:01", hostname: "dhcp-web1",
      guestRef: "guest:pve1:300",
    },
  ],
};

function urlOf(input: RequestInfo | URL): string {
  return typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
}

function renderView() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <DhcpView />
    </QueryClientProvider>,
  );
}

describe("DhcpView", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("renders reservations (with correlated guest) and leases (T-406 acceptance criteria 2/3)", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const url = urlOf(input);
        const body = url.includes("/sdn/dhcp") ? view : tree;
        return Promise.resolve(
          new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } }),
        );
      }),
    );

    renderView();

    await waitFor(() => {
      expect(screen.getByRole("table", { name: "DHCP reservations" })).toBeInTheDocument();
    });

    const reservationsTable = screen.getByRole("table", { name: "DHCP reservations" });
    expect(within(reservationsTable).getByText("10.50.0.10")).toBeInTheDocument();
    expect(within(reservationsTable).getByText("web1")).toBeInTheDocument();
    // Correlated reservation shows the resolved guestRef; the uncorrelated
    // "ghost" one shows "unmatched".
    expect(within(reservationsTable).getByText("guest:pve1:300")).toBeInTheDocument();
    expect(within(reservationsTable).getByText("unmatched")).toBeInTheDocument();

    const leasesTable = screen.getByRole("table", { name: "DHCP leases" });
    expect(within(leasesTable).getByText("10.50.0.150")).toBeInTheDocument();
    expect(within(leasesTable).getByText("dhcp-web1")).toBeInTheDocument();
    expect(within(leasesTable).getByText("guest:pve1:300")).toBeInTheDocument();

    // Zone filter dropdown is populated from the SDN tree.
    expect(screen.getByRole("option", { name: "labz" })).toBeInTheDocument();
  });

  it("re-fetches with ?zone= when the zone filter changes", async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = urlOf(input);
      const body = url.includes("/sdn/dhcp") ? view : tree;
      return Promise.resolve(
        new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } }),
      );
    });
    vi.stubGlobal("fetch", fetchMock);

    renderView();
    await waitFor(() => {
      expect(screen.getByRole("table", { name: "DHCP reservations" })).toBeInTheDocument();
    });

    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Filter by zone" }), "labz");

    await waitFor(() => {
      expect(fetchMock.mock.calls.some((c) => urlOf(c[0]).includes("/sdn/dhcp?zone=labz"))).toBe(true);
    });
  });

  it("shows empty states when there are no reservations/leases", async () => {
    const empty: DhcpViewData = { generatedAt: 1, reservations: [], leases: [] };
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const url = urlOf(input);
        const body = url.includes("/sdn/dhcp") ? empty : tree;
        return Promise.resolve(
          new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } }),
        );
      }),
    );

    renderView();
    await waitFor(() => {
      expect(screen.getByText("No reservations")).toBeInTheDocument();
    });
    expect(screen.getByText("No active leases")).toBeInTheDocument();
  });
});
