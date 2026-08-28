// SPDX-License-Identifier: Apache-2.0

// Unit coverage for findingBadges.ts's pure parsing/decision helpers — the
// shared vocabulary EntityNode.tsx, EntityEdge.tsx, canvasDraw.ts,
// SwitchFaceplate.tsx, and a11yBridge.ts all key their T-3501 rendering off,
// so a bug here would silently propagate wrong behavior into every renderer
// at once. Exercised directly rather than only through a component test.
import { describe, expect, it } from "vitest";
import {
  findingBadgeClass,
  findingChipText,
  findingDetailText,
  findingsForSource,
  hasOpenFinding,
  parseFindingBadge,
  parsedFindingBadges,
  shouldPulse,
  worstFindingSeverity,
} from "./findingBadges";

describe("parseFindingBadge", () => {
  it("parses a well-formed finding:<source>:<severity> token", () => {
    expect(parseFindingBadge("finding:health:error")).toEqual({ source: "health", severity: "error" });
  });

  it("returns undefined for the legacy bare drift token", () => {
    expect(parseFindingBadge("drift")).toBeUndefined();
  });

  it("returns undefined for every other badge vocabulary word", () => {
    for (const b of ["mgmt", "corosync", "mgmt-path", "qos-shaped", "vlans=10-20", "mode=802.3ad"]) {
      expect(parseFindingBadge(b)).toBeUndefined();
    }
  });

  it("returns undefined for an unrecognized severity (never fabricates one)", () => {
    expect(parseFindingBadge("finding:health:catastrophic")).toBeUndefined();
  });

  it("returns undefined for a malformed token missing the severity segment", () => {
    expect(parseFindingBadge("finding:health")).toBeUndefined();
    expect(parseFindingBadge("finding:health:")).toBeUndefined();
  });
});

describe("hasOpenFinding", () => {
  it("is true for a finding: token", () => {
    expect(hasOpenFinding(["finding:drift:warning"])).toBe(true);
  });

  it("is true for the legacy bare drift token alone (wgEdgeStatus.ts's synthesized edges have no richer form)", () => {
    expect(hasOpenFinding(["drift"])).toBe(true);
  });

  it("is false when neither is present", () => {
    expect(hasOpenFinding(["mgmt", "vlans=10-20"])).toBe(false);
    expect(hasOpenFinding([])).toBe(false);
  });
});

describe("worstFindingSeverity", () => {
  it("picks the highest-ranked severity among finding: tokens", () => {
    expect(worstFindingSeverity(["finding:drift:warning", "finding:health:error"])).toBe("error");
    expect(worstFindingSeverity(["finding:health:info", "finding:drift:warning"])).toBe("warning");
  });

  it("is undefined when only the legacy bare drift token is present — no severity is knowable", () => {
    expect(worstFindingSeverity(["drift"])).toBeUndefined();
  });

  it("is undefined for no badges at all", () => {
    expect(worstFindingSeverity([])).toBeUndefined();
  });
});

describe("shouldPulse (T-3501: motion reserved for the severity that warrants it)", () => {
  it("pulses for error severity", () => {
    expect(shouldPulse(["finding:health:error"])).toBe(true);
  });

  it("does not pulse for warning or info severity", () => {
    expect(shouldPulse(["finding:drift:warning"])).toBe(false);
    expect(shouldPulse(["finding:health:info"])).toBe(false);
  });

  it("falls back to pulsing for the legacy-badge-only case — no regression to motionless for an un-upgraded producer", () => {
    expect(shouldPulse(["drift"])).toBe(true);
  });

  it("does not pulse when there is no open finding at all", () => {
    expect(shouldPulse(["mgmt"])).toBe(false);
  });

  it("picks the pulse-worthiness of the worst source when several sources disagree", () => {
    // health is error (pulses), drift is warning (would not alone) — the
    // worst-of reduction must win, since an entity carrying BOTH must not
    // read as calmer than its worse-off half.
    expect(shouldPulse(["finding:drift:warning", "finding:health:error"])).toBe(true);
  });
});

describe("findingChipText / findingBadgeClass (glyph + colour, never colour alone)", () => {
  it("prefixes a distinct glyph per severity", () => {
    expect(findingChipText({ source: "health", severity: "error" })).toBe("■ health");
    expect(findingChipText({ source: "drift", severity: "warning" })).toBe("▲ drift");
    expect(findingChipText({ source: "ipam", severity: "info" })).toBe("● ipam");
  });

  it("gives each severity a distinct colour class", () => {
    // T-4204: the semantic status scale, not a hand-picked emerald/amber/red.
    expect(findingBadgeClass("error")).toContain("status-critical");
    expect(findingBadgeClass("warning")).toContain("status-degraded");
    expect(findingBadgeClass("info")).toContain("status-info");
    expect(findingBadgeClass("info")).not.toContain("status-critical");
    expect(findingBadgeClass("info")).not.toContain("status-degraded");
  });
});

describe("findingsForSource / findingDetailText", () => {
  const findings = [
    { source: "health" as const, severity: "error" as const, check: "bridge_no_carrier", detail: "enp2s0 has no carrier" },
    { source: "drift" as const, severity: "warning" as const, check: "file_runtime_divergence", detail: "" },
  ];

  it("filters an entity's full findings list down to one source", () => {
    expect(findingsForSource(findings, "health")).toHaveLength(1);
    expect(findingsForSource(findings, "health")[0]?.detail).toBe("enp2s0 has no carrier");
  });

  it("returns an empty array for a source with no matching finding, or an undefined list", () => {
    expect(findingsForSource(findings, "lldp")).toEqual([]);
    expect(findingsForSource(undefined, "health")).toEqual([]);
  });

  it("surfaces the finding's own detail text — the operator should not have to leave the map", () => {
    expect(findingDetailText({ source: "health", severity: "error" }, findings)).toBe("enp2s0 has no carrier");
  });

  it("falls back to a plain severity/source sentence when detail is empty or unavailable", () => {
    expect(findingDetailText({ source: "drift", severity: "warning" }, findings)).toBe("warning drift finding");
    expect(findingDetailText({ source: "health", severity: "error" }, undefined)).toBe("error health finding");
  });
});

describe("parsedFindingBadges preserves order and skips non-finding tokens", () => {
  it("parses only the finding: tokens, in their original order", () => {
    expect(parsedFindingBadges(["mgmt", "finding:drift:warning", "drift", "finding:health:error"])).toEqual([
      { source: "drift", severity: "warning" },
      { source: "health", severity: "error" },
    ]);
  });
});
