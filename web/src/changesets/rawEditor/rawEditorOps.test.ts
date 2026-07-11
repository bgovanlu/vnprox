import { describe, expect, it } from "vitest";
import type { Finding } from "../../api/types";
import { buildRawReplaceOp, errorFindings, findingsForRawOp, hasHashConflict, rawNodeTarget } from "./rawEditorOps";

describe("rawNodeTarget", () => {
  it("encodes a node:node:node ref", () => {
    expect(rawNodeTarget("pve1")).toBe("node:pve1:pve1");
  });
});

describe("buildRawReplaceOp", () => {
  it("builds the iface.raw.replace envelope with content and baseHash", () => {
    const op = buildRawReplaceOp("pve1", "auto lo\n", "abc123");
    expect(op).toEqual({
      op: "iface.raw.replace",
      target: "node:pve1:pve1",
      params: { content: "auto lo\n", baseHash: "abc123" },
    });
  });
});

function finding(overrides: Partial<Finding>): Finding {
  return { severity: "error", code: "raw.hash_conflict", message: "x", ref: "node:pve1:pve1", ...overrides };
}

describe("findingsForRawOp / hasHashConflict", () => {
  it("filters findings by the raw op's own ref, ignoring others", () => {
    const findings: Finding[] = [
      finding({ ref: "node:pve1:pve1", code: "raw.hash_conflict" }),
      finding({ ref: "bridge:pve1:vmbr0", code: "safety.protected_interface" }),
    ];
    expect(findingsForRawOp(findings, "pve1")).toHaveLength(1);
    expect(findingsForRawOp(findings, "pve2")).toHaveLength(0);
  });

  it("hasHashConflict is true only for the raw.hash_conflict code", () => {
    expect(hasHashConflict([finding({ code: "raw.hash_conflict" })], "pve1")).toBe(true);
    expect(hasHashConflict([finding({ code: "raw.parse_error" })], "pve1")).toBe(false);
    expect(hasHashConflict([], "pve1")).toBe(false);
  });

  it("scopes to the given node — a same-code finding on another node doesn't leak in", () => {
    const findings: Finding[] = [finding({ ref: "node:pve2:pve2", code: "raw.hash_conflict" })];
    expect(hasHashConflict(findings, "pve1")).toBe(false);
    expect(hasHashConflict(findings, "pve2")).toBe(true);
  });
});

describe("errorFindings", () => {
  it("includes error findings regardless of ref (AC2: a synthesized delta op's own ref)", () => {
    const findings: Finding[] = [
      finding({ ref: "bridge:pve1:vmbr0", code: "safety.protected_interface", severity: "error" }),
      finding({ ref: "node:pve1:pve1", code: "raw.parse_error", severity: "error" }),
    ];
    expect(errorFindings(findings)).toHaveLength(2);
  });

  it("excludes warnings and the hash-conflict code (which gets its own dedicated UI)", () => {
    const findings: Finding[] = [
      finding({ code: "advisory.bridge_missing_comment", severity: "warning" }),
      finding({ code: "raw.hash_conflict", severity: "error" }),
    ];
    expect(errorFindings(findings)).toHaveLength(0);
  });
});
