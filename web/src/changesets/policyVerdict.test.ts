// SPDX-License-Identifier: Apache-2.0

// T-3002 AC1's logic half: what the review screen is allowed to conclude
// about a policy refusal, and — more to the point — what it is not.
import { describe, expect, it } from "vitest";
import { ApiError } from "../api/client";
import type { PolicyResult, PolicyStatus } from "../api/policies";
import { classifySeverity, conditionText, policyFindings, policyVerdict, verdictBlocks } from "./policyVerdict";

function status(overrides: Partial<PolicyStatus> = {}): PolicyStatus {
  return {
    revision: 3,
    set: {
      version: 1,
      rules: [
        {
          id: "no-flat-vlan",
          description: "guest NICs must carry a VLAN tag",
          severity: "deny",
          match: [{ field: "op", op: "eq", value: "guest.nic.update" }],
          assert: [{ field: "params.vlan", op: "exists" }],
        },
        {
          id: "never-vmbr9",
          description: "vmbr9 is the storage fabric",
          severity: "deny",
          match: [{ field: "target.name", op: "eq", value: "vmbr9" }],
        },
      ],
    },
    ...overrides,
  };
}

const clean: PolicyResult = {
  rules: [
    { ruleId: "no-flat-vlan", description: "guest NICs must carry a VLAN tag", severity: "deny", matchedOps: [0] },
    { ruleId: "never-vmbr9", description: "vmbr9 is the storage fabric", severity: "deny" },
  ],
};

const denied: PolicyResult = {
  rules: [
    {
      ruleId: "no-flat-vlan",
      description: "guest NICs must carry a VLAN tag",
      severity: "deny",
      matchedOps: [0, 1],
      violatingOps: [0, 1],
    },
    { ruleId: "never-vmbr9", description: "vmbr9 is the storage fabric", severity: "deny", matchedOps: [] },
  ],
};

function verdictFor(s: PolicyStatus | undefined, r: PolicyResult | undefined, errors: Partial<{ statusError: unknown; resultError: unknown }> = {}) {
  return policyVerdict({
    status: s,
    statusError: errors.statusError ?? null,
    result: r,
    resultError: errors.resultError ?? null,
    isLoading: false,
  });
}

describe("policyVerdict", () => {
  it("names the violated rule and carries its assertions from the installed set", () => {
    const v = verdictFor(status(), denied);
    expect(v.kind).toBe("violations");
    if (v.kind !== "violations") return;
    expect(v.rules).toHaveLength(1);
    const rule = v.rules[0];
    expect(rule?.ruleId).toBe("no-flat-vlan");
    expect(rule?.severity).toBe("deny");
    expect(rule?.assertKnown).toBe(true);
    expect(rule?.assert.map(conditionText)).toEqual(["params.vlan exists"]);
    expect(rule?.violatingOpCount).toBe(2);
    expect(verdictBlocks(v)).toBe(true);
  });

  it("does not report a rule that matched but did not violate", () => {
    const v = verdictFor(status(), clean);
    expect(v).toEqual({ kind: "clean", ruleCount: 2 });
    expect(verdictBlocks(v)).toBe(false);
  });

  it("reports an unreadable rule's assertions as unreadable, NOT as absent", () => {
    // A rule with no assertions means "the match itself is the violation",
    // which is a stronger claim than "we could not read its assertions".
    // Collapsing the two would have the panel state the stronger one on no
    // evidence.
    const withoutRule = status({ set: { version: 1, rules: [] } });
    const v = verdictFor(withoutRule, denied);
    // An empty installed set short-circuits to none-installed, so use a set
    // that has rules but not this one.
    expect(v.kind).toBe("none-installed");

    const other = status({
      set: {
        version: 1,
        rules: [{ id: "unrelated", description: "", severity: "warn", match: [{ field: "op", op: "eq", value: "x" }] }],
      },
    });
    const v2 = verdictFor(other, denied);
    expect(v2.kind).toBe("violations");
    if (v2.kind !== "violations") return;
    expect(v2.rules[0]?.assertKnown).toBe(false);
    expect(v2.rules[0]?.assert).toEqual([]);
  });

  // T-3204: internal/change.PolicySet's Go zero value leaves Rules nil, and a
  // nil slice marshals to JSON `null`, not `[]` — this is what a cluster with
  // no installed policy set genuinely sends over the wire (confirmed against
  // a real daemon: GET /policies returns `"set":{"rules":null,...}`), not the
  // `rules: []` the test above already covers. Found via a real crash
  // ("Cannot read properties of null (reading 'length')") reproducing on
  // every changeset review dialog for such a cluster.
  it("treats a null (not just empty) installed rules array as no policy installed", () => {
    const nullRules = status({ set: { version: 1, rules: null } });
    const v = verdictFor(nullRules, denied);
    expect(v.kind).toBe("none-installed");
  });

  it("distinguishes a rule that genuinely asserts nothing", () => {
    const v = verdictFor(status(), {
      rules: [{ ruleId: "never-vmbr9", description: "", severity: "deny", violatingOps: [0] }],
    });
    expect(v.kind).toBe("violations");
    if (v.kind !== "violations") return;
    expect(v.rules[0]?.assertKnown).toBe(true);
    expect(v.rules[0]?.assert).toEqual([]);
  });

  it("treats an unrecognised severity as blocking, never as a warning", () => {
    expect(classifySeverity("deny")).toBe("deny");
    expect(classifySeverity("warn")).toBe("warn");
    expect(classifySeverity("advisory")).toBe("unknown");

    const s = status({
      set: {
        version: 1,
        rules: [{ id: "future", description: "", severity: "advisory", match: [{ field: "op", op: "eq", value: "x" }] }],
      },
    });
    const v = verdictFor(s, { rules: [{ ruleId: "future", description: "", severity: "advisory", violatingOps: [0] }] });
    expect(v.kind).toBe("violations");
    if (v.kind !== "violations") return;
    expect(v.rules[0]?.severity).toBe("unknown");
    expect(v.rules[0]?.rawSeverity).toBe("advisory");
    expect(verdictBlocks(v)).toBe(true);
  });

  it("never renders a read failure as 'no policy applies'", () => {
    const v = verdictFor(undefined, undefined, { statusError: new Error("boom") });
    expect(v).toEqual({ kind: "unreadable", message: "boom" });
    expect(verdictBlocks(v)).toBe(false);
  });

  it("separates a daemon with no policy store from a daemon with no rules", () => {
    const notConfigured = verdictFor(undefined, undefined, {
      statusError: new ApiError(503, "policy_unavailable", "change: policy store is not configured on this daemon"),
    });
    expect(notConfigured.kind).toBe("not-configured");

    const noRules = verdictFor(status({ set: { version: 1, rules: [] } }), {});
    expect(noRules.kind).toBe("none-installed");
  });

  it("puts deny and unrecognised severities ahead of warn", () => {
    const s = status({
      set: {
        version: 1,
        rules: [
          { id: "w", description: "", severity: "warn", match: [] },
          { id: "u", description: "", severity: "nonsense", match: [] },
          { id: "d", description: "", severity: "deny", match: [] },
        ],
      },
    });
    const v = verdictFor(s, {
      rules: [
        { ruleId: "w", description: "", severity: "warn", violatingOps: [0] },
        { ruleId: "u", description: "", severity: "nonsense", violatingOps: [1] },
        { ruleId: "d", description: "", severity: "deny", violatingOps: [2] },
      ],
    });
    expect(v.kind).toBe("violations");
    if (v.kind !== "violations") return;
    expect(v.rules.map((r) => r.ruleId)).toEqual(["d", "u", "w"]);
  });

  it("renders a condition the way the daemon expresses it, with no value where there is none", () => {
    expect(conditionText({ field: "params.mtu", op: "gte", value: 1500 })).toBe("params.mtu gte 1500");
    expect(conditionText({ field: "params.vlan", op: "exists" })).toBe("params.vlan exists");
    expect(conditionText({ field: "target.name", op: "in", value: ["a", "b"] })).toBe('target.name in ["a","b"]');
  });

  it("picks out only the policy findings a changeset carries", () => {
    const picked = policyFindings([
      { severity: "error", code: "policy.violation", message: "policy rule \"no-flat-vlan\": ..." },
      { severity: "error", code: "schema.mtu_invalid", message: "unrelated" },
      { severity: "error", code: "policy.invalid", message: "the cluster's installed policy set cannot be parsed" },
    ]);
    expect(picked.map((f) => f.code)).toEqual(["policy.violation", "policy.invalid"]);
  });
});
