// T-605's read-only-mode UX sweep found the bulk "Reattach N guest(s)"
// button was gated only on `!bulkTarget` (a target picked) with no
// capability check at all — a read-only session could select rows and
// draft a bulk guest.nic.update op regardless of guestNet/netWrite. This
// test pins the fix: the bulk button is disabled-with-tooltip whenever any
// currently-selected row's node lacks write, and enabled when every
// selected row's node grants it — mirroring the per-row Disconnect/Connect
// button's own existing capsForNode gating just above it in the table.
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { GuestNicRow } from "./guestNics";
import type { MeResponse, TopologyResponse } from "../api/types";
import { ToastProvider } from "../components/Toast";
import { GuestsPage } from "./GuestsPage";

const rows: GuestNicRow[] = [
  { ref: "guest-nic:pve1:100:net0", label: "app01/net0", node: "pve1", bridgeOrVnet: "vmbr0", linkDown: false },
  { ref: "guest-nic:pve2:101:net0", label: "cache01/net0", node: "pve2", bridgeOrVnet: "vmbr0", linkDown: false },
];

vi.mock("./queries", () => ({
  useAllGuestNicsQuery: () => ({ rows, isLoading: false }),
}));

vi.mock("../api/useSession", () => ({
  useSession: () => ({ data: mockSession }),
}));

const topology: TopologyResponse = {
  nodes: [{ id: "bridge:pve1:vmbr1", kind: "bridge", label: "vmbr1", layer: "l2", nodeGroup: "pve1", status: "ok", badges: [] }],
  edges: [],
  layers: ["phys", "l2", "sdn", "guest"],
  generatedAt: 0,
};

vi.mock("../topology/queries", () => ({
  useTopologyQuery: () => ({ data: topology, isLoading: false }),
}));

let mockSession: MeResponse | undefined;

const fullCaps = { netRead: true, netWrite: true, sdnRead: true, sdnWrite: true, fwRead: true, fwWrite: true, guestNet: true, audit: true, capture: false };
const readOnlyCaps = { netRead: true, netWrite: false, sdnRead: false, sdnWrite: false, fwRead: false, fwWrite: false, guestNet: false, audit: true, capture: false };

function renderPage(): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <GuestsPage />
      </ToastProvider>
    </QueryClientProvider>,
  );
}

describe("GuestsPage bulk reattach read-only gating", () => {
  it("disables the bulk Reattach button when the selected guest's node lacks write", async () => {
    mockSession = { user: { username: "auditor", realm: "pve" }, caps: { pve1: readOnlyCaps, pve2: readOnlyCaps } };
    const user = userEvent.setup();
    renderPage();

    await user.click(screen.getByRole("checkbox", { name: "Select app01/net0" }));
    await user.selectOptions(screen.getByRole("combobox", { name: "Reattach selected guests to" }), "vmbr1");

    expect(screen.getByRole("button", { name: /Reattach 1 guest/ })).toBeDisabled();
  });

  it("disables the bulk button when only SOME selected guests' nodes grant write", async () => {
    // pve1 (app01) is writable, pve2 (cache01) is not -> selecting both must
    // still disable the bulk action (the "every selected row" requirement,
    // not "any").
    mockSession = { user: { username: "netops", realm: "pve" }, caps: { pve1: fullCaps, pve2: readOnlyCaps } };
    const user = userEvent.setup();
    renderPage();

    await user.click(screen.getByRole("checkbox", { name: "Select app01/net0" }));
    await user.click(screen.getByRole("checkbox", { name: "Select cache01/net0" }));
    await user.selectOptions(screen.getByRole("combobox", { name: "Reattach selected guests to" }), "vmbr1");

    expect(screen.getByRole("button", { name: /Reattach 2 guest/ })).toBeDisabled();
  });

  it("enables the bulk Reattach button once a target is chosen and every selected node grants write", async () => {
    mockSession = { user: { username: "root", realm: "pam" }, caps: { pve1: fullCaps, pve2: fullCaps } };
    const user = userEvent.setup();
    renderPage();

    await user.click(screen.getByRole("checkbox", { name: "Select all" }));
    await user.selectOptions(screen.getByRole("combobox", { name: "Reattach selected guests to" }), "vmbr1");

    expect(screen.getByRole("button", { name: /Reattach 2 guest/ })).toBeEnabled();
  });
});
