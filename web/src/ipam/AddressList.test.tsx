// SPDX-License-Identifier: Apache-2.0

import { afterEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ToastProvider } from "../components/Toast";
import type { IpamAllocationList } from "../api/types";
import { AddressList } from "./AddressList";

// The reserve dialog embeds MacPicker, which reads the guest-NIC list; mock
// it (as CellDetailDialog.test does) so opening the dialog doesn't pull the
// full /topology response this test doesn't stub.
vi.mock("../guests/queries", () => ({
  useAllGuestNicsQuery: () => ({ rows: [], isLoading: false }),
}));

const list: IpamAllocationList = {
  cidr: "10.50.0.0/24",
  gateway: "10.50.0.1",
  prefix: 24,
  total: 254,
  generatedAt: 1_752_000_000,
  counts: { allocated: 2, reserved: 0, observed: 1, gateway: 1, conflict: 1, free: 249 },
  conflicts: [
    { type: "duplicate_ip", severity: "error", ips: ["10.50.0.77"], message: "multiple guests report 10.50.0.77", suggestion: "release all but one" },
  ],
  entries: [
    { ip: "10.50.0.1", state: "gateway", confidence: "allocated" },
    { ip: "10.50.0.10", state: "allocated", confidence: "both", hostname: "web1", vmid: 101, sources: ["pve-ipam", "guest-agent"] },
    { ip: "10.50.0.20", state: "allocated", confidence: "allocated", hostname: "web2", vmid: 102, sources: ["pve-ipam"] },
    { ip: "10.50.0.77", state: "conflict", confidence: "conflict", sources: ["guest-agent"] },
    { ip: "10.50.0.88", state: "observed", confidence: "observed", hostname: "squatter", sources: ["guest-agent"] },
  ],
  freeRanges: [
    { start: "10.50.0.2", end: "10.50.0.9", count: 8 },
    { start: "10.50.0.11", end: "10.50.0.19", count: 9 },
    { start: "10.50.0.89", end: "10.50.0.254", count: 166 },
  ],
};

function stubList(body: IpamAllocationList): void {
  vi.stubGlobal(
    "fetch",
    vi.fn(() => Promise.resolve(new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } }))),
  );
}

function renderList(readOnly = false) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <ToastProvider>
        <AddressList subnetCidr="10.50.0.0/24" readOnly={readOnly} />
      </ToastProvider>
    </QueryClientProvider>,
  );
}

describe("AddressList", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("renders occupied addresses as rows and free space as collapsed range rows", async () => {
    stubList(list);
    renderList();

    await waitFor(() => {
      expect(screen.getByLabelText("10.50.0.10: Allocated")).toBeInTheDocument();
    });
    // Occupied entries, each with its state.
    expect(screen.getByLabelText("10.50.0.1: Gateway")).toBeInTheDocument();
    expect(screen.getByLabelText("10.50.0.77: Conflict")).toBeInTheDocument();
    expect(screen.getByLabelText("10.50.0.88: Observed (unallocated)")).toBeInTheDocument();
    expect(screen.getByText("web1")).toBeInTheDocument();

    // Free space is collapsed into range rows, not 249 individual cells.
    const firstRange = screen.getByText("10.50.0.2 – 10.50.0.9").closest("div");
    expect(firstRange).not.toBeNull();
    expect(firstRange?.textContent).toContain("8");
    expect(firstRange?.textContent).toContain("free");
    expect(screen.getByText("10.50.0.89 – 10.50.0.254")).toBeInTheDocument();
  });

  it("surfaces conflicts with their suggested resolution above the list", async () => {
    stubList(list);
    renderList();
    await waitFor(() => {
      expect(screen.getByText("multiple guests report 10.50.0.77")).toBeInTheDocument();
    });
    expect(screen.getByText(/Suggested: release all but one/)).toBeInTheDocument();
  });

  it("filters the list to a single state via the filter chips", async () => {
    stubList(list);
    renderList();
    await waitFor(() => {
      expect(screen.getByLabelText("10.50.0.10: Allocated")).toBeInTheDocument();
    });

    await userEvent.click(screen.getByRole("button", { name: "Observed" }));

    // Only the observed entry survives; allocated rows and free ranges are gone.
    expect(screen.getByLabelText("10.50.0.88: Observed (unallocated)")).toBeInTheDocument();
    expect(screen.queryByLabelText("10.50.0.10: Allocated")).not.toBeInTheDocument();
    expect(screen.queryByText("10.50.0.2 – 10.50.0.9")).not.toBeInTheDocument();
  });

  it("searches by hostname", async () => {
    stubList(list);
    renderList();
    await waitFor(() => {
      expect(screen.getByLabelText("10.50.0.10: Allocated")).toBeInTheDocument();
    });

    await userEvent.type(screen.getByRole("searchbox", { name: "Filter addresses" }), "web2");
    expect(screen.getByLabelText("10.50.0.20: Allocated")).toBeInTheDocument();
    expect(screen.queryByLabelText("10.50.0.10: Allocated")).not.toBeInTheDocument();
  });

  it("clicking an occupied address opens its detail dialog", async () => {
    stubList(list);
    renderList();
    await waitFor(() => {
      expect(screen.getByLabelText("10.50.0.10: Allocated")).toBeInTheDocument();
    });
    await userEvent.click(screen.getByLabelText("10.50.0.10: Allocated"));
    await waitFor(() => {
      expect(screen.getByRole("heading", { name: "10.50.0.10" })).toBeInTheDocument();
    });
  });

  it("'Reserve first free' on a range opens the reserve dialog for that range's start", async () => {
    stubList(list);
    renderList();
    await waitFor(() => {
      expect(screen.getByText("10.50.0.2 – 10.50.0.9")).toBeInTheDocument();
    });
    const firstRange = screen.getByText("10.50.0.2 – 10.50.0.9").closest("div");
    expect(firstRange).not.toBeNull();
    await userEvent.click(within(firstRange as HTMLElement).getByRole("button", { name: /Reserve first free/ }));
    await waitFor(() => {
      expect(screen.getByRole("heading", { name: "10.50.0.2" })).toBeInTheDocument();
    });
  });

  it("hides reserve affordances for a read-only subnet", async () => {
    stubList(list);
    renderList(true);
    await waitFor(() => {
      expect(screen.getByText("10.50.0.2 – 10.50.0.9")).toBeInTheDocument();
    });
    expect(screen.queryByRole("button", { name: /Reserve first free/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Reserve next free/ })).not.toBeInTheDocument();
  });
});
