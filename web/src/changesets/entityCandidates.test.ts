import { describe, expect, it } from "vitest";
import type { TopologyResponse } from "../api/types";
import {
  attachedGuestNics,
  bondSlaveCandidates,
  bridgePortCandidates,
  enslavementMap,
  reattachTargets,
  vlanParentCandidates,
} from "./entityCandidates";

// A minimal slice of the three-node-vlan pvemock fixture's shape (one
// node): bond0 (eno1+eno2), vmbr0 (VLAN-aware, port bond0), vmbr0.20, and a
// spare unenslaved eno3 to exercise the "no conflict" candidate case.
const topology: TopologyResponse = {
  layers: ["phys", "l2"],
  generatedAt: 1,
  nodes: [
    { id: "physnic:pve1:eno1", kind: "physnic", label: "eno1", layer: "phys", nodeGroup: "pve1", status: "ok", badges: [] },
    { id: "physnic:pve1:eno2", kind: "physnic", label: "eno2", layer: "phys", nodeGroup: "pve1", status: "ok", badges: [] },
    { id: "physnic:pve1:eno3", kind: "physnic", label: "eno3", layer: "phys", nodeGroup: "pve1", status: "down", badges: [] },
    { id: "bond:pve1:bond0", kind: "bond", label: "bond0", layer: "l2", nodeGroup: "pve1", status: "ok", badges: [] },
    { id: "bridge:pve1:vmbr0", kind: "bridge", label: "vmbr0", layer: "l2", nodeGroup: "pve1", status: "ok", badges: [] },
    { id: "bridge:pve1:vmbr1", kind: "bridge", label: "vmbr1", layer: "l2", nodeGroup: "pve1", status: "ok", badges: [] },
    { id: "vlan:pve1:vmbr0.20", kind: "vlan", label: "vmbr0.20", layer: "l2", nodeGroup: "pve1", status: "ok", badges: [] },
    { id: "sdn-vnet::vnet100", kind: "sdn-vnet", label: "vnet100", layer: "sdn", nodeGroup: "", status: "ok", badges: [] },
  ],
  edges: [
    { from: "physnic:pve1:eno1", to: "bond:pve1:bond0", kind: "enslaved-by", status: "ok", badges: [] },
    { from: "physnic:pve1:eno2", to: "bond:pve1:bond0", kind: "enslaved-by", status: "ok", badges: [] },
    { from: "bond:pve1:bond0", to: "bridge:pve1:vmbr0", kind: "port-of", status: "ok", badges: [] },
    { from: "vlan:pve1:vmbr0.20", to: "bridge:pve1:vmbr0", kind: "tagged-on", status: "ok", badges: [] },
    { from: "guest-nic:pve1:200/net0", to: "bridge:pve1:vmbr0", kind: "attached-to", status: "ok", badges: [] },
    { from: "guest-nic:pve1:201/net0", to: "bridge:pve1:vmbr0", kind: "attached-to", status: "ok", badges: [] },
  ],
};

describe("enslavementMap", () => {
  it("maps a slave/port/tagged ref to the name of what it's already used by", () => {
    const m = enslavementMap(topology);
    expect(m.get("physnic:pve1:eno1")).toBe("bond0");
    expect(m.get("physnic:pve1:eno2")).toBe("bond0");
    expect(m.get("bond:pve1:bond0")).toBe("vmbr0");
    expect(m.get("physnic:pve1:eno3")).toBeUndefined();
  });
});

describe("bridgePortCandidates", () => {
  it("lists physnics/bonds/vlans on the node, flagging already-enslaved ones, excluding the bridge itself", () => {
    const candidates = bridgePortCandidates(topology, "pve1", "bridge:pve1:vmbr0");
    const byName = new Map(candidates.map((c) => [c.name, c]));
    expect(byName.get("eno1")?.alreadyEnslaved).toBe("bond0");
    expect(byName.get("eno3")?.alreadyEnslaved).toBeUndefined();
    expect(byName.get("bond0")?.alreadyEnslaved).toBe("vmbr0");
    expect(candidates.some((c) => c.ref === "bridge:pve1:vmbr0")).toBe(false);
  });
});

describe("bondSlaveCandidates", () => {
  it("only lists physnics on the node", () => {
    const candidates = bondSlaveCandidates(topology, "pve1");
    expect(candidates.map((c) => c.name).sort()).toEqual(["eno1", "eno2", "eno3"]);
  });
});

describe("vlanParentCandidates", () => {
  it("lists physnics, bonds, and bridges (not other VLANs) on the node", () => {
    const parents = vlanParentCandidates(topology, "pve1");
    expect(parents.sort()).toEqual(["bond0", "eno1", "eno2", "eno3", "vmbr0", "vmbr1"]);
  });
});

describe("reattachTargets", () => {
  it("lists other bridges on the node plus cluster-scoped VNets, excluding the bridge being deleted", () => {
    const targets = reattachTargets(topology, "pve1", "bridge:pve1:vmbr0");
    expect(targets.sort()).toEqual(["vmbr1", "vnet100"]);
  });
});

describe("attachedGuestNics", () => {
  it("lists guest NICs attached-to the given bridge", () => {
    const nodesById = new Map(topology.nodes.map((n) => [n.id, n]));
    const attached = attachedGuestNics(topology.edges, nodesById, "bridge:pve1:vmbr0");
    expect(attached.map((a) => a.ref).sort()).toEqual(["guest-nic:pve1:200/net0", "guest-nic:pve1:201/net0"]);
  });

  it("returns an empty list for a bridge with no attached guests", () => {
    const nodesById = new Map(topology.nodes.map((n) => [n.id, n]));
    expect(attachedGuestNics(topology.edges, nodesById, "bridge:pve1:vmbr1")).toEqual([]);
  });
});
