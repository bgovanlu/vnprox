import { afterEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ToastProvider } from "../components/Toast";
import type { IpamAllocationGrid } from "../api/types";
import { AllocationGrid } from "./AllocationGrid";

const smallGrid: IpamAllocationGrid = {
  cidr: "10.50.0.0/24",
  prefix: 24,
  total: 254,
  paged: false,
  conflicts: [
    { type: "duplicate_ip", severity: "error", ips: ["10.50.0.77"], message: "dup", suggestion: "fix it" },
  ],
  generatedAt: 1_752_000_000,
  cells: [
    { ip: "10.50.0.1", state: "gateway", confidence: "allocated" },
    { ip: "10.50.0.10", state: "allocated", confidence: "both", hostname: "web1", sources: ["pve-ipam", "guest-agent"] },
    { ip: "10.50.0.20", state: "allocated", confidence: "allocated", hostname: "web2", sources: ["pve-ipam"] },
    { ip: "10.50.0.77", state: "conflict", confidence: "conflict", sources: ["guest-agent"] },
    { ip: "10.50.0.88", state: "observed", confidence: "observed", hostname: "web3", sources: ["guest-agent"] },
    { ip: "10.50.0.99", state: "free" },
  ],
};

const pagedGrid: IpamAllocationGrid = {
  cidr: "10.60.0.0/16",
  prefix: 16,
  total: 65536,
  paged: true,
  conflicts: [],
  generatedAt: 1_752_000_000,
  blocks: [
    { cidr: "10.60.0.0/24", total: 254, allocated: 10, observed: 2, conflicts: 0, utilization: 0.04 },
    { cidr: "10.60.1.0/24", total: 254, allocated: 0, observed: 0, conflicts: 1, utilization: 0 },
  ],
};

function renderGrid(cidr: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <ToastProvider>
        <AllocationGrid subnetCidr={cidr} />
      </ToastProvider>
    </QueryClientProvider>,
  );
}

describe("AllocationGrid", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("acceptance criterion 1: renders a /24 grid with correct per-cell states, one of each confidence label", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() =>
        Promise.resolve(new Response(JSON.stringify(smallGrid), { status: 200, headers: { "Content-Type": "application/json" } })),
      ),
    );
    renderGrid("10.50.0.0/24");

    await waitFor(() => {
      expect(screen.getByRole("grid", { name: /Allocation grid for 10.50.0.0\/24/ })).toBeInTheDocument();
    });

    // Every cell renders with an accessible label naming its IP and state.
    expect(screen.getByLabelText("10.50.0.1: Gateway")).toBeInTheDocument();
    expect(screen.getByLabelText("10.50.0.10: Allocated")).toBeInTheDocument();
    expect(screen.getByLabelText("10.50.0.20: Allocated")).toBeInTheDocument();
    expect(screen.getByLabelText("10.50.0.77: Conflict")).toBeInTheDocument();
    expect(screen.getByLabelText("10.50.0.88: Observed (unallocated)")).toBeInTheDocument();
    expect(screen.getByLabelText("10.50.0.99: Free")).toBeInTheDocument();
  });

  it("clicking a cell opens its detail dialog with hostname/mac/source", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() =>
        Promise.resolve(new Response(JSON.stringify(smallGrid), { status: 200, headers: { "Content-Type": "application/json" } })),
      ),
    );
    renderGrid("10.50.0.0/24");
    await waitFor(() => {
      expect(screen.getByLabelText("10.50.0.10: Allocated")).toBeInTheDocument();
    });
    await userEvent.click(screen.getByLabelText("10.50.0.10: Allocated"));
    await waitFor(() => {
      expect(screen.getByRole("heading", { name: "10.50.0.10" })).toBeInTheDocument();
    });
    expect(screen.getByText("web1")).toBeInTheDocument();
    // allocated + observed ("both") is shown as one of the four confidence
    // labels in the dialog description.
    expect(screen.getByText(/allocated \+ observed/)).toBeInTheDocument();
  });

  it("a subnet bigger than 256 addresses renders paged block summaries, not a full grid", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() =>
        Promise.resolve(new Response(JSON.stringify(pagedGrid), { status: 200, headers: { "Content-Type": "application/json" } })),
      ),
    );
    renderGrid("10.60.0.0/16");
    await waitFor(() => {
      expect(screen.getByText("10.60.0.0/24")).toBeInTheDocument();
    });
    expect(screen.getByText("10.60.1.0/24")).toBeInTheDocument();
    expect(screen.getByText("1 conflict(s)")).toBeInTheDocument();
    expect(screen.queryByRole("grid")).not.toBeInTheDocument();
  });
});
