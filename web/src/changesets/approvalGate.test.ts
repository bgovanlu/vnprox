import { describe, expect, it } from "vitest";
import { blocksApply, twoPersonBlocksApply, twoPersonRequiredMessage } from "./approvalGate";
import type { ApprovalState, TwoPersonState } from "../api/types";

function approval(overrides: Partial<ApprovalState>): ApprovalState {
  return { status: "none", required: false, ...overrides };
}

describe("blocksApply", () => {
  it("is false when approval is undefined (older response / not decorated)", () => {
    expect(blocksApply(undefined)).toBe(false);
  });

  it("is false when the deployment does not require approval", () => {
    expect(blocksApply(approval({ required: false, status: "none" }))).toBe(false);
    expect(blocksApply(approval({ required: false, status: "rejected" }))).toBe(false);
  });

  it("is true when required and not yet approved", () => {
    expect(blocksApply(approval({ required: true, status: "none" }))).toBe(true);
  });

  it("is true when required and the current decision is a rejection", () => {
    expect(blocksApply(approval({ required: true, status: "rejected" }))).toBe(true);
  });

  it("is false once approved", () => {
    expect(blocksApply(approval({ required: true, status: "approved" }))).toBe(false);
  });
});

function twoPerson(overrides: Partial<TwoPersonState>): ApprovalState {
  return approval({
    twoPerson: {
      classes: [{ class: "fw.*", approvals: 2, ops: 1 }],
      approvers: [],
      required: 2,
      satisfied: false,
      ...overrides,
    },
  });
}

describe("twoPersonBlocksApply", () => {
  it("is false when there is no approval block at all", () => {
    expect(twoPersonBlocksApply(undefined)).toBe(false);
  });

  it("is false when the changeset is in no protected class", () => {
    expect(twoPersonBlocksApply(approval({ required: false }))).toBe(false);
  });

  it("is true when a protected class's requirement is unmet", () => {
    expect(twoPersonBlocksApply(twoPerson({ approvers: ["bob"] }))).toBe(true);
  });

  it("is false once the requirement is satisfied", () => {
    expect(twoPersonBlocksApply(twoPerson({ approvers: ["bob", "carol"], satisfied: true }))).toBe(false);
  });

  it("is false when an emergency break-glass override is on record", () => {
    expect(
      twoPersonBlocksApply(
        twoPerson({
          breakGlass: {
            changesetId: "cs1",
            reason: "corosync down",
            invokedBy: "alice",
            invokedAt: 1_700_000_000,
            ackableAt: 1_700_086_400,
          },
        }),
      ),
    ).toBe(false);
  });
});

describe("twoPersonRequiredMessage", () => {
  it("names the shortfall and the strictest protected class", () => {
    const msg = twoPersonRequiredMessage(
      twoPerson({
        approvers: ["bob"],
        required: 3,
        classes: [
          { class: "fw.*", approvals: 2, ops: 1 },
          { class: "tag:pci-scope", approvals: 3, ops: 1 },
        ],
      }),
    );
    expect(msg).toContain("3 different people");
    expect(msg).toContain("1 of 3");
    expect(msg).toContain("tag:pci-scope");
  });
});
