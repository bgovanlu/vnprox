// SPDX-License-Identifier: Apache-2.0

// T-3005 AC1/AC3/AC4: the review screen's apply-strategy picker.
//
// The load-bearing assertions here are about the REQUEST BODY, not about
// what the screen looks like: `mode: all` must send exactly what it has
// always sent, and a canary must send exactly the shape docs/api.md
// documents. Backend is mocked at the api/changesets.ts boundary.
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ToastProvider } from "../components/Toast";
import type { Changeset } from "../api/types";
import { ReviewApplyScreen } from "./ReviewApplyScreen";

vi.mock("../api/changesets", () => ({
  diffChangeset: vi.fn(() => Promise.resolve({ files: [], ops: [] })),
  applyChangeset: vi.fn(() => Promise.resolve({ ...twoNodeChangeset(), status: "applying" })),
  validateChangeset: vi.fn((id: string) => Promise.resolve(twoNodeChangeset({ id }))),
}));

vi.mock("../api/ws", () => ({
  createWsClient: () => ({ subscribe: () => () => undefined, status: () => "closed", close: () => undefined }),
  defaultWsUrl: () => "ws://unused",
}));

import * as changesetsApi from "../api/changesets";

/** A changeset whose plan preview touches three nodes with node-file ops
 * only — the shape canary apply is eligible for. */
function twoNodeChangeset(overrides: Partial<Changeset> = {}): Changeset {
  return {
    id: "csC",
    title: "MTU 9000 fleet-wide",
    author: "root@pam",
    status: "validated",
    ops: [
      { op: "iface.update", target: "iface:pve1:vmbr0", params: { mtu: 9000 } },
      { op: "iface.update", target: "iface:pve2:vmbr0", params: { mtu: 9000 } },
      { op: "iface.update", target: "iface:pve3:vmbr0", params: { mtu: 9000 } },
    ],
    findings: [],
    createdAt: 0,
    updatedAt: 0,
    ...overrides,
  };
}

function renderReview(cs: Changeset): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <ReviewApplyScreen changeset={cs} onClose={() => undefined} />
      </ToastProvider>
    </QueryClientProvider> as ReactNode,
  );
}

function applyCallBody(): unknown {
  const mock = changesetsApi.applyChangeset as unknown as { mock: { calls: unknown[][] } };
  return (mock.mock.calls[0] ?? [])[1];
}

describe("ReviewApplyScreen — apply strategy (T-3005)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("defaults to mode: all and sends TODAY'S request body, with no new fields", async () => {
    const user = userEvent.setup();
    renderReview(twoNodeChangeset());
    await screen.findByRole("region", { name: /apply strategy/i });

    await user.click(screen.getByRole("button", { name: /^Apply$/ }));
    await waitFor(() => {
      expect(changesetsApi.applyChangeset).toHaveBeenCalled();
    });
    // Regression assertion: exact equality, not toMatchObject. A stray
    // `applyStrategy: {mode:"all"}` or `autoRollbackOnError: false` would be
    // a change to the existing apply path, which this card must not make.
    expect(applyCallBody()).toEqual({ confirmTimeoutSec: 120 });
  });

  it("sends the documented canary body when a canary is configured", async () => {
    const user = userEvent.setup();
    renderReview(twoNodeChangeset());
    const panel = await screen.findByRole("region", { name: /apply strategy/i });

    await user.click(within(panel).getByRole("radio", { name: /canary/i }));
    await user.click(within(panel).getByRole("checkbox", { name: "pve1" }));

    await user.click(screen.getByRole("button", { name: /^Apply$/ }));
    await waitFor(() => {
      expect(changesetsApi.applyChangeset).toHaveBeenCalled();
    });
    expect(applyCallBody()).toEqual({
      confirmTimeoutSec: 120,
      applyStrategy: { mode: "canary", canaryNodes: ["pve1"], holdForSec: 60, gate: "manual" },
    });
  });

  it("keeps auto-rollback off by default and round-trips it into the body when ticked", async () => {
    const user = userEvent.setup();
    renderReview(twoNodeChangeset());
    const panel = await screen.findByRole("region", { name: /apply strategy/i });

    const toggle = within(panel).getByRole("checkbox", { name: /roll back automatically/i });
    expect(toggle).not.toBeChecked();
    await user.click(toggle);
    expect(toggle).toBeChecked();

    await user.click(screen.getByRole("button", { name: /^Apply$/ }));
    await waitFor(() => {
      expect(changesetsApi.applyChangeset).toHaveBeenCalled();
    });
    expect(applyCallBody()).toEqual({ confirmTimeoutSec: 120, autoRollbackOnError: true });
  });

  it("disables the canary option, with a reason, for a single-node changeset", async () => {
    renderReview(
      twoNodeChangeset({ ops: [{ op: "iface.update", target: "iface:pve1:vmbr0", params: { mtu: 9000 } }] }),
    );
    const panel = await screen.findByRole("region", { name: /apply strategy/i });
    expect(within(panel).getByRole("radio", { name: /canary/i })).toBeDisabled();
    expect(within(panel).getByTestId("canary-ineligible-reason")).toHaveTextContent(/at least two affected nodes/i);
  });

  it("blocks Apply, with the server's own reason, when the canary covers every node", async () => {
    const user = userEvent.setup();
    renderReview(twoNodeChangeset());
    const panel = await screen.findByRole("region", { name: /apply strategy/i });

    await user.click(within(panel).getByRole("radio", { name: /canary/i }));
    for (const node of ["pve1", "pve2", "pve3"]) {
      await user.click(within(panel).getByRole("checkbox", { name: node }));
    }
    expect(within(panel).getByTestId("apply-strategy-error")).toHaveTextContent(/no second stage/i);
    expect(screen.getByRole("button", { name: /^Apply$/ })).toBeDisabled();
  });

  it("blocks Apply when the hold is not shorter than the confirm window", async () => {
    const user = userEvent.setup();
    renderReview(twoNodeChangeset());
    const panel = await screen.findByRole("region", { name: /apply strategy/i });

    await user.click(within(panel).getByRole("radio", { name: /canary/i }));
    await user.click(within(panel).getByRole("checkbox", { name: "pve1" }));
    const hold = within(panel).getByLabelText(/canary hold seconds/i);
    await user.clear(hold);
    await user.type(hold, "120");

    expect(within(panel).getByTestId("apply-strategy-error")).toHaveTextContent(/shorter than the commit-confirm window/i);
    expect(screen.getByRole("button", { name: /^Apply$/ })).toBeDisabled();
  });
});
