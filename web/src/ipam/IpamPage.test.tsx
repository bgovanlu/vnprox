// SPDX-License-Identifier: Apache-2.0

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import type { IpamSubnetsResponse } from "../api/types";
import { IpamPage } from "./IpamPage";

const subnets: IpamSubnetsResponse = {
  generatedAt: 1_752_000_000,
  items: [
    {
      cidr: "10.50.0.0/24",
      zone: "labz",
      vnet: "vnet10",
      gateway: "10.50.0.1",
      source: "sdn",
      total: 254,
      allocated: 4,
      observed: 3,
      conflicts: 2,
      utilization: 0.0157,
    },
    {
      cidr: "192.168.99.0/24",
      node: "pve1",
      source: "bridge",
      readOnly: true,
      total: 254,
      allocated: 0,
      observed: 0,
      conflicts: 0,
      utilization: 0,
    },
  ],
};

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <MemoryRouter>
      <QueryClientProvider client={client}>
        <IpamPage />
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

describe("IpamPage", () => {
  beforeEach(() => {
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const url = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
        if (url.includes("/allocations")) {
          return Promise.resolve(
            new Response(
              JSON.stringify({
                cidr: "10.50.0.0/24", prefix: 24, total: 254, conflicts: [], generatedAt: 1_752_000_000,
                entries: [{ ip: "10.50.0.1", state: "gateway", hostname: "labz-gw" }],
                freeRanges: [{ start: "10.50.0.2", end: "10.50.0.254", count: 253 }],
                counts: { allocated: 0, reserved: 0, observed: 0, gateway: 1, conflict: 0, free: 253 },
              }),
              { status: 200, headers: { "Content-Type": "application/json" } },
            ),
          );
        }
        return Promise.resolve(new Response(JSON.stringify(subnets), { status: 200, headers: { "Content-Type": "application/json" } }));
      }),
    );
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("lists both SDN and detected non-SDN subnets, with a conflict badge and read-only marker", async () => {
    renderPage();
    await waitFor(() => {
      expect(screen.getByText("10.50.0.0/24")).toBeInTheDocument();
    });
    expect(screen.getByText("192.168.99.0/24")).toBeInTheDocument();
    expect(screen.getByText("2 conflicts")).toBeInTheDocument();
    expect(screen.getByText("read-only")).toBeInTheDocument();
    expect(screen.getByText(/labz \/ vnet10/)).toBeInTheDocument();
  });

  it("default-selects the first subnet and renders its grid + a CSV export link", async () => {
    renderPage();
    await waitFor(() => {
      expect(screen.getByRole("heading", { name: "10.50.0.0/24" })).toBeInTheDocument();
    });
    const exportLink = screen.getByRole("link", { name: /Export CSV/ });
    expect(exportLink).toHaveAttribute("href", "/api/v1/ipam/subnets/10.50.0.0/24/allocations?format=csv");
  });
});
