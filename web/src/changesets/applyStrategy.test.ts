// SPDX-License-Identifier: Apache-2.0

// T-3005: the client-side mirror of internal/change/apply_staged.go's
// validateApplyStrategy, and the request-body builder.
//
// The point of these tests is that the picker never OFFERS a strategy the
// server refuses, and that `mode: all` produces no `applyStrategy` field at
// all — the regression that keeps today's apply path byte-identical.
import { describe, expect, it } from "vitest";
import type { PlanStep } from "../api/types";
import {
  DEFAULT_CANARY_HOLD_SEC,
  affectedNodes,
  autoGateUnavailableReason,
  buildApplyStrategy,
  canaryEligibility,
  defaultSelection,
  selectionError,
  type StrategySelection,
} from "./applyStrategy";

function nodeSteps(...nodes: string[]): PlanStep[] {
  return nodes.flatMap((node) => [
    { kind: "stage_file" as const, node, summary: `Stage on ${node}` },
    { kind: "reload" as const, node, summary: `Reload on ${node}` },
  ]);
}

function canarySelection(overrides: Partial<StrategySelection> = {}): StrategySelection {
  return { ...defaultSelection(), mode: "canary", canaryNodes: ["pve1"], ...overrides };
}

describe("affectedNodes", () => {
  it("returns per-node steps' nodes in first-appearance order, deduplicated", () => {
    expect(affectedNodes(nodeSteps("pve2", "pve1", "pve2"))).toEqual(["pve2", "pve1"]);
  });

  it("ignores cluster-scope steps, which carry no node", () => {
    expect(affectedNodes([{ kind: "sdn_apply", summary: "Apply SDN" }])).toEqual([]);
  });
});

describe("canaryEligibility", () => {
  it("is eligible for a plan touching two or more nodes", () => {
    const e = canaryEligibility(nodeSteps("pve1", "pve2", "pve3"));
    expect(e.eligible).toBe(true);
    expect(e.nodes).toEqual(["pve1", "pve2", "pve3"]);
  });

  it("refuses a single-node plan, and says why", () => {
    const e = canaryEligibility(nodeSteps("pve1"));
    expect(e.eligible).toBe(false);
    expect(e.nodes).toEqual([]);
    expect(e.reason).toMatch(/at least two affected nodes/i);
  });

  it("refuses a plan carrying cluster-scope steps that must run first", () => {
    const e = canaryEligibility([
      { kind: "sdn_stage", summary: "Stage zone" },
      ...nodeSteps("pve1", "pve2"),
    ]);
    expect(e.eligible).toBe(false);
    expect(e.reason).toMatch(/sdn_stage/);
  });
});

describe("autoGateUnavailableReason", () => {
  it("is undefined for a plan of pure node-file steps", () => {
    expect(autoGateUnavailableReason(nodeSteps("pve1", "pve2"))).toBeUndefined();
  });

  it("names the steps that need a live PVE session", () => {
    const reason = autoGateUnavailableReason([...nodeSteps("pve1", "pve2"), { kind: "fw_apply", node: "pve1", summary: "fw" }]);
    expect(reason).toMatch(/fw_apply/);
  });
});

describe("selectionError", () => {
  const eligibility = canaryEligibility(nodeSteps("pve1", "pve2", "pve3"));

  it("never blocks mode: all", () => {
    expect(selectionError(defaultSelection(), eligibility, 120)).toBeUndefined();
  });

  it("accepts a proper subset of the affected nodes", () => {
    expect(selectionError(canarySelection(), eligibility, 120)).toBeUndefined();
  });

  it("refuses an empty canary list", () => {
    expect(selectionError(canarySelection({ canaryNodes: [] }), eligibility, 120)).toMatch(/at least one canary node/i);
  });

  it("refuses a canary list covering every affected node", () => {
    expect(selectionError(canarySelection({ canaryNodes: ["pve1", "pve2", "pve3"] }), eligibility, 120)).toMatch(
      /no second stage/i,
    );
  });

  it("refuses a hold outside [10, 600]", () => {
    expect(selectionError(canarySelection({ holdForSec: 5 }), eligibility, 900)).toMatch(/between 10 and 600/);
    expect(selectionError(canarySelection({ holdForSec: 601 }), eligibility, 900)).toMatch(/between 10 and 600/);
  });

  it("refuses a hold not shorter than the commit-confirm window", () => {
    expect(selectionError(canarySelection({ holdForSec: 120 }), eligibility, 120)).toMatch(/shorter than the commit-confirm window/i);
  });
});

describe("buildApplyStrategy", () => {
  const eligibility = canaryEligibility(nodeSteps("pve1", "pve2", "pve3"));

  it("emits NOTHING for mode: all — the default apply body is unchanged", () => {
    expect(buildApplyStrategy(defaultSelection(), eligibility)).toBeUndefined();
  });

  it("emits the documented canary shape", () => {
    expect(buildApplyStrategy(canarySelection({ canaryNodes: ["pve1"], gate: "auto", holdForSec: 45 }), eligibility)).toEqual({
      mode: "canary",
      canaryNodes: ["pve1"],
      holdForSec: 45,
      gate: "auto",
    });
  });

  it("orders canaryNodes by the plan, not by click order", () => {
    const strategy = buildApplyStrategy(canarySelection({ canaryNodes: ["pve3", "pve1"] }), eligibility);
    expect(strategy?.canaryNodes).toEqual(["pve1", "pve3"]);
  });

  it("drops a selected node the plan no longer affects", () => {
    const strategy = buildApplyStrategy(canarySelection({ canaryNodes: ["pve1", "pve9"] }), eligibility);
    expect(strategy?.canaryNodes).toEqual(["pve1"]);
  });

  it("defaults the hold to the engine's own default", () => {
    expect(defaultSelection().holdForSec).toBe(DEFAULT_CANARY_HOLD_SEC);
    expect(defaultSelection().gate).toBe("manual");
    expect(defaultSelection().mode).toBe("all");
  });
});
