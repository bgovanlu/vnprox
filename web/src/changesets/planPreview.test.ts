import { describe, expect, it } from "vitest";
import type { Op } from "../api/types";
import { buildPlanPreview } from "./planPreview";

describe("buildPlanPreview (mirrors internal/change/apply_plan.go BuildPlan)", () => {
  it("groups node-file ops per node in first-appearance order, an adjacent stage->reload pair each", () => {
    const ops: Op[] = [
      { op: "bridge.create", target: "bridge:pve1:vmbr1", params: {} },
      { op: "bond.update", target: "bond:pve2:bond0", params: { mtu: 9000 } },
      { op: "vlan.create", target: "vlan:pve1:vmbr1.30", params: { parent: "vmbr1", vid: 30 } },
    ];
    const { plan, unsupportedOps } = buildPlanPreview(ops);
    expect(unsupportedOps).toEqual([]);
    expect(plan.steps.map((s) => `${s.kind}:${s.node ?? ""}`)).toEqual([
      "stage_file:pve1",
      "reload:pve1",
      "stage_file:pve2",
      "reload:pve2",
    ]);
    // pve1's stage step realizes ops 0 and 2, in original order.
    expect(plan.steps[0]?.opIdx).toEqual([0, 2]);
    expect(plan.steps[2]?.opIdx).toEqual([1]);
  });

  it("puts sdn.apply last", () => {
    const ops: Op[] = [
      { op: "sdn.apply", params: {} },
      { op: "bridge.create", target: "bridge:pve1:vmbr1", params: {} },
    ];
    const { plan } = buildPlanPreview(ops);
    expect(plan.steps.map((s) => s.kind)).toEqual(["stage_file", "reload", "sdn_apply"]);
  });

  it("reports op families the apply engine cannot execute yet instead of planning them", () => {
    const ops: Op[] = [
      { op: "guest.nic.update", target: "guest-nic:pve1:200/net0", params: { bridgeOrVnet: "vmbr1" } },
      { op: "bridge.create", target: "bridge:pve1:vmbr1", params: {} },
    ];
    const { plan, unsupportedOps } = buildPlanPreview(ops);
    expect(unsupportedOps).toEqual(["guest.nic.update"]);
    expect(plan.steps.map((s) => s.kind)).toEqual(["stage_file", "reload"]);
  });

  it("returns an empty plan for an empty changeset", () => {
    expect(buildPlanPreview([]).plan.steps).toEqual([]);
  });
});
