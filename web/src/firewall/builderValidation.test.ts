// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { validateRuleBuilder } from "./builderValidation";

const knownMacros = new Set(["HTTP", "HTTPS", "SSH"]);

function base() {
  return { direction: "in", action: "ACCEPT", proto: "", macro: "", dport: "" };
}

describe("validateRuleBuilder", () => {
  it("accepts a minimal valid rule", () => {
    expect(validateRuleBuilder(base(), knownMacros)).toEqual([]);
  });

  it("accepts a valid macro rule", () => {
    expect(validateRuleBuilder({ ...base(), macro: "HTTP" }, knownMacros)).toEqual([]);
  });

  it("rejects an unknown direction", () => {
    const errors = validateRuleBuilder({ ...base(), direction: "sideways" }, knownMacros);
    expect(errors).toContain("Direction must be in, out, forward, or a security-group reference.");
  });

  it("accepts the forward direction (T-3103)", () => {
    expect(validateRuleBuilder({ ...base(), direction: "forward" }, knownMacros)).toEqual([]);
  });

  it("rejects an unknown action", () => {
    const errors = validateRuleBuilder({ ...base(), action: "MAYBE" }, knownMacros);
    expect(errors).toContain("Action must be ACCEPT, DROP, or REJECT.");
  });

  it("a group-direction rule requires a group name in action, not ACCEPT/DROP/REJECT", () => {
    const errors = validateRuleBuilder({ ...base(), direction: "group", action: "" }, knownMacros);
    expect(errors).toContain("Choose a security group to reference.");
  });

  it("a group-direction rule with a group name in action is valid", () => {
    const errors = validateRuleBuilder({ ...base(), direction: "group", action: "base-services" }, knownMacros);
    expect(errors).toEqual([]);
  });

  it("rejects an unknown macro", () => {
    const errors = validateRuleBuilder({ ...base(), macro: "NOT-REAL" }, knownMacros);
    expect(errors).toContain('"NOT-REAL" is not a known macro.');
  });

  it("rejects proto/dport combined with a macro", () => {
    const errors = validateRuleBuilder({ ...base(), macro: "HTTP", dport: "8080" }, knownMacros);
    expect(errors).toContain("A macro already implies its own proto/ports — clear proto/port or remove the macro.");
  });

  it("rejects an unrecognized proto", () => {
    const errors = validateRuleBuilder({ ...base(), proto: "sctp" }, knownMacros);
    expect(errors).toContain("Proto must be tcp, udp, or icmp.");
  });
});
