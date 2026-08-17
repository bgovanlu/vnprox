import { describe, expect, it } from "vitest";
import {
  buildBondCreateOp,
  buildBondUpdateOp,
  buildBridgeCreateOp,
  buildBridgeDeleteOp,
  buildBridgePortAddOp,
  buildBridgeUpdateOp,
  buildBulkGuestNicOps,
  buildGuestNicUpdateOp,
  buildGuestReattachOps,
  buildIfaceUpdateOp,
  buildSdnApplyOp,
  buildSdnFabricCreateOp,
  buildSdnFabricDeleteOp,
  buildSdnFabricUpdateOp,
  buildSdnSubnetCreateOp,
  buildSdnSubnetDeleteOp,
  buildSdnSubnetUpdateOp,
  buildSdnVnetCreateOp,
  buildSdnVnetDeleteOp,
  buildSdnVnetUpdateOp,
  buildSdnZoneCreateOp,
  buildSdnZoneDeleteOp,
  buildSdnZoneUpdateOp,
  buildVlanCreateOp,
  buildVlanUpdateOp,
  type BondFormValues,
  type BridgeFormValues,
  type IfaceFormValues,
  type SdnFabricFormValues,
  type SdnSubnetFormValues,
  type SdnVnetFormValues,
  type SdnZoneFormValues,
  type VlanFormValues,
} from "./opBuilders";

const bridgeForm: BridgeFormValues = {
  ports: ["bond0"],
  vlanAware: true,
  vids: [{ low: 10, high: 30 }],
  addresses: ["10.10.0.11/24"],
  gateway: "10.10.0.1",
  mtu: 1500,
  stp: false,
  comments: "mgmt",
};

describe("bridge op builders", () => {
  it("bridge.create carries every create-time field, including ports (create-only)", () => {
    const op = buildBridgeCreateOp("bridge:pve1:vmbr1", bridgeForm);
    expect(op).toEqual({
      op: "bridge.create",
      target: "bridge:pve1:vmbr1",
      params: {
        ports: ["bond0"],
        vlanAware: true,
        vids: [{ low: 10, high: 30 }],
        addresses: ["10.10.0.11/24"],
        gateway: "10.10.0.1",
        mtu: 1500,
        stp: false,
        comments: "mgmt",
      },
    });
  });

  it("bridge.update is a diff against the initial values and never carries ports", () => {
    const changed: BridgeFormValues = { ...bridgeForm, mtu: 9000, ports: ["bond0", "bond1"] };
    const op = buildBridgeUpdateOp("bridge:pve1:vmbr1", bridgeForm, changed);
    expect(op.op).toBe("bridge.update");
    expect(op.params).toEqual({ mtu: 9000 });
    expect(op.params).not.toHaveProperty("ports");
  });

  it("bridge.update omits every field that didn't change", () => {
    const op = buildBridgeUpdateOp("bridge:pve1:vmbr1", bridgeForm, bridgeForm);
    expect(op.params).toEqual({});
  });

  it("bridge.delete and bridge.port.add carry only their own params", () => {
    expect(buildBridgeDeleteOp("bridge:pve1:vmbr1")).toEqual({ op: "bridge.delete", target: "bridge:pve1:vmbr1", params: {} });
    expect(buildBridgePortAddOp("bridge:pve1:vmbr1", "eno3")).toEqual({
      op: "bridge.port.add",
      target: "bridge:pve1:vmbr1",
      params: { port: "eno3" },
    });
  });

  it("buildGuestReattachOps produces one guest.nic.update per ref, sharing the new target", () => {
    const ops = buildGuestReattachOps(["guest-nic:pve1:200/net0", "guest-nic:pve2:201/net0"], "vmbr2");
    expect(ops).toEqual([
      { op: "guest.nic.update", target: "guest-nic:pve1:200/net0", params: { bridgeOrVnet: "vmbr2" } },
      { op: "guest.nic.update", target: "guest-nic:pve2:201/net0", params: { bridgeOrVnet: "vmbr2" } },
    ]);
  });
});

describe("bond op builders", () => {
  const bondForm: BondFormValues = {
    mode: "802.3ad",
    slaves: ["eno1", "eno2"],
    lacpRate: "slow",
    xmitHashPolicy: "layer2",
    miimon: 100,
    mtu: 1500,
    comments: "",
  };

  it("bond.create carries mode + slaves", () => {
    const op = buildBondCreateOp("bond:pve1:bond0", bondForm);
    expect(op.op).toBe("bond.create");
    expect(op.params).toMatchObject({ mode: "802.3ad", slaves: ["eno1", "eno2"] });
  });

  it("bond.update only carries the changed slave list", () => {
    const changed: BondFormValues = { ...bondForm, slaves: ["eno1", "eno2", "eno3"] };
    const op = buildBondUpdateOp("bond:pve1:bond0", bondForm, changed);
    expect(op.params).toEqual({ slaves: ["eno1", "eno2", "eno3"] });
  });

  it("bond.create on an ovs-bond target carries bridge", () => {
    const ovsForm: BondFormValues = { ...bondForm, mode: "active-backup", ovsBridge: "vmbr1" };
    const op = buildBondCreateOp("ovs-bond:pve1:bond0", ovsForm);
    expect(op.params).toMatchObject({ mode: "active-backup", bridge: "vmbr1" });
  });

  it("bond.create on a plain bond target never sends bridge, even if ovsBridge is set", () => {
    const formWithStrayBridge: BondFormValues = { ...bondForm, ovsBridge: "vmbr1" };
    const op = buildBondCreateOp("bond:pve1:bond0", formWithStrayBridge);
    expect((op.params as { bridge?: string }).bridge).toBeUndefined();
  });
});

describe("vlan op builders", () => {
  const vlanForm: VlanFormValues = { parent: "vmbr0", vid: 30, addresses: ["10.10.30.11/24"], mtu: 1500 };

  it("vlan.create carries parent/vid/addresses/mtu", () => {
    const op = buildVlanCreateOp("vlan:pve1:vmbr0.30", vlanForm);
    expect(op.params).toEqual({ parent: "vmbr0", vid: 30, addresses: ["10.10.30.11/24"], mtu: 1500 });
  });

  it("vlan.update only diffs addresses/mtu (parent/vid are not editable post-create)", () => {
    const changed: VlanFormValues = { ...vlanForm, mtu: 9000 };
    const op = buildVlanUpdateOp("vlan:pve1:vmbr0.30", vlanForm, changed);
    expect(op.params).toEqual({ mtu: 9000 });
  });

  it("vlan.create for an ovs int port carries ovs + tag", () => {
    const ovsForm: VlanFormValues = { parent: "vmbr1", vid: 20, addresses: [], mtu: 0, ovs: true };
    const op = buildVlanCreateOp("vlan:pve1:vlan20", ovsForm);
    expect(op.params).toEqual({ parent: "vmbr1", vid: 20, ovs: true });
  });

  it("vlan.create for an ovs int port carries trunks only when ovs is true", () => {
    const trunks = [{ low: 10, high: 20 }];
    const ovsOp = buildVlanCreateOp("vlan:pve1:vlan-trunk", { parent: "vmbr1", vid: 0, addresses: [], mtu: 0, ovs: true, trunks });
    expect(ovsOp.params).toMatchObject({ trunks });

    const plainOp = buildVlanCreateOp("vlan:pve1:vmbr0.20", { ...vlanForm, trunks });
    const plainParams = plainOp.params as { trunks?: unknown; ovs?: unknown };
    expect(plainParams.trunks).toBeUndefined();
    expect(plainParams.ovs).toBeUndefined();
  });
});

describe("iface op builder", () => {
  const ifaceForm: IfaceFormValues = { mtu: 1500, comments: "", addresses: [], gateway: "", autostart: true };

  it("iface.update only carries what changed", () => {
    const changed: IfaceFormValues = { ...ifaceForm, mtu: 9000, autostart: false };
    const op = buildIfaceUpdateOp("physnic:pve1:eno3", ifaceForm, changed);
    expect(op.params).toEqual({ mtu: 9000, autostart: false });
  });
});

describe("guest nic op builders", () => {
  it("buildBulkGuestNicOps shares one param patch across every selected ref", () => {
    const ops = buildBulkGuestNicOps(["guest-nic:pve1:200/net0", "guest-nic:pve1:201/net0"], { bridgeOrVnet: "vmbr2" });
    expect(ops).toHaveLength(2);
    expect(ops.every((o) => o.op === "guest.nic.update")).toBe(true);
    expect(ops.map((o) => o.target)).toEqual(["guest-nic:pve1:200/net0", "guest-nic:pve1:201/net0"]);
    expect(ops.map((o) => o.params)).toEqual([{ bridgeOrVnet: "vmbr2" }, { bridgeOrVnet: "vmbr2" }]);
  });

  it("buildGuestNicUpdateOp is a plain single-op wrapper", () => {
    expect(buildGuestNicUpdateOp("guest-nic:pve1:200/net0", { linkDown: true })).toEqual({
      op: "guest.nic.update",
      target: "guest-nic:pve1:200/net0",
      params: { linkDown: true },
    });
  });
});

describe("sdn zone op builders", () => {
  const zoneForm: SdnZoneFormValues = {
    type: "vlan",
    bridge: "vmbr0",
    controller: "",
    nodes: ["pve1", "pve2"],
    exitNodes: [],
    peers: [],
    vrfVxlan: 0,
    mtu: 1500,
  };

  it("sdn.zone.create carries type/bridge/nodes/mtu", () => {
    const op = buildSdnZoneCreateOp("sdn-zone::zone1", zoneForm);
    expect(op).toEqual({
      op: "sdn.zone.create",
      target: "sdn-zone::zone1",
      params: { type: "vlan", bridge: "vmbr0", nodes: ["pve1", "pve2"], mtu: 1500 },
    });
  });

  it("sdn.zone.update is a diff against the initial values", () => {
    const changed: SdnZoneFormValues = { ...zoneForm, mtu: 1450, nodes: ["pve1"] };
    const op = buildSdnZoneUpdateOp("sdn-zone::zone1", zoneForm, changed);
    expect(op.op).toBe("sdn.zone.update");
    expect(op.params).toEqual({ mtu: 1450, nodes: ["pve1"] });
  });

  it("sdn.zone.update omits every field that didn't change", () => {
    const op = buildSdnZoneUpdateOp("sdn-zone::zone1", zoneForm, zoneForm);
    expect(op.params).toEqual({});
  });

  // T-403: the zone wizards' write-path gap fix (exitNodes/peers).
  it("sdn.zone.create carries exitNodes/peers when present", () => {
    const evpnForm: SdnZoneFormValues = {
      ...zoneForm,
      type: "evpn",
      exitNodes: ["pve1", "pve2"],
      peers: ["10.10.0.11", "10.10.0.12", "10.10.0.13"],
    };
    const op = buildSdnZoneCreateOp("sdn-zone::zone2", evpnForm);
    expect(op.params).toMatchObject({
      exitNodes: ["pve1", "pve2"],
      peers: ["10.10.0.11", "10.10.0.12", "10.10.0.13"],
    });
  });

  // T-403: caught while wiring the EVPN wizard — buildSdnZoneCreateOp
  // dropped `controller` entirely even though SdnZoneEditor's own
  // Controller field (type "evpn") writes into the form.
  it("sdn.zone.create carries controller when present", () => {
    const evpnForm: SdnZoneFormValues = { ...zoneForm, type: "evpn", controller: "evpn1" };
    const op = buildSdnZoneCreateOp("sdn-zone::zone3", evpnForm);
    expect(op.params).toMatchObject({ controller: "evpn1" });
  });

  it("sdn.zone.update diffs exitNodes/peers independently", () => {
    const changed: SdnZoneFormValues = { ...zoneForm, peers: ["10.10.0.99"] };
    const op = buildSdnZoneUpdateOp("sdn-zone::zone1", zoneForm, changed);
    expect(op.params).toEqual({ peers: ["10.10.0.99"] });
  });

  it("sdn.zone.delete carries no params", () => {
    expect(buildSdnZoneDeleteOp("sdn-zone::zone1")).toEqual({
      op: "sdn.zone.delete",
      target: "sdn-zone::zone1",
      params: {},
    });
  });
});

describe("sdn vnet op builders", () => {
  const vnetForm: SdnVnetFormValues = { zone: "zone1", alias: "app-tier", tag: 100, vlanAware: false };

  it("sdn.vnet.create carries zone/alias/tag/vlanAware", () => {
    const op = buildSdnVnetCreateOp("sdn-vnet::zone1/vnet100", vnetForm);
    expect(op).toEqual({
      op: "sdn.vnet.create",
      target: "sdn-vnet::zone1/vnet100",
      params: { zone: "zone1", alias: "app-tier", tag: 100, vlanAware: false },
    });
  });

  it("sdn.vnet.update only diffs alias/tag/vlanAware (zone is not editable post-create)", () => {
    const changed: SdnVnetFormValues = { ...vnetForm, tag: 200 };
    const op = buildSdnVnetUpdateOp("sdn-vnet::zone1/vnet100", vnetForm, changed);
    expect(op.params).toEqual({ tag: 200 });
  });

  it("sdn.vnet.delete carries no params", () => {
    expect(buildSdnVnetDeleteOp("sdn-vnet::zone1/vnet100")).toEqual({
      op: "sdn.vnet.delete",
      target: "sdn-vnet::zone1/vnet100",
      params: {},
    });
  });
});

describe("sdn subnet op builders", () => {
  const subnetForm: SdnSubnetFormValues = {
    vnet: "zone1/vnet100",
    cidr: "10.100.0.0/24",
    gateway: "10.100.0.1",
    dnsZonePrefix: "",
    dhcpRanges: ["10.100.0.100-10.100.0.200"],
    snat: true,
  };

  it("sdn.subnet.create carries vnet/cidr/gateway/dhcpRanges/snat", () => {
    const op = buildSdnSubnetCreateOp("sdn-subnet::10.100.0.0/24", subnetForm);
    expect(op).toEqual({
      op: "sdn.subnet.create",
      target: "sdn-subnet::10.100.0.0/24",
      params: {
        vnet: "zone1/vnet100",
        cidr: "10.100.0.0/24",
        gateway: "10.100.0.1",
        dhcpRanges: ["10.100.0.100-10.100.0.200"],
        snat: true,
      },
    });
  });

  it("sdn.subnet.update only diffs gateway/dnsZonePrefix/dhcpRanges/snat (vnet/cidr are not editable post-create)", () => {
    const changed: SdnSubnetFormValues = { ...subnetForm, gateway: "10.100.0.254" };
    const op = buildSdnSubnetUpdateOp("sdn-subnet::10.100.0.0/24", subnetForm, changed);
    expect(op.params).toEqual({ gateway: "10.100.0.254" });
  });

  it("sdn.subnet.delete carries no params", () => {
    expect(buildSdnSubnetDeleteOp("sdn-subnet::10.100.0.0/24")).toEqual({
      op: "sdn.subnet.delete",
      target: "sdn-subnet::10.100.0.0/24",
      params: {},
    });
  });
});

const ospfFabricForm: SdnFabricFormValues = {
  protocol: "ospf",
  ipPrefix: "10.255.0.0/24",
  ip6Prefix: "",
  csnpInterval: 0,
  helloInterval: 0,
  routeFilter: "pl1",
  area: "0.0.0.0",
  redistribute: ["connected"],
  persistentKeepalive: 0,
};

describe("sdn fabric op builders", () => {
  it("create carries protocol and only the fields this protocol uses", () => {
    const op = buildSdnFabricCreateOp("sdn-fabric::fab1", ospfFabricForm);
    expect(op).toEqual({
      op: "sdn.fabric.create",
      target: "sdn-fabric::fab1",
      params: {
        protocol: "ospf",
        ipPrefix: "10.255.0.0/24",
        area: "0.0.0.0",
        redistribute: ["connected"],
        routeFilter: "pl1",
      },
    });
  });

  it("update carries only fields that changed, and never protocol (immutable)", () => {
    const form: SdnFabricFormValues = { ...ospfFabricForm, area: "0.0.0.1" };
    const op = buildSdnFabricUpdateOp("sdn-fabric::fab1", ospfFabricForm, form);
    expect(op).toEqual({
      op: "sdn.fabric.update",
      target: "sdn-fabric::fab1",
      params: { area: "0.0.0.1" },
    });
  });

  it("delete carries no params", () => {
    expect(buildSdnFabricDeleteOp("sdn-fabric::fab1")).toEqual({
      op: "sdn.fabric.delete",
      target: "sdn-fabric::fab1",
      params: {},
    });
  });
});

describe("sdn.apply op builder", () => {
  it("carries no target (cluster-wide, no single entity) and no params", () => {
    expect(buildSdnApplyOp()).toEqual({ op: "sdn.apply", target: undefined, params: {} });
  });
});
