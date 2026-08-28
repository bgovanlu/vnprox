// SPDX-License-Identifier: Apache-2.0

// T-703 AC3/AC5: the review screen turns the server's touchesMgmtPath flag
// into a mandatory acknowledgement block — the ack renders, apply is
// disabled until the node name is typed exactly, and the confirm-window
// input floors at 180s. Backend is mocked at the api/changesets.ts boundary.
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ToastProvider } from "../components/Toast";
import type { Changeset } from "../api/types";
import { ReviewApplyScreen } from "./ReviewApplyScreen";

vi.mock("../api/changesets", () => ({
  diffChangeset: vi.fn(() => Promise.resolve({ files: [], ops: [] })),
  applyChangeset: vi.fn((_id: string, req: unknown) => Promise.resolve({ ...mgmtChangeset(), status: "awaiting_confirm", _req: req })),
  validateChangeset: vi.fn((id: string) => Promise.resolve(mgmtChangeset({ id }))),
}));

vi.mock("../api/ws", () => ({
  createWsClient: () => ({ subscribe: () => () => undefined, status: () => "closed", close: () => undefined }),
  defaultWsUrl: () => "ws://unused",
}));

import * as changesetsApi from "../api/changesets";

function mgmtChangeset(overrides: Partial<Changeset> = {}): Changeset {
  return {
    id: "csM",
    title: "Management redundancy: pve1",
    author: "root@pam",
    status: "validated",
    ops: [
      { op: "bridge.port.remove", target: "bridge:pve1:vmbr0", params: { port: "eno1" } },
      { op: "bond.create", target: "bond:pve1:bond0", params: { mode: "active-backup", slaves: ["eno1", "eno2"], miimon: 100 } },
      { op: "bridge.port.add", target: "bridge:pve1:vmbr0", params: { port: "bond0" } },
    ],
    findings: [],
    touchesMgmtPath: true,
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

describe("ReviewApplyScreen — management-path acknowledgement (T-703)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders the ack block and keeps Apply disabled until the node name is typed exactly", async () => {
    const user = userEvent.setup();
    renderReview(mgmtChangeset());

    expect(await screen.findByRole("group", { name: /management-path acknowledgement/i })).toBeInTheDocument();
    const applyBtn = screen.getByRole("button", { name: /^Apply$/ });
    expect(applyBtn).toBeDisabled();

    const ackInput = screen.getByLabelText(/type node name to acknowledge/i);
    await user.type(ackInput, "pve2"); // wrong node
    expect(applyBtn).toBeDisabled();

    await user.clear(ackInput);
    await user.type(ackInput, "pve1"); // correct
    await waitFor(() => {
      expect(applyBtn).toBeEnabled();
    });

    await user.click(applyBtn);
    await waitFor(() => {
      expect(changesetsApi.applyChangeset).toHaveBeenCalled();
    });
    const call = (changesetsApi.applyChangeset as unknown as { mock: { calls: unknown[][] } }).mock.calls[0] ?? [];
    expect(call[1]).toMatchObject({ mgmtAck: { node: "pve1" }, confirmTimeoutSec: 180 });
  });

  it("floors the confirm window at 180s for a management-path changeset", async () => {
    renderReview(mgmtChangeset());
    await screen.findByRole("group", { name: /management-path acknowledgement/i });
    const windowInput = screen.getByRole<HTMLInputElement>("spinbutton");
    expect(windowInput.value).toBe("180");
    expect(windowInput.min).toBe("180");
  });

  it("shows no ack block and defaults to 120s for a non-management changeset", async () => {
    renderReview(mgmtChangeset({ touchesMgmtPath: false, ops: [{ op: "bridge.create", target: "bridge:pve1:vmbr9", params: {} }] }));
    await waitFor(() => {
      expect(screen.queryByRole("group", { name: /management-path acknowledgement/i })).not.toBeInTheDocument();
    });
    const windowInput = screen.getByRole<HTMLInputElement>("spinbutton");
    expect(windowInput.value).toBe("120");
  });
});
