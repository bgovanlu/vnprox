// T-804 acceptance criterion 4: the bond inspector's live LACP section
// rendering both states (matched/negotiated, and mismatched) from fixture
// data. __fixtures__/inventory-detail-bond0-lacp-ok.json and
// -mismatch.json are hand-built GET /inventory/{ref} response shapes
// matching internal/topology.Detail's real marshal-through-JSON contract
// (SlaveDetail is internal/inventory.BondSlaveState's own capitalized Go
// field names — see InspectorPanel.test.tsx's captured-fixture precedent).
import detailOkFixture from "./__fixtures__/inventory-detail-bond0-lacp-ok.json";
import detailMismatchFixture from "./__fixtures__/inventory-detail-bond0-lacp-mismatch.json";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import type { EntityDetail } from "../api/types";
import { ToastProvider } from "../components/Toast";
import { BondLacpSection } from "./BondLacpSection";
import { InspectorPanel } from "./InspectorPanel";

const okDetail = detailOkFixture as unknown as EntityDetail;
const mismatchDetail = detailMismatchFixture as unknown as EntityDetail;

vi.mock("../api/topology", () => ({
  fetchInventoryDetail: vi.fn(() => Promise.resolve(mockDetailResponse)),
  fetchTopology: vi.fn(),
  searchInventory: vi.fn(),
}));

let mockDetailResponse: EntityDetail = okDetail;

function renderPanel(ref: string): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <ToastProvider>
          <InspectorPanel selectedRef={ref} onClose={() => undefined} onSelectRelated={() => undefined} />
        </ToastProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("BondLacpSection (pure component)", () => {
  it("renders a plain no-detail message when SlaveDetail carries no LACP data at all", () => {
    render(
      <BondLacpSection
        fields={{
          Mode: "active-backup",
          SlaveDetail: [{ Name: "eno1", MIIStatus: "up", Active: true, LACPDetailSet: false }],
        }}
      />,
    );
    expect(screen.getByText(/No 802\.3ad LACP negotiation detail available/)).toBeInTheDocument();
    expect(screen.getByText(/active-backup/)).toBeInTheDocument();
  });

  it("renders a negotiated-correctly callout for a fully matched bond", () => {
    render(<BondLacpSection fields={okDetail.fields} />);
    expect(screen.getByRole("status")).toHaveTextContent("LACP negotiated correctly on every slave.");
    expect(screen.getByText("eno1")).toBeInTheDocument();
    expect(screen.getByText("eno2")).toBeInTheDocument();
    expect(screen.getAllByText("negotiated")).toHaveLength(2);
  });

  it("renders a split-brain callout for a mismatched bond, distinct per-slave state", () => {
    render(<BondLacpSection fields={mismatchDetail.fields} />);
    expect(screen.getByRole("status")).toHaveTextContent(/split-brain/i);
    expect(screen.getByText("negotiated")).toBeInTheDocument();
    expect(screen.getByText("not negotiated")).toBeInTheDocument();
  });
});

describe("InspectorPanel LACP tab", () => {
  it("shows a LACP tab for a bond entity and renders the negotiated-correctly state", async () => {
    mockDetailResponse = okDetail;
    renderPanel(okDetail.ref);

    await userEvent.click(await screen.findByRole("tab", { name: "LACP" }));

    const panel = await screen.findByText("LACP negotiated correctly on every slave.");
    expect(panel).toBeInTheDocument();
  });

  it("renders the mismatched state distinctly (split-brain callout + per-slave not-negotiated badge)", async () => {
    mockDetailResponse = mismatchDetail;
    renderPanel(mismatchDetail.ref);

    await userEvent.click(await screen.findByRole("tab", { name: "LACP" }));

    const status = await screen.findByRole("status");
    expect(within(status).getByText(/split-brain/i)).toBeInTheDocument();
    expect(await screen.findByText("not negotiated")).toBeInTheDocument();
  });

  it("does not show a LACP tab for a non-bond entity", async () => {
    mockDetailResponse = { ...okDetail, ref: "bridge:pve1:vmbr0", kind: "bridge", label: "vmbr0" };
    renderPanel("bridge:pve1:vmbr0");

    // Wait for the panel to finish loading (the "Fields" tab, present for
    // every entity kind) before asserting the LACP tab's absence.
    await screen.findByRole("tab", { name: "Fields" });
    expect(screen.queryByRole("tab", { name: "LACP" })).not.toBeInTheDocument();
  });
});
