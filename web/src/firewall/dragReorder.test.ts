import { describe, expect, it } from "vitest";
import type { RuleView } from "../api/types";
import { computeReorderMove } from "./dragReorder";

function rule(pos: number, comment: string): RuleView {
  return { pos, enabled: true, direction: "in", action: "ACCEPT", comment };
}

describe("computeReorderMove", () => {
  const rules = [rule(0, "a"), rule(1, "b"), rule(2, "c"), rule(3, "d")];

  it("moves the dragged rule to the end", () => {
    const result = computeReorderMove("fw-ruleset:pve1:guest/qemu/100", rules, 0, 3);
    expect(result).toBeDefined();
    expect(result?.op).toEqual({
      op: "fw.rule.move",
      target: "fw-ruleset:pve1:guest/qemu/100",
      params: {
        fromPos: 0,
        toPos: 3,
        expect: { direction: "in", action: "ACCEPT", comment: "a", enabled: true },
      },
    });
    expect(result?.optimistic.map((r) => r.comment)).toEqual(["b", "c", "d", "a"]);
    expect(result?.optimistic.map((r) => r.pos)).toEqual([0, 1, 2, 3]);
  });

  it("moves a rule earlier in the list", () => {
    const result = computeReorderMove("target", rules, 3, 1);
    expect(result?.optimistic.map((r) => r.comment)).toEqual(["a", "d", "b", "c"]);
  });

  it("moves a middle rule to a middle position", () => {
    const result = computeReorderMove("target", rules, 1, 2);
    expect(result?.optimistic.map((r) => r.comment)).toEqual(["a", "c", "b", "d"]);
  });

  it("returns undefined for a no-op drag (same index)", () => {
    expect(computeReorderMove("target", rules, 1, 1)).toBeUndefined();
  });

  it("returns undefined for an out-of-range index", () => {
    expect(computeReorderMove("target", rules, 0, 99)).toBeUndefined();
    expect(computeReorderMove("target", rules, -1, 2)).toBeUndefined();
  });

  it("carries the dragged rule's own fields as the move's Expect fingerprint", () => {
    const withMacro: RuleView = { pos: 0, enabled: false, direction: "in", action: "ACCEPT", macro: "HTTP", comment: "web" };
    const result = computeReorderMove("target", [withMacro, rule(1, "b")], 0, 1);
    expect(result?.op.params).toMatchObject({
      expect: { direction: "in", action: "ACCEPT", macro: "HTTP", comment: "web", enabled: false },
    });
  });
});
