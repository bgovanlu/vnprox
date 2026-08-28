// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ToastProvider } from "../components/Toast";
import { ApprovalPanel } from "./ApprovalPanel";
import type { ApprovalState } from "../api/types";

const approveMutateAsync = vi.fn();
const rejectMutateAsync = vi.fn();

vi.mock("./queries", () => ({
  useReviewApproveMutation: () => ({ mutateAsync: approveMutateAsync, isPending: false }),
  useReviewRejectMutation: () => ({ mutateAsync: rejectMutateAsync, isPending: false }),
}));

function renderPanel(approval: ApprovalState | undefined) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <ApprovalPanel changesetId="cs-1" approval={approval} />
      </ToastProvider>
    </QueryClientProvider>,
  );
}

describe("ApprovalPanel", () => {
  it("shows 'not yet reviewed' with no decision recorded", () => {
    renderPanel({ status: "none", required: false });
    expect(screen.getByText("Not yet reviewed")).toBeInTheDocument();
  });

  it("names the requirement when required and unapproved", () => {
    renderPanel({ status: "none", required: true });
    expect(screen.getByText(/required before this changeset can apply/i)).toBeInTheDocument();
  });

  it("shows the decider and timestamp once approved", () => {
    renderPanel({ status: "approved", required: true, decidedBy: "bob", decidedAt: 1_700_000_000 });
    expect(screen.getByText("Approved")).toBeInTheDocument();
    expect(screen.getByText(/by bob/)).toBeInTheDocument();
  });

  it("shows the rejection reason", () => {
    renderPanel({ status: "rejected", required: true, decidedBy: "carol", decidedAt: 1_700_000_000, reason: "needs another look" });
    expect(screen.getByText(/needs another look/)).toBeInTheDocument();
  });

  it("calls the approve mutation when Approve is clicked", async () => {
    approveMutateAsync.mockResolvedValueOnce(undefined);
    const user = userEvent.setup();
    renderPanel({ status: "none", required: true });
    await user.click(screen.getByRole("button", { name: "Approve" }));
    await waitFor(() => {
      expect(approveMutateAsync).toHaveBeenCalledWith("cs-1");
    });
  });

  it("reveals a reason field and calls the reject mutation on confirm", async () => {
    rejectMutateAsync.mockResolvedValueOnce(undefined);
    const user = userEvent.setup();
    renderPanel({ status: "none", required: true });
    await user.click(screen.getByRole("button", { name: "Reject" }));
    const reasonInput = await screen.findByLabelText("Rejection reason");
    await user.type(reasonInput, "not ready");
    await user.click(screen.getByRole("button", { name: "Confirm reject" }));
    await waitFor(() => {
      expect(rejectMutateAsync).toHaveBeenCalledWith({ id: "cs-1", reason: "not ready" });
    });
  });

  it("disables the Approve button once already approved", () => {
    renderPanel({ status: "approved", required: true });
    expect(screen.getByRole("button", { name: "Approve" })).toBeDisabled();
  });
});
