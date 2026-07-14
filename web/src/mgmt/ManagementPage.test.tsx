// Issue #1: the dedicated Management page lists each node's management
// carrier, its resolved aspects, and the edit affordances.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Capabilities, EntityDetail, MeResponse, ProtectedInterfacesStatusResponse } from "../api/types";
import { ToastProvider } from "../components/Toast";
import { ManagementPage } from "./ManagementPage";

let mockStatus: ProtectedInterfacesStatusResponse;
let mockWrite = true;

const details: Record<string, EntityDetail> = {
  "bridge:pve1:vmbr0": {
    ref: "bridge:pve1:vmbr0",
    kind: "bridge",
    node: "pve1",
    label: "vmbr0",
    fields: { addresses: "10.0.0.11/24", gateway: "10.0.0.1", mtu: "1500", comments: "mgmt bridge" },
    provenance: {},
    related: [],
    generatedAt: 1,
  },
};

const ALL_CAPS: Capabilities = {
  netRead: true,
  netWrite: true,
  sdnRead: true,
  sdnWrite: true,
  fwRead: true,
  fwWrite: true,
  guestNet: true,
  audit: true,
};

vi.mock("../api/topology", () => ({
  fetchInventoryDetail: vi.fn((ref: string) => Promise.resolve(details[ref])),
  fetchTopology: vi.fn(),
  searchInventory: vi.fn(),
}));

vi.mock("../api/protectedInterfaces", () => ({
  fetchMgmtStatus: vi.fn(() => Promise.resolve(mockStatus)),
  fetchProtectedInterfaces: vi.fn(() => Promise.resolve({ nodes: {}, updatedAt: 0, version: 0 })),
  fetchProtectedInterfacesSuggest: vi.fn(() => Promise.resolve({ nodes: {} })),
  saveProtectedInterfaces: vi.fn(),
}));

vi.mock("../api/auth", () => ({
  getMe: vi.fn(
    (): Promise<MeResponse> =>
      Promise.resolve({
        user: { username: "root", realm: "pam" },
        caps: { "": mockWrite ? ALL_CAPS : { ...ALL_CAPS, netWrite: false } },
      }),
  ),
}));

vi.mock("../api/onboarding", () => ({
  fetchOnboardingProgress: vi.fn(() => Promise.reject(Object.assign(new Error("not found"), { status: 404 }))),
  saveOnboardingProgress: vi.fn(),
}));

function renderPage(): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <ToastProvider>
          <ManagementPage />
        </ToastProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("ManagementPage", () => {
  beforeEach(() => {
    mockWrite = true;
    mockStatus = {
      source: "detected",
      nodes: {
        pve1: [{ ref: "bridge:pve1:vmbr0", roles: ["mgmt", "corosync"], path: ["physnic:pve1:eno1"], redundant: false }],
      },
    };
  });

  it("lists the node's management carrier with its resolved aspects", async () => {
    renderPage();

    expect(await screen.findByRole("heading", { name: "Management interfaces" })).toBeInTheDocument();
    // Aspects resolved from the carrier's inventory detail.
    expect(await screen.findByText("10.0.0.11/24")).toBeInTheDocument();
    expect(screen.getByText("10.0.0.1")).toBeInTheDocument();
    expect(screen.getByText("Management IP")).toBeInTheDocument();
    expect(screen.getByText("Corosync link")).toBeInTheDocument();
    // Redundancy verdict + detected-source caveat.
    expect(screen.getByText(/is the only physical interface behind this carrier/)).toBeInTheDocument();
    expect(screen.getByText(/no one has confirmed it during onboarding/)).toBeInTheDocument();
  });

  it("offers the edit + redundancy affordances when the user can write", async () => {
    renderPage();
    // The Edit button enables once the carrier's inventory detail resolves
    // its kind (bridge → the bridge editor).
    await screen.findByText("10.0.0.11/24");
    expect(screen.getByRole("button", { name: "Edit interface" })).toBeEnabled();
    expect(screen.getByRole("button", { name: /redundan/i })).toBeInTheDocument();
  });

  it("disables the editor and hides the wizard when the user cannot write", async () => {
    mockWrite = false;
    renderPage();
    expect(await screen.findByRole("button", { name: "Edit interface" })).toBeDisabled();
    expect(screen.queryByRole("button", { name: /redundan/i })).not.toBeInTheDocument();
  });
});
