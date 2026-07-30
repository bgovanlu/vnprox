import { describe, expect, it } from "vitest";
import type { TopologyEdge, TopologyNode } from "../api/types";
import { buildSwitchModel, switchCarriesVlan, type SwitchModel, type SwitchUplink } from "./switchModel";
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

describe("switchCarriesVlan — table cases (audit: bridge-own-badge parity + trunk ranges)", () => {
  function baseModel(overrides: Partial<SwitchModel> = {}): SwitchModel {
    return {
      ref: "bridge:pve1:vmbrX",
      node: "pve1",
      name: "vmbrX",
      kind: "bridge",
      status: "ok",
      badges: [],
      uplinks: [],
      vlans: [],
      accessPorts: [],
      vnets: [],
      ...overrides,
    };
  }

  const bondWithTrunkBadge: SwitchUplink = {
    ref: "bond:pve1:bond0",
    label: "bond0",
    kind: "bond",
    status: "ok",
    badges: ["vlans=10-20"],
    members: [],
  };

  const cases: [string, SwitchModel, number, boolean][] = [
    // Fix #1: the graph view's computeVlanMatch (projection.ts) treats a
    // bridge's OWN badge as a direct carrier via entityCarriesVlan — the
    // switch view must match that instead of only inspecting vlans/
    // accessPorts/vnets/uplink badges.
    ["bridge's own vid= badge, single VID match", baseModel({ badges: ["vid=15"] }), 15, true],
    ["bridge's own vid= badge, non-matching VID", baseModel({ badges: ["vid=15"] }), 16, false],
    ["bridge's own vlans= trunk-range badge, VID inside range", baseModel({ badges: ["vlans=10-20"] }), 15, true],
    ["bridge's own vlans= trunk-range badge, VID outside range", baseModel({ badges: ["vlans=10-20"] }), 25, false],
    // Trunk-range branch of switchCarriesVlan itself (an uplink's badge, not
    // the bridge's own) — now sourced from projection.ts's badgeCarriesVlan.
    ["uplink trunk badge vlans=10-20, VID inside range", baseModel({ uplinks: [bondWithTrunkBadge] }), 12, true],
    ["uplink trunk badge vlans=10-20, VID outside range", baseModel({ uplinks: [bondWithTrunkBadge] }), 99, false],
    ["no VLAN-carrying badge anywhere on the model", baseModel(), 15, false],
  ];

  it.each(cases)("%s", (_desc, model, vid, expected) => {
    expect(switchCarriesVlan(model, vid)).toBe(expected);
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

  it("marks a bond member inactive (standby/backup) when its enslaved-by edge has no 'active' badge", () => {
    const nodes = [
      node({ id: "bridge:pve1:vmbr8", kind: "bridge", layer: "l2", label: "vmbr8" }),
      node({ id: "bond:pve1:bond8", kind: "bond", layer: "l2", label: "bond8" }),
      node({ id: "physnic:pve1:eno8a", kind: "physnic", layer: "phys", label: "eno8a" }),
      node({ id: "physnic:pve1:eno8b", kind: "physnic", layer: "phys", label: "eno8b" }),
    ];
    const edges = [
      edge("bond:pve1:bond8", "bridge:pve1:vmbr8", "port-of"),
      edge("physnic:pve1:eno8a", "bond:pve1:bond8", "enslaved-by", ["active"]),
      // No "active" badge on this member's edge — it's the standby/backup slave.
      edge("physnic:pve1:eno8b", "bond:pve1:bond8", "enslaved-by"),
    ];
    const model = buildSwitchModel(nodes, edges);
    const sw = switchFor(model, "pve1");
    const bond = must(sw.uplinks[0], "bond uplink");
    const active = must(
      bond.members.find((m) => m.label === "eno8a"),
      "active member",
    );
    const standby = must(
      bond.members.find((m) => m.label === "eno8b"),
      "standby member",
    );
    expect(active.active).toBe(true);
    expect(standby.active).toBe(false);
  });

  it("falls back to the VNet's own vid/tag badge when its realizes edge carries no tag badge", () => {
    const nodes = [
      node({ id: "bridge:pve1:vmbr7", kind: "bridge", layer: "l2", label: "vmbr7" }),
      node({
        id: "sdn-vnet:cluster:vnet50",
        kind: "sdn-vnet",
        layer: "sdn",
        nodeGroup: "",
        label: "vnet50",
        badges: ["tag=50"],
      }),
    ];
    // Deliberately no tag/vid badge on the "realizes" edge itself, forcing
    // realizeTagOf.get(...) to miss and fall back to the VNet node's own
    // "tag=50" badge (switchModel.ts's `realizeTagOf.get(...) ?? vidFromBadges(n.badges)`).
    const edges = [edge("sdn-vnet:cluster:vnet50", "bridge:pve1:vmbr7", "realizes")];
    const model = buildSwitchModel(nodes, edges);
    const sw = switchFor(model, "pve1");
    expect(sw.vnets).toHaveLength(1);
    expect(must(sw.vnets[0], "vnet").tag).toBe(50);
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

// T-1907: a "phys-group:<node>" pill (internal/topology/collapse_physical.go)
// stands in for real NIC nodes throughout buildSwitchModel's generic
// byId-lookup rendering — no dedicated component was needed for it (see
// docs/features/topology.md §4), but it does need the two spots that
// otherwise assume a real "physnic"/bond kind: the free-ports scan (a pill
// with no synthesized connectivity edge must not silently disappear) and
// the isGroup/count flags NicPort/UplinkModule (SwitchFaceplate.tsx) use to
// render its "click to expand" affordance instead of a real port chip.
describe("buildSwitchModel — T-1907 phys-group pill", () => {
  it("renders a phys-group pill port-of a bridge as a one-member uplink flagged isGroup, consumed (not a free port)", () => {
    const nodes = [
      node({ id: "bridge:pve1:vmbr0", kind: "bridge", layer: "l2", label: "vmbr0" }),
      node({
        id: "phys-group:pve1",
        kind: "phys-group",
        layer: "phys",
        label: "10 NICs",
        collapsedCount: 10,
        badges: ["count=10"],
      }),
    ];
    const edges = [edge("phys-group:pve1", "bridge:pve1:vmbr0", "port-of", ["count=1"])];
    const model = buildSwitchModel(nodes, edges);
    const sw = switchFor(model, "pve1");
    expect(sw.uplinks).toHaveLength(1);
    const uplink = must(sw.uplinks[0], "uplink");
    expect(uplink.isGroup).toBe(true);
    expect(uplink.count).toBe(10);
    expect(uplink.members).toHaveLength(1);
    expect(must(uplink.members[0], "member").isGroup).toBe(true);
    expect(must(model.nodes[0], "group").freePorts).toEqual([]);
  });

  it("renders a phys-group pill enslaved-by a bond as one flagged bond member", () => {
    const nodes = [
      node({ id: "bridge:pve1:vmbr0", kind: "bridge", layer: "l2", label: "vmbr0" }),
      node({ id: "bond:pve1:bond0", kind: "bond", layer: "l2", label: "bond0" }),
      node({
        id: "phys-group:pve1",
        kind: "phys-group",
        layer: "phys",
        label: "10 NICs",
        collapsedCount: 10,
        badges: ["count=10"],
      }),
    ];
    const edges = [
      edge("bond:pve1:bond0", "bridge:pve1:vmbr0", "port-of"),
      edge("phys-group:pve1", "bond:pve1:bond0", "enslaved-by", ["count=2"]),
    ];
    const model = buildSwitchModel(nodes, edges);
    const sw = switchFor(model, "pve1");
    const bond = must(sw.uplinks[0], "bond uplink");
    expect(bond.isGroup).toBeFalsy();
    const member = must(bond.members[0], "group member");
    expect(member.ref).toBe("phys-group:pve1");
    expect(member.isGroup).toBe(true);
    expect(member.count).toBe(10);
  });

  it("lists a fully-unattached phys-group pill (no surviving connectivity edge) as a free port, flagged isGroup", () => {
    const nodes = [
      node({
        id: "phys-group:pve1",
        kind: "phys-group",
        layer: "phys",
        label: "9 NICs",
        collapsedCount: 9,
        badges: ["count=9"],
      }),
    ];
    const model = buildSwitchModel(nodes, []);
    const group = must(model.nodes[0], "group");
    expect(group.switches).toEqual([]);
    expect(group.freePorts).toHaveLength(1);
    const port = must(group.freePorts[0], "free port");
    expect(port.isGroup).toBe(true);
    expect(port.count).toBe(9);
    expect(port.ref).toBe("phys-group:pve1");
  });

  it("never flags a real NIC/bond as isGroup", () => {
    const nodes = [
      node({ id: "physnic:pve1:eno1", kind: "physnic", layer: "phys", label: "eno1" }),
      node({ id: "bond:pve1:bond1", kind: "bond", layer: "l2", label: "bond1" }),
    ];
    const model = buildSwitchModel(nodes, []);
    const group = must(model.nodes[0], "group");
    for (const p of group.freePorts) {
      expect(p.isGroup).toBeFalsy();
    }
  });
});
