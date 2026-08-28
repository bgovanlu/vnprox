// SPDX-License-Identifier: Apache-2.0

// T-3907: composition + accessibility-relevant rendering coverage for the
// cabling plan view. Mirrors ports/PortsPage.test.tsx's own convention
// (mock the fetch at the API-module boundary, render through a real
// QueryClientProvider) rather than mocking the query hook itself, so the
// real useTopologyQuery -> buildSwitchModel -> buildCablingPlan pipeline is
// what's actually exercised.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { TopologyResponse } from "../api/types";
import { CablingPlanView } from "./CablingPlanView";

let mockResponse: TopologyResponse;
vi.mock("../api/topology", () => ({
  fetchTopology: vi.fn(() => Promise.resolve(mockResponse)),
}));

function renderView() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <CablingPlanView />
    </QueryClientProvider>,
  );
}

function baseResponse(overrides: Partial<TopologyResponse> = {}): TopologyResponse {
  return { nodes: [], edges: [], layers: [], generatedAt: 0, ...overrides };
}

describe("CablingPlanView", () => {
  it("groups NICs by node and marks a NIC with no LLDP neighbor 'Not discovered', never blank", async () => {
    mockResponse = baseResponse({
      nodes: [
        { id: "physnic:pve1:eno1", kind: "physnic", label: "eno1", layer: "phys", nodeGroup: "pve1", status: "ok", badges: [], speedMbps: 1000, mediaPort: "tp", duplex: "full" },
        {
          id: "lldp-neighbor:pve1:sw1",
          kind: "lldp-neighbor",
          label: "sw-core-01",
          layer: "phys",
          nodeGroup: "pve1",
          status: "ok",
          badges: ["port=Te1/0/1"],
        },
        { id: "physnic:pve1:eno2", kind: "physnic", label: "eno2", layer: "phys", nodeGroup: "pve1", status: "ok", badges: [] },
      ],
      edges: [{ from: "physnic:pve1:eno1", to: "lldp-neighbor:pve1:sw1", kind: "lldp-adjacent", status: "ok", badges: [] }],
    });
    renderView();

    // The accessible table is the source of truth — assert against it
    // specifically (scoped by its `region`), since the decorative SVG
    // diagram below it repeats the same labels for sighted users and would
    // otherwise make an unscoped query ambiguous.
    const section = await screen.findByRole("region", { name: "Cabling for pve1" });
    expect(within(section).getByText("eno1")).toBeInTheDocument();
    expect(within(section).getByText("eno2")).toBeInTheDocument();

    // Discovered NIC: far end printed, not blank.
    expect(within(section).getByText(/sw-core-01/)).toBeInTheDocument();
    expect(within(section).getByText(/Te1\/0\/1/)).toBeInTheDocument();

    // Undiscovered NIC: an explicit, non-blank marker — never absent, never
    // read as "not connected".
    expect(within(section).getByText("Not discovered")).toBeInTheDocument();

    // Speed/duplex/media surfaced for the NIC that reported them.
    expect(within(section).getByText("1 Gbps full")).toBeInTheDocument();
    expect(within(section).getByText("tp")).toBeInTheDocument();

    // The summary line names the unknown count rather than staying silent
    // about it.
    expect(screen.getByText(/1 with no LLDP neighbor discovered/)).toBeInTheDocument();
  });

  it("shows an empty state when the topology carries no physical NICs", async () => {
    mockResponse = baseResponse();
    renderView();
    expect(await screen.findByText("No physical NICs discovered yet")).toBeInTheDocument();
  });

  it("renders one section per node, each with its own table", async () => {
    mockResponse = baseResponse({
      nodes: [
        { id: "physnic:pve1:eno1", kind: "physnic", label: "eno1", layer: "phys", nodeGroup: "pve1", status: "ok", badges: [] },
        { id: "physnic:pve2:eno1", kind: "physnic", label: "eno1", layer: "phys", nodeGroup: "pve2", status: "ok", badges: [] },
      ],
      edges: [],
    });
    renderView();

    const pve1Section = await screen.findByRole("region", { name: "Cabling for pve1" });
    const pve2Section = screen.getByRole("region", { name: "Cabling for pve2" });
    expect(within(pve1Section).getByText("eno1")).toBeInTheDocument();
    expect(within(pve2Section).getByText("eno1")).toBeInTheDocument();
  });

  it("the print action is hidden from print output (only the table/diagram content prints)", async () => {
    mockResponse = baseResponse({
      nodes: [{ id: "physnic:pve1:eno1", kind: "physnic", label: "eno1", layer: "phys", nodeGroup: "pve1", status: "ok", badges: [] }],
      edges: [],
    });
    renderView();
    const printButton = await screen.findByRole("button", { name: "Print" });
    expect(printButton.className).toContain("print:hidden");
  });
});
