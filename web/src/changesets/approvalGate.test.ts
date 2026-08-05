import { describe, expect, it } from "vitest";
import { blocksApply } from "./approvalGate";
import type { ApprovalState } from "../api/types";

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
