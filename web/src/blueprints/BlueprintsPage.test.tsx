// T-605's read-only-mode UX sweep found this page's Import/Capture/Delete/
// Instantiate affordances had no capsForNode/hasAnyCap gating at all (every
// other write surface in the app already did) — this test pins the fix:
// all four are disabled-with-tooltip, not hidden, for a netWrite-less
// session (docs/user-guide.md §5), and enabled for a full session.
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { Blueprint, BlueprintsListResponse, MeResponse } from "../api/types";
import { ToastProvider } from "../components/Toast";
import { BlueprintsPage } from "./BlueprintsPage";

const STARTER: Blueprint = {
  blueprintVersion: 1,
  id: "starter-1",
  name: "Single NIC homelab",
  readOnly: true,
  nodeSelector: { mode: "all" },
  params: [],
  entities: [],
};

const SAVED: Blueprint = {
  blueprintVersion: 1,
  id: "saved-1",
  name: "My blueprint",
  readOnly: false,
  nodeSelector: { mode: "all" },
  params: [],
  entities: [],
};

const listResponse: BlueprintsListResponse = { items: [STARTER, SAVED] };

vi.mock("./queries", async () => {
  const actual = await vi.importActual<typeof import("./queries")>("./queries");
  return {
    ...actual,
    useBlueprintsQuery: () => ({ data: listResponse, isLoading: false, error: null }),
  };
});

vi.mock("../api/useSession", () => ({
  useSession: () => ({ data: mockSession }),
}));

let mockSession: MeResponse | undefined;

const fullSession: MeResponse = {
  user: { username: "root", realm: "pam" },
  caps: { "": { netRead: true, netWrite: true, sdnRead: true, sdnWrite: true, fwRead: true, fwWrite: true, guestNet: true, audit: true, capture: false } },
};

const readOnlySession: MeResponse = {
  user: { username: "auditor", realm: "pve" },
  caps: { "": { netRead: true, netWrite: false, sdnRead: false, sdnWrite: false, fwRead: false, fwWrite: false, guestNet: false, audit: true, capture: false } },
};

function renderPage(): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <BlueprintsPage />
      </ToastProvider>
    </QueryClientProvider>,
  );
}

describe("BlueprintsPage read-only gating", () => {
  it("disables Import/Capture/Delete/Instantiate for a netWrite-less session", async () => {
    mockSession = readOnlySession;
    renderPage();

    expect(screen.getByRole("button", { name: "Import" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Capture" })).toBeDisabled();

    await screen.findByRole("button", { name: new RegExp(STARTER.name) });
    screen.getByRole("button", { name: new RegExp(SAVED.name) }).click();
    expect(await screen.findByRole("button", { name: "Delete" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Instantiate" })).toBeDisabled();
  });

  it("enables the same affordances for a full-capability session", async () => {
    mockSession = fullSession;
    renderPage();

    expect(screen.getByRole("button", { name: "Import" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Capture" })).toBeEnabled();

    screen.getByRole("button", { name: new RegExp(SAVED.name) }).click();
    expect(await screen.findByRole("button", { name: "Delete" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Instantiate" })).toBeEnabled();
  });
});
