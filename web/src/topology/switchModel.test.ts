import { describe, expect, it } from "vitest";
import type { TopologyEdge, TopologyNode } from "../api/types";
import { buildSwitchModel, switchCarriesVlan, type SwitchModel } from "./switchModel";
import threeNodeVlan from "./__fixtures__/three-node-vlan-topology.json";
import scaleLab from "./__fixtures__/scale-lab-topology.json";

function node(partial: Partial<TopologyNode> & Pick<TopologyNode, "id" | "kind" | "layer">): TopologyNode {
  return { label: partial.id, nodeGroup: "pve1", status: "ok", badges: [], ...partial };
}
function edge(from: string, to: string, kind: string, badges: string[] = []): TopologyEdge {
  return { from, to, kind, status: "ok", badges };
}

/** Test helper: assert-and-narrow (noUncheckedIndexedAccess makes every index
 * `T | undefined`; these throw with a clear message rather than littering the
 * assertions with optional chaining). */
function must<T>(v: T | undefined, what: string): T {
  if (v === undefined) throw new Error(`expected ${what} to be defined`);
  return v;
}
function switchFor(model: ReturnType<typeof buildSwitchModel>, nodeName: string): SwitchModel {
  const group = must(
    model.nodes.find((n) => n.node === nodeName),
    `node group ${nodeName}`,
  );
  return must(group.switches[0], `first switch on ${nodeName}`);
}

describe("buildSwitchModel — three-node-vlan fixture", () => {
  const model = buildSwitchModel(threeNodeVlan.nodes as TopologyNode[], threeNodeVlan.edges as TopologyEdge[]);

  it("produces one node group per cluster node, each with its vmbr0 switch", () => {
    expect(model.nodes.map((n) => n.node)).toEqual(["pve1", "pve2", "pve3"]);
    for (const group of model.nodes) {
      expect(group.switches.map((s) => s.name)).toEqual(["vmbr0"]);
    }
  });

  it("assembles pve1's vmbr0 uplink as bond0 expanded to its active member NICs with LLDP neighbors", () => {
    const vmbr0 = switchFor(model, "pve1");
    expect(vmbr0.uplinks).toHaveLength(1);
    const bond = must(vmbr0.uplinks[0], "bond uplink");
    expect(bond.label).toBe("bond0");
    expect(bond.kind).toBe("bond");
    expect(bond.members.map((m) => m.label)).toEqual(["eno1", "eno2"]);
    expect(bond.members.every((m) => m.active)).toBe(true);
    expect(bond.members.map((m) => m.neighbor?.label)).toEqual(["sw-core-01", "sw-core-02"]);
    expect(must(bond.members[0], "first member").neighbor?.port).toBe("Te1/0/1");
  });

  it("lists the guest access port with its VLAN tag and recovered VMID", () => {
    const vmbr0 = switchFor(model, "pve1");
    expect(vmbr0.accessPorts).toHaveLength(1);
    const port = must(vmbr0.accessPorts[0], "access port");
    expect(port.label).toBe("app01/net0");
    expect(port.vid).toBe(100);
    expect(port.vmid).toBe(200);
    expect(port.isGroup).toBe(false);
  });

  it("lists the VLAN sub-interface and both realized VNets with their tags", () => {
    const vmbr0 = switchFor(model, "pve1");
    expect(vmbr0.vlans.map((v) => v.vid)).toEqual([20]);
    expect(vmbr0.vnets.map((v) => ({ label: v.label, tag: v.tag }))).toEqual([
      { label: "vnet100 (app-tier)", tag: 100 },
      { label: "vnet200 (db-tier)", tag: 200 },
    ]);
  });

  it("marks a NIC consumed by its bond — no NIC appears as a free port", () => {
    for (const group of model.nodes) {
      expect(group.freePorts).toEqual([]);
    }
  });

  it("switchCarriesVlan matches access-port, VLAN sub-if, and VNet tags", () => {
    const vmbr0 = switchFor(model, "pve1");
    expect(switchCarriesVlan(vmbr0, 100)).toBe(true); // access port + vnet
    expect(switchCarriesVlan(vmbr0, 20)).toBe(true); // vlan sub-if
    expect(switchCarriesVlan(vmbr0, 999)).toBe(false);
  });
});

describe("buildSwitchModel — collapsed guests (scale-lab fixture)", () => {
  it("renders a guest-group pill as a single '+N' access port carrying its count", () => {
    const model = buildSwitchModel(scaleLab.nodes as TopologyNode[], scaleLab.edges as TopologyEdge[]);
    const allSwitches = model.nodes.flatMap((n) => n.switches);
    const groupPorts = allSwitches.flatMap((s) => s.accessPorts).filter((p) => p.isGroup);
    expect(groupPorts.length).toBeGreaterThan(0);
    for (const p of groupPorts) {
      expect(p.count ?? 0).toBeGreaterThan(0);
    }
    // Every switch that has a group pill lists it last.
    for (const sw of allSwitches) {
      const idx = sw.accessPorts.findIndex((p) => p.isGroup);
      if (idx !== -1) expect(idx).toBe(sw.accessPorts.length - 1);
    }
  });
});

describe("buildSwitchModel — synthetic edge cases", () => {
  it("surfaces a bare NIC directly bridged as a single-member uplink", () => {
    const nodes = [
      node({ id: "bridge:pve1:vmbr9", kind: "bridge", layer: "l2", label: "vmbr9" }),
      node({ id: "physnic:pve1:eno9", kind: "physnic", layer: "phys", label: "eno9" }),
    ];
    const edges = [edge("physnic:pve1:eno9", "bridge:pve1:vmbr9", "port-of")];
    const model = buildSwitchModel(nodes, edges);
    const sw = switchFor(model, "pve1");
    expect(sw.uplinks).toHaveLength(1);
    expect(must(sw.uplinks[0], "uplink").kind).toBe("physnic");
    expect(must(sw.uplinks[0], "uplink").members.map((m) => m.label)).toEqual(["eno9"]);
    expect(must(model.nodes[0], "group").freePorts).toEqual([]); // consumed by the bridge
  });

  it("lists an unattached NIC/bond as a free port on its node", () => {
    const nodes = [
      node({ id: "physnic:pve1:eno7", kind: "physnic", layer: "phys", label: "eno7" }),
      node({ id: "bond:pve1:bond7", kind: "bond", layer: "l2", label: "bond7" }),
    ];
    const model = buildSwitchModel(nodes, []);
    const group = must(model.nodes[0], "group");
    expect(group.node).toBe("pve1");
    expect(group.switches).toEqual([]);
    expect(group.freePorts.map((p) => p.label).sort()).toEqual(["bond7", "eno7"]);
  });

  it("handles ovs-bridge/ovs-bond kinds the same as their linux counterparts", () => {
    const nodes = [
      node({ id: "ovs-bridge:pve1:vmbr0", kind: "ovs-bridge", layer: "l2", label: "vmbr0" }),
      node({ id: "ovs-bond:pve1:bond0", kind: "ovs-bond", layer: "l2", label: "bond0" }),
      node({ id: "physnic:pve1:eno1", kind: "physnic", layer: "phys", label: "eno1" }),
    ];
    const edges = [
      edge("ovs-bond:pve1:bond0", "ovs-bridge:pve1:vmbr0", "port-of"),
      edge("physnic:pve1:eno1", "ovs-bond:pve1:bond0", "enslaved-by", ["active"]),
    ];
    const model = buildSwitchModel(nodes, edges);
    const sw = switchFor(model, "pve1");
    expect(sw.kind).toBe("ovs-bridge");
    expect(must(sw.uplinks[0], "uplink").kind).toBe("ovs-bond");
    expect(must(sw.uplinks[0], "uplink").members.map((m) => m.label)).toEqual(["eno1"]);
    expect(must(model.nodes[0], "group").freePorts).toEqual([]);
  });
});
