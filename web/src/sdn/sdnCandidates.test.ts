import { describe, expect, it } from "vitest";
import { vnetReattachTargets } from "./sdnCandidates";

describe("vnetReattachTargets", () => {
  const nodes = [
    { id: "sdn-vnet::zone1/vnet100", kind: "sdn-vnet" },
    { id: "sdn-vnet::zone1/vnet200", kind: "sdn-vnet" },
    { id: "sdn-vnet::zone2/vnet300", kind: "sdn-vnet" },
    { id: "bridge:pve1:vmbr0", kind: "bridge" },
  ];

  it("returns every other cluster vnet's bare id, excluding the one being deleted", () => {
    expect(vnetReattachTargets(nodes, "zone1/vnet100")).toEqual(["vnet200", "vnet300"]);
  });

  it("excludes non-vnet topology nodes (e.g. bridges)", () => {
    expect(vnetReattachTargets(nodes, "zone1/vnet100")).not.toContain("vmbr0");
  });

  it("returns an empty list when the only vnet is the one being deleted", () => {
    const single = [{ id: "sdn-vnet::zone1/vnet100", kind: "sdn-vnet" }];
    expect(vnetReattachTargets(single, "zone1/vnet100")).toEqual([]);
  });

  it("handles a vnet id with no zone prefix defensively", () => {
    const flat = [{ id: "sdn-vnet::vnet100", kind: "sdn-vnet" }];
    expect(vnetReattachTargets(flat, "zone1/vnet200")).toEqual(["vnet100"]);
  });
});
