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
  buildVlanCreateOp,
  buildVlanUpdateOp,
  type BondFormValues,
  type BridgeFormValues,
  type IfaceFormValues,
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
