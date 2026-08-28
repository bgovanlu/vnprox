// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ToastProvider } from "../components/Toast";
import { CommentsPanel } from "./CommentsPanel";
import type { Changeset } from "../api/types";

const addMutateAsync = vi.fn();
const deleteMutate = vi.fn();

vi.mock("./queries", () => ({
  useAddCommentMutation: () => ({ mutateAsync: addMutateAsync, isPending: false }),
  useDeleteCommentMutation: () => ({ mutate: deleteMutate, isPending: false }),
}));

function baseChangeset(overrides: Partial<Changeset>): Changeset {
  return {
    id: "cs-1",
    title: "t",
    author: "alice",
    status: "draft",
    ops: [],
    findings: [],
    createdAt: 1,
    updatedAt: 1,
    ...overrides,
  };
}

function renderPanel(changeset: Changeset) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <CommentsPanel changeset={changeset} />
      </ToastProvider>
    </QueryClientProvider>,
  );
}

describe("CommentsPanel", () => {
  it("shows a changeset-level comment thread", () => {
    const cs = baseChangeset({
      comments: [{ id: "c1", author: "bob", body: "looks good overall", createdAt: 1_700_000_000 }],
    });
    renderPanel(cs);
    expect(screen.getByText("looks good overall")).toBeInTheDocument();
    expect(screen.getByText("bob")).toBeInTheDocument();
  });

  it("adds a changeset-level comment via the add-comment form", async () => {
    addMutateAsync.mockResolvedValueOnce(undefined);
    const user = userEvent.setup();
    renderPanel(baseChangeset({}));
    await user.type(screen.getByLabelText("Add a changeset-level comment"), "double-check the MTU");
    await user.click(screen.getByRole("button", { name: "Comment" }));
    await waitFor(() => {
      expect(addMutateAsync).toHaveBeenCalledWith({ id: "cs-1", opId: undefined, body: "double-check the MTU" });
    });
  });

  it("renders a per-op comment thread grouped under its own operation card", () => {
    const cs = baseChangeset({
      ops: [{ op: "bridge.create", target: "bridge:pve1:vmbr1", params: {}, id: "op-1" }],
      comments: [{ id: "c1", opId: "op-1", author: "bob", body: "worried about this bridge", createdAt: 1_700_000_000 }],
    });
    renderPanel(cs);
    expect(screen.getByText("worried about this bridge")).toBeInTheDocument();
  });

  it("shows an orphaned-comment section when a comment's op is no longer on the changeset", () => {
    const cs = baseChangeset({
      ops: [],
      comments: [{ id: "c1", opId: "op-gone", author: "bob", body: "stale comment", createdAt: 1_700_000_000 }],
    });
    renderPanel(cs);
    expect(screen.getByText("Comments on removed operations")).toBeInTheDocument();
    expect(screen.getByText("stale comment")).toBeInTheDocument();
  });

  it("calls the delete mutation when a comment's delete button is clicked", async () => {
    const user = userEvent.setup();
    const cs = baseChangeset({
      comments: [{ id: "c1", author: "bob", body: "remove me", createdAt: 1_700_000_000 }],
    });
    renderPanel(cs);
    await user.click(screen.getByRole("button", { name: "Delete comment" }));
    expect(deleteMutate).toHaveBeenCalledWith({ id: "cs-1", commentId: "c1" });
  });
});
