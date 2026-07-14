import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { PortsResponse } from "../api/types";
import { PortsPage } from "./PortsPage";

let mockResponse: PortsResponse;
vi.mock("../api/lldp", () => ({ fetchPorts: vi.fn(() => Promise.resolve(mockResponse)) }));

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <PortsPage />
    </QueryClientProvider>,
  );
}

describe("PortsPage", () => {
  it("renders a row per port with a stale flag and a CSV link", async () => {
    mockResponse = {
      items: [
        { node: "pve1", nic: "eno1", switch: "sw-core", port: "Gi0/1", speedMbps: 10000, pvid: 1, taggedVlans: [10, 20], lastSeen: Math.floor(Date.now() / 1000), stale: false },
        { node: "pve2", nic: "eno2", switch: "sw-core", port: "Gi0/2", stale: true },
      ],
    };
    renderPage();

    expect(await screen.findByText("eno1")).toBeInTheDocument();
    expect(screen.getAllByText("sw-core")).toHaveLength(2);
    expect(screen.getByText("Gi0/1")).toBeInTheDocument();
    expect(screen.getByText("10 Gbps")).toBeInTheDocument();
    expect(screen.getByText("10, 20")).toBeInTheDocument();
    expect(screen.getByText("stale")).toBeInTheDocument();

    const csv = screen.getByRole("link", { name: "Export CSV" });
    expect(csv).toHaveAttribute("href", "/api/v1/ports?format=csv");
  });

  it("shows an empty state when no ports are discovered", async () => {
    mockResponse = { items: [] };
    renderPage();
    expect(await screen.findByText("No ports discovered yet")).toBeInTheDocument();
  });
});
