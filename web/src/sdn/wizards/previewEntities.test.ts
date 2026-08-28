// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import {
  buildEvpnPreview,
  buildQinqPreview,
  buildSimplePreview,
  buildVlanPreview,
  buildVxlanPreview,
  type EvpnZoneParams,
  type QinqZoneParams,
  type SimpleZoneParams,
  type VlanZoneParams,
  type VxlanZoneParams,
} from "./previewEntities";

describe("buildSimplePreview", () => {
  const base: SimpleZoneParams = {
    zoneId: "homelab",
    zoneType: "simple",
    memberNodes: ["pve1", "pve2"],
    vnetId: "vnet1",
    vnetAlias: "",
    subnetCidr: "",
    bridgeName: "",
  };

  it("is empty until zoneId + a member node are set", () => {
    expect(buildSimplePreview({ ...base, zoneId: "", memberNodes: [] })).toEqual({ nodes: [], edges: [] });
  });

  it("renders zone -> vnet -> one planned bridge per member node", () => {
    const g = buildSimplePreview(base);
    expect(g.nodes.filter((n) => n.kind === "sdn-zone")).toHaveLength(1);
    expect(g.nodes.filter((n) => n.kind === "sdn-vnet")).toHaveLength(1);
    const bridges = g.nodes.filter((n) => n.kind === "bridge");
    expect(bridges).toHaveLength(2);
    expect(bridges.every((n) => n.badges.includes("planned"))).toBe(true);
    expect(new Set(bridges.map((n) => n.nodeGroup))).toEqual(new Set(["pve1", "pve2"]));
    // Every member bridge is "realized" by the vnet.
    const vnetId = g.nodes.find((n) => n.kind === "sdn-vnet")?.id;
    expect(g.edges.filter((e) => e.kind === "realizes" && e.from === vnetId)).toHaveLength(2);
  });

  it("includes a subnet node + edge only when a CIDR is set", () => {
    expect(buildSimplePreview(base).nodes.some((n) => n.kind === "sdn-subnet")).toBe(false);
    const withSubnet = buildSimplePreview({ ...base, subnetCidr: "10.50.0.0/24" });
    expect(withSubnet.nodes.some((n) => n.kind === "sdn-subnet" && n.label === "10.50.0.0/24")).toBe(true);
    expect(withSubnet.edges.some((e) => e.kind === "subnet-of")).toBe(true);
  });
});

describe("buildVlanPreview", () => {
  const base: VlanZoneParams = {
    zoneId: "vlanz2",
    zoneType: "vlan",
    memberNodes: ["pve1", "pve2", "pve3"],
    vnetId: "vnet300",
    vnetAlias: "",
    subnetCidr: "",
    bridgeName: "vmbr0",
    vid: 300,
  };

  it("requires an existing bridge name to render anything", () => {
    expect(buildVlanPreview({ ...base, bridgeName: "" })).toEqual({ nodes: [], edges: [] });
  });

  it("renders the existing bridge (not planned) per member node, badged with the vid", () => {
    const g = buildVlanPreview(base);
    const zone = g.nodes.find((n) => n.kind === "sdn-zone");
    expect(zone?.badges).toContain("vid=300");
    const bridges = g.nodes.filter((n) => n.kind === "bridge");
    expect(bridges).toHaveLength(3);
    expect(bridges.every((n) => n.label === "vmbr0")).toBe(true);
    expect(bridges.every((n) => !n.badges.includes("planned"))).toBe(true);
    expect(bridges.every((n) => n.status === "ok")).toBe(true);
  });
});

describe("buildQinqPreview", () => {
  const base: QinqZoneParams = {
    zoneId: "qinqz",
    zoneType: "qinq",
    memberNodes: ["pve1"],
    vnetId: "vnet1",
    vnetAlias: "",
    subnetCidr: "",
    bridgeName: "vmbr0",
    serviceVid: 100,
    customerVid: 42,
  };

  it("illustrates the double tag as two chained vlan nodes into the vnet", () => {
    const g = buildQinqPreview(base);
    const service = g.nodes.find((n) => n.label === "Service tag 100");
    const customer = g.nodes.find((n) => n.label === "Customer tag 42");
    expect(service).toBeDefined();
    expect(customer).toBeDefined();
    expect(service?.kind).toBe("vlan");
    expect(customer?.kind).toBe("vlan");
    // customer wraps inside service; vnet carries the customer tag.
    expect(g.edges.some((e) => e.from === customer?.id && e.to === service?.id)).toBe(true);
    const vnet = g.nodes.find((n) => n.kind === "sdn-vnet");
    expect(g.edges.some((e) => e.from === vnet?.id && e.to === customer?.id)).toBe(true);
    const zone = g.nodes.find((n) => n.kind === "sdn-zone");
    expect(zone?.badges).toEqual(expect.arrayContaining(["s-vid=100", "c-vid=42"]));
  });
});

describe("buildVxlanPreview", () => {
  const base: VxlanZoneParams = {
    zoneId: "vxlanz",
    zoneType: "vxlan",
    memberNodes: ["pve1", "pve2", "pve3"],
    vnetId: "vnet1",
    vnetAlias: "",
    subnetCidr: "",
    mtu: 0,
    vni: 0,
    peers: { pve1: "10.10.0.11", pve2: "10.10.0.12", pve3: "10.10.0.13" },
  };

  it("shows the derived safe MTU on the zone badge when mtu is left blank", () => {
    const g = buildVxlanPreview(base);
    const zone = g.nodes.find((n) => n.kind === "sdn-zone" && n.badges.includes("mtu=1450"));
    expect(zone).toBeDefined();
    expect(zone?.badges).not.toContain("mtu-too-large");
  });

  it("flags an over-large mtu on the zone badge (T-402 AC3 scenario)", () => {
    const g = buildVxlanPreview({ ...base, mtu: 1500 });
    const zone = g.nodes.find((n) => n.kind === "sdn-zone" && n.badges.includes("mtu=1500"));
    expect(zone?.badges).toContain("mtu-too-large");
  });

  it("full-meshes the VTEP peers (3 peers -> 3 edges)", () => {
    const g = buildVxlanPreview(base);
    const vtepNodes = g.nodes.filter((n) => n.badges.includes("vtep"));
    expect(vtepNodes).toHaveLength(3);
    const meshEdges = g.edges.filter((e) => e.kind === "vtep-peer");
    expect(meshEdges).toHaveLength(3); // C(3,2) = 3
  });

  it("skips a member node with no suggested/entered peer address", () => {
    const g = buildVxlanPreview({ ...base, peers: { pve1: "10.10.0.11", pve2: undefined, pve3: "10.10.0.13" } });
    expect(g.nodes.filter((n) => n.badges.includes("vtep"))).toHaveLength(2);
    expect(g.edges.filter((e) => e.kind === "vtep-peer")).toHaveLength(1);
  });
});

describe("buildEvpnPreview", () => {
  const base: EvpnZoneParams = {
    zoneId: "evpnz",
    zoneType: "evpn",
    memberNodes: ["pve1", "pve2", "pve3"],
    vnetId: "vnet1",
    vnetAlias: "",
    subnetCidr: "",
    controller: "evpn1",
    asn: 65000,
    peerAddresses: ["10.10.0.11", "10.10.0.12", "10.10.0.13"],
    exitNodes: ["pve1"],
    vrfVxlan: 0,
    vni: 100,
  };

  it("renders the correct BGP session graph for a 3-peer input — T-403 AC3", () => {
    const g = buildEvpnPreview(base);
    const controller = g.nodes.find((n) => n.badges.includes("controller"));
    expect(controller).toBeDefined();
    expect(controller?.label).toBe("evpn1 (AS65000)");

    const peerNodes = g.nodes.filter((n) => n.id.startsWith("wizard-preview:bgp-peer:"));
    expect(peerNodes).toHaveLength(3);
    expect(new Set(peerNodes.map((n) => n.label))).toEqual(new Set(["10.10.0.11", "10.10.0.12", "10.10.0.13"]));

    const sessionEdges = g.edges.filter((e) => e.kind === "bgp-session");
    expect(sessionEdges).toHaveLength(3);
    expect(sessionEdges.every((e) => e.from === controller?.id)).toBe(true);
    expect(new Set(sessionEdges.map((e) => e.to))).toEqual(new Set(peerNodes.map((n) => n.id)));
  });

  it("badges the first exit node as primary, the rest as exit", () => {
    const g = buildEvpnPreview({ ...base, exitNodes: ["pve1", "pve2"] });
    const peerNodes = g.nodes.filter((n) => n.id.startsWith("wizard-preview:bgp-peer:"));
    expect(peerNodes[0]?.badges).toEqual(expect.arrayContaining(["exit", "primary"]));
    expect(peerNodes[1]?.badges).toEqual(expect.arrayContaining(["exit"]));
    expect(peerNodes[1]?.badges).not.toContain("primary");
  });

  it("labels each member bridge, marking exit nodes distinctly", () => {
    const g = buildEvpnPreview(base);
    const bridges = g.nodes.filter((n) => n.kind === "bridge");
    expect(bridges).toHaveLength(3);
    expect(bridges.find((n) => n.nodeGroup === "pve1")?.label).toBe("pve1 (exit)");
    expect(bridges.find((n) => n.nodeGroup === "pve2")?.label).toBe("pve2");
  });
});
