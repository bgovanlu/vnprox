// SPDX-License-Identifier: Apache-2.0

import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { LockNoticeBanner } from "./LockNoticeBanner";
import { useLockNoticeStore } from "./lockNoticeStore";
import type { Changeset, ChangesetLocks } from "../api/types";

const replaceOps = vi.fn();

vi.mock("./useDrawerActions", () => ({
  useDrawerActions: () => ({ replaceOps, addOps: vi.fn(), amendLastOps: vi.fn() }),
}));

const changeset: Changeset = {
  id: "cs-bob",
  title: "bob's draft",
  author: "bob@pam",
  status: "draft",
  ops: [{ op: "bridge.create", target: "bridge:pve1:vmbr0", params: {} }],
  findings: [],
  createdAt: 1_700_000_000,
  updatedAt: 1_700_000_000,
};

function setNotice(locks: ChangesetLocks | undefined, changesetId = "cs-bob") {
  useLockNoticeStore.getState().setNotice(changesetId, locks);
}

const heldByAlice: ChangesetLocks = {
  held: [
    {
      ref: "bridge:pve1:vmbr0",
      changesetId: "cs-alice",
      holder: "alice@pam",
      acquiredAt: 1_700_000_000,
      expiresAt: 1_700_000_900,
      mine: false,
    },
  ],
};

describe("LockNoticeBanner", () => {
  beforeEach(() => {
    replaceOps.mockReset();
    useLockNoticeStore.getState().clear();
  });

  it("renders nothing when nothing is contended", () => {
    render(<LockNoticeBanner changeset={changeset} />);
    expect(screen.queryByTestId("lock-notice")).not.toBeInTheDocument();
  });

  it("names the holder and the entity", () => {
    setNotice(heldByAlice);
    render(<LockNoticeBanner changeset={changeset} />);
    expect(screen.getByTestId("lock-notice")).toBeInTheDocument();
    expect(screen.getByText(/vmbr0 on pve1 — held by alice@pam/)).toBeInTheDocument();
  });

  // The load-bearing UI assertion for T-2805's "advisory, not mandatory":
  // this banner must never read like — or behave like — a block.
  it("states that the change is staged anyway and disables nothing", () => {
    setNotice(heldByAlice);
    render(<LockNoticeBanner changeset={changeset} />);
    expect(screen.getByText(/staged either way/i)).toBeInTheDocument();
    expect(screen.getByText(/does not block/i)).toBeInTheDocument();
    for (const button of screen.getAllByRole("button")) {
      expect(button).not.toBeDisabled();
    }
  });

  it("takes the lock over with the same ops when asked", async () => {
    replaceOps.mockResolvedValueOnce(changeset);
    setNotice(heldByAlice);
    const user = userEvent.setup();
    render(<LockNoticeBanner changeset={changeset} />);
    await user.click(screen.getByRole("button", { name: "Take over the lock" }));
    await waitFor(() => {
      expect(replaceOps).toHaveBeenCalledWith(changeset.ops, { lockOverride: true });
    });
  });

  it("reports a completed override and offers no further takeover", () => {
    setNotice({
      overridden: [
        {
          ref: "bridge:pve1:vmbr0",
          changesetId: "cs-alice",
          holder: "alice@pam",
          acquiredAt: 1_700_000_000,
          expiresAt: 1_700_000_900,
          mine: true,
        },
      ],
    });
    render(<LockNoticeBanner changeset={changeset} />);
    expect(screen.getByText(/took over/i)).toBeInTheDocument();
    expect(screen.getByText(/audit log/i)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Take over the lock" })).not.toBeInTheDocument();
  });

  // T-2805 AC5's UI consequence: `holder` is absent for a session without the
  // `audit` capability, and the banner must still be a sentence.
  it("renders without a name when the identity is withheld", () => {
    setNotice({
      held: [
        {
          ref: "bridge:pve1:vmbr0",
          changesetId: "cs-alice",
          acquiredAt: 1_700_000_000,
          expiresAt: 1_700_000_900,
          mine: false,
        },
      ],
    });
    render(<LockNoticeBanner changeset={changeset} />);
    expect(screen.getByText(/held by another operator/)).toBeInTheDocument();
    expect(screen.queryByText(/undefined/)).not.toBeInTheDocument();
  });

  it("never renders one draft's collision over another's", () => {
    setNotice(heldByAlice, "cs-someone-else");
    render(<LockNoticeBanner changeset={changeset} />);
    expect(screen.queryByTestId("lock-notice")).not.toBeInTheDocument();
  });

  it("clears itself when a later staging round trip has nothing to warn about", () => {
    setNotice(heldByAlice);
    const { rerender } = render(<LockNoticeBanner changeset={changeset} />);
    expect(screen.getByTestId("lock-notice")).toBeInTheDocument();
    setNotice({ held: [] });
    rerender(<LockNoticeBanner changeset={changeset} />);
    expect(screen.queryByTestId("lock-notice")).not.toBeInTheDocument();
  });
});
