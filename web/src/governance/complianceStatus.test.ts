// T-3002 AC4's logic half: a control with no mapped evidence is `unmapped`,
// and `unmapped` is never a pass. Plus the fifth case the wire can produce
// and the Go union cannot — a status this build has never seen.
import { describe, expect, it } from "vitest";
import type { ComplianceReport } from "../api/compliance";
import {
  classifyControlStatus,
  classifyControls,
  countByStatus,
  isPassing,
  summaryDisagrees,
} from "./complianceStatus";

function report(overrides: Partial<ComplianceReport> = {}): ComplianceReport {
  return {
    productVersion: "3.0.3",
    profileId: "general-network-hygiene",
    profileTitle: "General network hygiene",
    profileVersion: "1",
    notice: "This report asserts no framework compliance.",
    generatedAt: 1_700_000_000,
    summary: { pass: 1, fail: 1, notEvaluated: 1, unmapped: 1, total: 4 },
    checkUniverse: "the check catalog this build can emit",
    controls: [
      { id: "c-pass", title: "p", statement: "", status: "pass", evidence: [{ kind: "check", name: "x", status: "satisfied", detail: "" }] },
      { id: "c-fail", title: "f", statement: "", status: "fail", evidence: [{ kind: "check", name: "y", status: "unsatisfied", detail: "" }] },
      { id: "c-ne", title: "n", statement: "", status: "not_evaluated", evidence: [{ kind: "posture", name: "z", status: "not_evaluated", detail: "" }] },
      { id: "c-unmapped", title: "u", statement: "", status: "unmapped", unmappedReason: "vnprox observes nothing here" },
    ],
    ...overrides,
  };
}

describe("compliance control statuses", () => {
  it("classifies the four documented values and nothing else", () => {
    expect(classifyControlStatus("pass")).toBe("pass");
    expect(classifyControlStatus("fail")).toBe("fail");
    expect(classifyControlStatus("not_evaluated")).toBe("not_evaluated");
    expect(classifyControlStatus("unmapped")).toBe("unmapped");
    expect(classifyControlStatus("partially_compliant")).toBe("unknown");
    expect(classifyControlStatus("")).toBe("unknown");
  });

  it("passes exactly one status — unmapped, not_evaluated and unknown are not it", () => {
    expect(isPassing("pass")).toBe(true);
    expect(isPassing("fail")).toBe(false);
    expect(isPassing("not_evaluated")).toBe(false);
    expect(isPassing("unmapped")).toBe(false);
    expect(isPassing("unknown")).toBe(false);
  });

  it("keeps an unmapped control in the list rather than dropping it", () => {
    const classified = classifyControls(report());
    expect(classified).toHaveLength(4);
    const unmapped = classified.find((c) => c.id === "c-unmapped");
    expect(unmapped?.classified).toBe("unmapped");
    expect(isPassing(unmapped?.classified ?? "unknown")).toBe(false);
  });

  it("counts locally, so a status this build cannot model cannot hide in the summary", () => {
    const r = report({
      controls: [
        ...report().controls,
        { id: "c-new", title: "future", statement: "", status: "partially_compliant" },
      ],
    });
    const counts = countByStatus(classifyControls(r));
    expect(counts).toEqual({ pass: 1, fail: 1, not_evaluated: 1, unmapped: 1, unknown: 1 });
    // The daemon's own four-bucket summary cannot represent the fifth, so it
    // now disagrees — and that disagreement is reported rather than resolved.
    expect(summaryDisagrees(r, counts)).toBe(true);
  });

  it("does not claim a disagreement when there is none", () => {
    const r = report();
    expect(summaryDisagrees(r, countByStatus(classifyControls(r)))).toBe(false);
    expect(summaryDisagrees(undefined, countByStatus([]))).toBe(false);
  });
});
