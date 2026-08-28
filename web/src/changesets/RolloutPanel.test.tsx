// SPDX-License-Identifier: Apache-2.0

// T-3005 AC2: the rollout view renders per-node state and the Continue
// action mid-hold, and a reload re-derives that state from the server rather
// than from anything this side remembered.
//
// The reload case is exercised through <ChangesetDrawer/> rather than
// through <RolloutPanel/> directly, because that is where the claim actually
// lives: the drawer store persists only the changeset id, and the hold comes
// back from GET /changesets/{id}. Unmounting everything and remounting with
// a brand-new QueryClient is a page reload as far as this code can tell.
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ToastProvider } from "../components/Toast";
import type { Changeset } from "../api/types";
import { ChangesetDrawer } from "./ChangesetDrawer";
import { RolloutPanel } from "./RolloutPanel";
import { useChangesetDrawerStore } from "./store";

vi.mock("../api/changesets", () => ({
  listChangesets: vi.fn(() => Promise.resolve([])),
  getChangeset: vi.fn(() => Promise.resolve(heldChangeset())),
  createChangeset: vi.fn(),
  updateChangeset: vi.fn(),
  discardChangeset: vi.fn(),
  validateChangeset: vi.fn(),
  diffChangeset: vi.fn(() => Promise.resolve({ files: [], ops: [] })),
  applyChangeset: vi.fn(),
  confirmChangeset: vi.fn(),
  continueChangeset: vi.fn(() => Promise.resolve({ ...heldChangeset(), status: "awaiting_confirm", applyStage: undefined })),
  rollbackChangeset: vi.fn(),
}));

vi.mock("../api/ws", () => ({
  createWsClient: () => ({ subscribe: () => () => undefined, status: () => "closed", close: () => undefined }),
  defaultWsUrl: () => "ws://unused",
}));

import * as changesetsApi from "../api/changesets";

/** A changeset paused mid-canary: pve1 applied, pve2/pve3 never contacted. */
function heldChangeset(overrides: Partial<Changeset> = {}): Changeset {
  return {
    id: "csHold",
    title: "MTU 9000 fleet-wide",
    author: "root@pam",
    status: "applying",
    ops: [{ op: "iface.update", target: "iface:pve1:vmbr0", params: { mtu: 9000 } }],
    findings: [],
    createdAt: 0,
    updatedAt: 0,
    applyStage: {
      state: "canary_hold",
      author: "root@pam",
      strategy: { mode: "canary", gate: "manual", canaryNodes: ["pve1"], holdForSec: 60 },
      appliedNodes: ["pve1"],
      pendingNodes: ["pve2", "pve3"],
      holdStartedAt: 1_700_000_100,
      holdDeadline: 1_700_000_160,
      confirmDeadline: 1_700_000_220,
    },
    ...overrides,
  };
}

function renderPanel(cs: Changeset): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <RolloutPanel changeset={cs} />
      </ToastProvider>
    </QueryClientProvider> as ReactNode,
  );
}

function renderDrawer(): { unmount: () => void } {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const { unmount } = render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <ChangesetDrawer />
      </ToastProvider>
    </QueryClientProvider> as ReactNode,
  );
  return { unmount };
}

describe("RolloutPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useChangesetDrawerStore.setState({ activeId: "csHold", drawerOpen: false, reviewRequested: false, warningsAcknowledged: false });
  });

  it("renders nothing for a changeset with no staged apply", () => {
    renderPanel(heldChangeset({ applyStage: undefined }));
    expect(screen.queryByTestId("rollout-panel")).not.toBeInTheDocument();
  });

  it("renders per-node state and what the gate is waiting for", () => {
    renderPanel(heldChangeset());
    expect(screen.getByTestId("rollout-node-pve1")).toHaveTextContent(/pve1 — applied/);
    expect(screen.getByTestId("rollout-node-pve2")).toHaveTextContent(/pve2 — not contacted/);
    expect(screen.getByTestId("rollout-node-pve3")).toHaveTextContent(/pve3 — not contacted/);
    expect(screen.getByTestId("rollout-panel")).toHaveTextContent(/waiting for you/i);
  });

  it("calls POST /changesets/{id}/continue from the Continue action", async () => {
    const user = userEvent.setup();
    renderPanel(heldChangeset());
    await user.click(screen.getByRole("button", { name: /continue to remaining nodes/i }));
    await waitFor(() => {
      expect(changesetsApi.continueChangeset).toHaveBeenCalledWith("csHold");
    });
  });

  it("offers no Continue while the remaining stage is already promoting", () => {
    renderPanel(
      heldChangeset({
        applyStage: {
          state: "promoting",
          strategy: { mode: "canary", gate: "manual", canaryNodes: ["pve1"], holdForSec: 60 },
          appliedNodes: ["pve1"],
          pendingNodes: ["pve2", "pve3"],
        },
      }),
    );
    expect(screen.getByRole("button", { name: /continue to remaining nodes/i })).toBeDisabled();
  });

  it("renders an unaccounted-for node as unknown, not as pending", () => {
    renderPanel(
      heldChangeset({
        applyStage: {
          state: "canary_hold",
          strategy: { mode: "canary", gate: "manual", canaryNodes: ["pve1"] },
          appliedNodes: ["pve1"],
          pendingNodes: ["pve2"],
        },
        plan: {
          steps: [
            { kind: "reload", node: "pve1", summary: "" },
            { kind: "reload", node: "pve2", summary: "" },
            { kind: "reload", node: "pve3", summary: "" },
          ],
        },
      }),
    );
    expect(screen.getByTestId("rollout-node-pve3")).toHaveTextContent(/pve3 — unknown/);
    expect(screen.getByTestId("rollout-node-pve3")).not.toHaveTextContent(/not contacted/);
  });

  it("refuses to render an absent node report as an empty (reassuring) list", () => {
    renderPanel(
      heldChangeset({
        applyStage: { state: "canary_hold", strategy: { mode: "canary", gate: "manual" } },
      }),
    );
    expect(screen.queryByTestId("rollout-nodes")).not.toBeInTheDocument();
    expect(screen.getByTestId("rollout-panel")).toHaveTextContent(/did not report which nodes/i);
  });
});

describe("RolloutPanel — restart/reload survival", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useChangesetDrawerStore.setState({ activeId: "csHold", drawerOpen: false, reviewRequested: false, warningsAcknowledged: false });
  });

  it("re-derives the hold from GET /changesets/{id} on a fresh mount", async () => {
    const first = renderDrawer();
    expect(await screen.findByTestId("rollout-panel")).toHaveTextContent(/canary hold/i);
    first.unmount();

    // A page reload: nothing but the changeset id survives (the drawer store
    // persists that alone), and everything else is fetched again.
    renderDrawer();
    const panel = await screen.findByTestId("rollout-panel");
    expect(panel).toHaveTextContent(/canary hold/i);
    expect(screen.getByTestId("rollout-node-pve1")).toHaveTextContent(/pve1 — applied/);
    expect(changesetsApi.getChangeset).toHaveBeenCalledWith("csHold");
  });
});
