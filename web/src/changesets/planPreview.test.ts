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

  it("plans an SDN wizard changeset as executable sdn_stage steps (regression: was falsely flagged un-appliable)", () => {
    const ops: Op[] = [
      { op: "sdn.zone.create", target: "sdn-zone::homelab", params: { type: "simple", nodes: ["pvecube"] } },
      { op: "sdn.vnet.create", target: "sdn-vnet::homelab/vnet1", params: { zone: "homelab", vlanAware: false } },
      {
        op: "sdn.subnet.create",
        target: "sdn-subnet::10.10.10.0/24",
        params: { vnet: "homelab/vnet1", cidr: "10.10.10.0/24", gateway: "10.10.10.1", snat: false },
      },
      { op: "sdn.apply", params: {} },
    ];
    const { plan, unsupportedOps } = buildPlanPreview(ops);
    // No warning — these ops execute end to end (T-402).
    expect(unsupportedOps).toEqual([]);
    expect(plan.steps.map((s) => s.kind)).toEqual(["sdn_stage", "sdn_stage", "sdn_stage", "sdn_apply"]);
  });

  it("orders mixed families like BuildPlan: SDN-stage, IPAM, per-node stage/reload, firewall, sdn.apply last", () => {
    const ops: Op[] = [
      { op: "ipam.alloc.create", target: "sdn-subnet::10.10.10.0/24", params: { cidr: "10.10.10.10/32" } },
      { op: "bridge.create", target: "bridge:pve1:vmbr1", params: {} },
      { op: "sdn.zone.create", target: "sdn-zone::z", params: { type: "simple" } },
      { op: "fw.rule.create", target: "fw:pve1:", params: { action: "ACCEPT", type: "in" } },
      { op: "sdn.apply", params: {} },
    ];
    const { plan, unsupportedOps } = buildPlanPreview(ops);
    expect(unsupportedOps).toEqual([]);
    expect(plan.steps.map((s) => s.kind)).toEqual([
      "sdn_stage",
      "ipam_alloc",
      "stage_file",
      "reload",
      "fw_apply",
      "fw_verify",
      "sdn_apply",
    ]);
  });

  it("returns an empty plan for an empty changeset", () => {
    expect(buildPlanPreview([]).plan.steps).toEqual([]);
  });

  // T-1402: wg.* (T-1401) is executable — regression against the same
  // "falsely flagged un-appliable" class of bug the SDN wizard test above
  // guards (this preview lagged BuildPlan's own wgOpTypes support until
  // this task added it).
  it("plans wg.* ops as executable wg_apply steps (regression: was falsely flagged un-appliable)", () => {
    const ops: Op[] = [
      { op: "wg.tunnel.create", target: "wg-tunnel:pve1:t1", params: { ifName: "wg0" } },
      { op: "wg.peer.add", target: "wg-peer:pve1:t1/PEERkey=", params: { publicKey: "PEERkey=", external: true } },
    ];
    const { plan, unsupportedOps } = buildPlanPreview(ops);
    expect(unsupportedOps).toEqual([]);
    expect(plan.steps.map((s) => s.kind)).toEqual(["wg_apply", "wg_apply"]);
    expect(plan.steps.map((s) => s.node)).toEqual(["pve1", "pve1"]);
  });

  it("orders WireGuard steps like BuildPlan: after per-node stage/reload, before firewall/sdn.apply", () => {
    const ops: Op[] = [
      { op: "bridge.create", target: "bridge:pve1:vmbr1", params: {} },
      { op: "wg.tunnel.create", target: "wg-tunnel:pve1:t1", params: { ifName: "wg0", carrier: "vmbr1" } },
      { op: "fw.rule.create", target: "fw-ruleset:pve1:node", params: { action: "ACCEPT", direction: "in" } },
      { op: "sdn.apply", params: {} },
    ];
    const { plan, unsupportedOps } = buildPlanPreview(ops);
    expect(unsupportedOps).toEqual([]);
    expect(plan.steps.map((s) => s.kind)).toEqual(["stage_file", "reload", "wg_apply", "fw_apply", "fw_verify", "sdn_apply"]);
  });
});
