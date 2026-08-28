// SPDX-License-Identifier: Apache-2.0

// T-1805 (UI half): the apply dialog states plainly when a changeset will NOT
// self-revert, and for how long it will — and the countdown banner then shows
// the server's real coverage report once the change is in its confirm window.
// Backend is mocked at the api/changesets.ts boundary, mirroring
// ReviewApplyScreen.mgmt.test.tsx.
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ToastProvider } from "../components/Toast";
import type { Changeset, Op } from "../api/types";
import { CountdownBanner } from "./CountdownBanner";
import { ReviewApplyScreen } from "./ReviewApplyScreen";

vi.mock("../api/changesets", () => ({
  diffChangeset: vi.fn(() => Promise.resolve({ files: [], ops: [] })),
  applyChangeset: vi.fn(() => Promise.resolve(fwChangeset({ status: "awaiting_confirm" }))),
  validateChangeset: vi.fn(() => Promise.resolve(fwChangeset())),
  confirmChangeset: vi.fn(() => Promise.resolve(fwChangeset({ status: "committed" }))),
  rollbackChangeset: vi.fn(() => Promise.resolve(fwChangeset({ status: "rolled_back" }))),
}));

vi.mock("../api/ws", () => ({
  createWsClient: () => ({ subscribe: () => () => undefined, status: () => "closed", close: () => undefined }),
  defaultWsUrl: () => "ws://unused",
}));

const fwOp: Op = {
  op: "fw.rule.create",
  target: "fw-ruleset:pve1:guest/qemu/100",
  params: { direction: "in", action: "DROP" },
};
const bridgeOp: Op = { op: "bridge.create", target: "bridge:pve1:vmbr9", params: {} };

function fwChangeset(overrides: Partial<Changeset> = {}): Changeset {
  return {
    id: "csF",
    title: "Block 3306 on web01",
    author: "root@pam",
    status: "validated",
    ops: [fwOp],
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

function renderBanner(cs: Changeset): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <CountdownBanner changeset={cs} />
      </ToastProvider>
    </QueryClientProvider> as ReactNode,
  );
}

describe("ReviewApplyScreen — unattended-revert coverage notice (T-1805)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("tells the operator, before apply, that undoing the firewall part needs their Proxmox login", async () => {
    renderReview(fwChangeset());
    const notice = await screen.findByTestId("revert-coverage-notice");
    expect(notice).toHaveTextContent(/needs your Proxmox login/i);
    expect(notice).toHaveTextContent(/encrypted copy of your Proxmox session/i);
    // "for how long it will" — the default confirm window.
    expect(notice).toHaveTextContent(/120 seconds/);
    // ...and what happens when it will not.
    expect(notice).toHaveTextContent(/no longer undo itself automatically/i);
  });

  it("restates the coverage when the operator changes the confirm window", async () => {
    const user = userEvent.setup();
    renderReview(fwChangeset());
    await screen.findByTestId("revert-coverage-notice");

    const windowInput = screen.getByRole("spinbutton");
    await user.clear(windowInput);
    await user.type(windowInput, "300");

    expect(screen.getByTestId("revert-coverage-notice")).toHaveTextContent(/300 seconds/);
  });

  it("shows nothing for a changeset whose revert needs no PVE ticket", async () => {
    renderReview(fwChangeset({ ops: [bridgeOp] }));
    // Wait for the dialog itself before asserting an absence.
    await screen.findByText(/Review & apply/i);
    expect(screen.queryByTestId("revert-coverage-notice")).not.toBeInTheDocument();
  });
});

describe("CountdownBanner — in-window coverage report (T-1805)", () => {
  it("shows the server's reduced-coverage warning during the confirm window", () => {
    const nowSec = Math.floor(Date.now() / 1000);
    renderBanner(
      fwChangeset({
        status: "awaiting_confirm",
        confirmDeadline: nowSec + 600,
        unattendedRevert: { required: true, available: true, fullWindow: false, coversUntil: nowSec + 40 },
      }),
    );
    expect(screen.getByTestId("revert-coverage-status")).toHaveTextContent(/Automatic undo of the firewall\/SDN part stops working/i);
  });

  it("states plainly when there is no automatic undo of the firewall/SDN part at all", () => {
    const nowSec = Math.floor(Date.now() / 1000);
    renderBanner(
      fwChangeset({
        status: "awaiting_confirm",
        confirmDeadline: nowSec + 120,
        unattendedRevert: {
          required: true,
          available: false,
          fullWindow: false,
          reason: "no PVE session credential was available at apply time; firewall/SDN changes in this changeset will not revert automatically",
        },
      }),
    );
    expect(screen.getByTestId("revert-coverage-status")).toHaveTextContent(/will NOT undo itself/i);
  });

  it("stays quiet for a changeset whose revert needs no ticket", () => {
    const nowSec = Math.floor(Date.now() / 1000);
    renderBanner(
      fwChangeset({
        ops: [bridgeOp],
        status: "awaiting_confirm",
        confirmDeadline: nowSec + 120,
        unattendedRevert: { required: false, available: true, fullWindow: true, coversUntil: nowSec + 120 },
      }),
    );
    expect(screen.queryByTestId("revert-coverage-status")).not.toBeInTheDocument();
  });
});
