// T-605's read-only-mode UX sweep found this page's "Take snapshot" and
// "Create restore draft" affordances had no capability gating at all —
// this test pins the fix: both are disabled-with-tooltip for a
// netWrite-less session, and enabled for a full session.
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { MeResponse } from "../api/types";
import type { SnapshotListResponse, SnapshotSummary } from "../api/snapshots";
import { ToastProvider } from "../components/Toast";
import { HistoryPage } from "./HistoryPage";

const SNAPSHOT: SnapshotSummary = { id: "snap-1", kind: "manual", takenAt: 1_700_000_000, nodes: ["pve1"] };
const listResponse: SnapshotListResponse = { items: [SNAPSHOT] };

vi.mock("../api/snapshots", async () => {
  const actual = await vi.importActual<typeof import("../api/snapshots")>("../api/snapshots");
  return {
    ...actual,
    fetchSnapshots: vi.fn(() => Promise.resolve(listResponse)),
    createSnapshot: vi.fn(),
    restoreSnapshot: vi.fn(),
    fetchSnapshotDiff: vi.fn(),
  };
});

vi.mock("../api/useSession", () => ({
  useSession: () => ({ data: mockSession }),
}));

let mockSession: MeResponse | undefined;

const fullSession: MeResponse = {
  user: { username: "root", realm: "pam" },
  caps: { "": { netRead: true, netWrite: true, sdnRead: true, sdnWrite: true, fwRead: true, fwWrite: true, guestNet: true, audit: true } },
};

const readOnlySession: MeResponse = {
  user: { username: "auditor", realm: "pve" },
  caps: { "": { netRead: true, netWrite: false, sdnRead: false, sdnWrite: false, fwRead: false, fwWrite: false, guestNet: false, audit: true } },
};

function renderPage(): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <HistoryPage />
      </ToastProvider>
    </QueryClientProvider>,
  );
}

describe("HistoryPage read-only gating", () => {
  it("disables Take snapshot and Create restore draft for a netWrite-less session", async () => {
    mockSession = readOnlySession;
    const user = userEvent.setup();
    renderPage();

    expect(screen.getByRole("button", { name: "Take snapshot" })).toBeDisabled();

    await screen.findByText(/snap-1/);
    await user.click(screen.getByRole("button", { name: "Restore…" }));
    expect(await screen.findByRole("button", { name: "Create restore draft" })).toBeDisabled();
  });

  it("enables both for a full-capability session", async () => {
    mockSession = fullSession;
    const user = userEvent.setup();
    renderPage();

    expect(screen.getByRole("button", { name: "Take snapshot" })).toBeEnabled();

    await screen.findByText(/snap-1/);
    await user.click(screen.getByRole("button", { name: "Restore…" }));
    expect(await screen.findByRole("button", { name: "Create restore draft" })).toBeEnabled();
  });
});
