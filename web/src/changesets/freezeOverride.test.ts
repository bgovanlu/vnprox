// SPDX-License-Identifier: Apache-2.0

// T-4006: reason validation and the blocked-hint logic, kept out of the
// component so both are directly testable (mirrors breakGlass.ts's own
// reasonError, which this file's freezeOverrideReasonError is deliberately
// parallel to).
import { describe, expect, it } from "vitest";
import { FREEZE_TAG, freezeBlocksApply, freezeOverrideReasonError, MAX_FREEZE_OVERRIDE_REASON_LEN } from "./freezeOverride";
import type { Finding } from "../api/types";
import type { PolicyResult, PolicyRuleResult } from "../api/policies";

function finding(overrides: Partial<Finding> = {}): Finding {
  return { severity: "error", code: "policy.violation", message: "m", ...overrides };
}

function ruleResult(overrides: Partial<PolicyRuleResult> = {}): PolicyRuleResult {
  return { ruleId: "r", description: "d", severity: "deny", ...overrides };
}

describe("freezeOverrideReasonError", () => {
  it("requires a non-blank reason", () => {
    expect(freezeOverrideReasonError("")).toBeDefined();
    expect(freezeOverrideReasonError("   ")).toBeDefined();
  });

  it("accepts an ordinary reason", () => {
    expect(freezeOverrideReasonError("on-call incident, router replacement")).toBeUndefined();
  });

  it("rejects a reason over the server's own bound", () => {
    const tooLong = "x".repeat(MAX_FREEZE_OVERRIDE_REASON_LEN + 1);
    expect(freezeOverrideReasonError(tooLong)).toBeDefined();
  });

  it("accepts a reason exactly at the bound", () => {
    const atBound = "x".repeat(MAX_FREEZE_OVERRIDE_REASON_LEN);
    expect(freezeOverrideReasonError(atBound)).toBeUndefined();
  });
});

describe("freezeBlocksApply", () => {
  it("is false with no blocking policy finding, even if a freeze rule is violating", () => {
    const result: PolicyResult = { rules: [ruleResult({ tags: [FREEZE_TAG], violatingOps: [0] })] };
    expect(freezeBlocksApply([], result)).toBe(false);
    expect(freezeBlocksApply([finding({ severity: "warning" })], result)).toBe(false);
  });

  it("is false with a blocking finding but no freeze-tagged rule violating", () => {
    const result: PolicyResult = { rules: [ruleResult({ tags: ["some-other-tag"], violatingOps: [0] })] };
    expect(freezeBlocksApply([finding()], result)).toBe(false);
  });

  it("is false when the policy-test result is not loaded yet", () => {
    expect(freezeBlocksApply([finding()], undefined)).toBe(false);
  });

  it("is true with both a blocking finding and a violating freeze-tagged rule", () => {
    const result: PolicyResult = { rules: [ruleResult({ tags: [FREEZE_TAG], violatingOps: [0] })] };
    expect(freezeBlocksApply([finding()], result)).toBe(true);
  });

  it("ignores a freeze-tagged rule that matched but did not violate", () => {
    const result: PolicyResult = { rules: [ruleResult({ tags: [FREEZE_TAG], matchedOps: [0], violatingOps: [] })] };
    expect(freezeBlocksApply([finding()], result)).toBe(false);
  });
});
