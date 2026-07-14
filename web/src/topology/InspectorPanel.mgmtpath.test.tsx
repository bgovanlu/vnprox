// T-702 acceptance criterion 4: the inspector's "Management path" tab
// renders carrier/path/redundancy-statement/source-caveat, and the
// not-redundant plain-English wording specifically.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import type { EntityDetail, ProtectedInterfacesStatusResponse } from "../api/types";
import { ToastProvider } from "../components/Toast";
import { InspectorPanel } from "./InspectorPanel";

let mockDetailResponse: EntityDetail;
let mockStatusResponse: ProtectedInterfacesStatusResponse;

vi.mock("../api/topology", () => ({
  fetchInventoryDetail: vi.fn(() => Promise.resolve(mockDetailResponse)),
  fetchTopology: vi.fn(),
  searchInventory: vi.fn(),
}));

vi.mock("../api/protectedInterfaces", () => ({
  fetchMgmtStatus: vi.fn(() => Promise.resolve(mockStatusResponse)),
  fetchProtectedInterfaces: vi.fn(() => Promise.resolve({ nodes: {}, updatedAt: 0, version: 0 })),
  fetchProtectedInterfacesSuggest: vi.fn(() => Promise.resolve({ nodes: {} })),
  saveProtectedInterfaces: vi.fn(),
}));

vi.mock("../api/onboarding", () => ({
  fetchOnboardingProgress: vi.fn(() =>
    Promise.reject(Object.assign(new Error("not found"), { status: 404 })),
  ),
  saveOnboardingProgress: vi.fn(),
}));

function bridgeDetail(ref: string, node: string): EntityDetail {
  return {
    ref,
    kind: "bridge",
    node,
    label: "vmbr0",
    fields: {},
    provenance: {},
    related: [],
    generatedAt: 1,
  };
}

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

describe("InspectorPanel Management path tab", () => {
  it("shows carrier/path/not-redundant wording and the detected-source caveat", async () => {
    mockDetailResponse = bridgeDetail("bridge:pve1:vmbr0", "pve1");
    mockStatusResponse = {
      source: "detected",
      nodes: {
        pve1: [
          {
            ref: "bridge:pve1:vmbr0",
            roles: ["mgmt"],
            path: ["physnic:pve1:eno1"],
            redundant: false,
          },
        ],
      },
    };

    const user = userEvent.setup();
    renderPanel("bridge:pve1:vmbr0");

    expect(await screen.findByText("vmbr0")).toBeInTheDocument();
    await user.click(screen.getByRole("tab", { name: "Management path" }));

    expect(await screen.findByText("bridge:pve1:vmbr0", { exact: false })).toBeInTheDocument();
    expect(screen.getByText(/physnic:pve1:eno1 is the only physical interface/)).toBeInTheDocument();
    expect(screen.getByText(/no one has confirmed it during/)).toBeInTheDocument();
  });

  it("shows the redundant wording and no caveat when confirmed", async () => {
    mockDetailResponse = bridgeDetail("bridge:pve1:vmbr0", "pve1");
    mockStatusResponse = {
      source: "confirmed",
      nodes: {
        pve1: [
          {
            ref: "bridge:pve1:vmbr0",
            roles: ["mgmt", "corosync"],
            path: ["bond:pve1:bond0", "physnic:pve1:eno1", "physnic:pve1:eno2"],
            redundant: true,
          },
        ],
      },
    };

    const user = userEvent.setup();
    renderPanel("bridge:pve1:vmbr0");

    expect(await screen.findByText("vmbr0")).toBeInTheDocument();
    await user.click(screen.getByRole("tab", { name: "Management path" }));

    expect(await screen.findByText(/Redundant: this path has 3 physical interfaces/)).toBeInTheDocument();
    expect(screen.queryByText(/no one has confirmed it during/)).not.toBeInTheDocument();
  });
});
