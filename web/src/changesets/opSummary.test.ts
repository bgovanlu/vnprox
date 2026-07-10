import { describe, expect, it } from "vitest";
import type { Op } from "../api/types";
import { opKindLabel, refId, refNode, summarizeOp } from "./opSummary";

describe("refNode / refId", () => {
  it("splits a plain kind:node:id ref", () => {
    expect(refNode("bridge:pve1:vmbr0")).toBe("pve1");
    expect(refId("bridge:pve1:vmbr0")).toBe("vmbr0");
  });

  it("recovers an id containing extra colons/slashes (only the first two colons are structural)", () => {
    expect(refNode("sdn-subnet::2001:db8::/64")).toBe("");
    expect(refId("sdn-subnet::2001:db8::/64")).toBe("2001:db8::/64");
    expect(refId("sdn-vnet::zone1/vnet1")).toBe("zone1/vnet1");
  });

  it("returns empty string for a malformed ref rather than throwing", () => {
    expect(refNode("not-a-ref")).toBe("");
    expect(refId("not-a-ref")).toBe("not-a-ref");
  });
});

describe("opKindLabel", () => {
  it("is the op-type family prefix", () => {
    expect(opKindLabel({ op: "bridge.create", target: "x", params: {} })).toBe("bridge");
    expect(opKindLabel({ op: "guest.nic.update", target: "x", params: {} })).toBe("guest");
    expect(opKindLabel({ op: "sdn.apply", params: {} })).toBe("sdn");
  });
});

describe("summarizeOp", () => {
  it("renders bond.create with mode and slaves", () => {
    const op: Op = { op: "bond.create", target: "bond:pve1:bond1", params: { mode: "802.3ad", slaves: ["eno3", "eno4"] } };
    expect(summarizeOp(op)).toBe("Create bond bond1 (802.3ad) from eno3, eno4");
  });

  it("renders bridge.create with its ports", () => {
    const op: Op = { op: "bridge.create", target: "bridge:pve1:vmbr1", params: { ports: ["bond1"] } };
    expect(summarizeOp(op)).toBe("Create bridge vmbr1 with ports bond1");
  });

  it("renders bridge.port.add / remove", () => {
    expect(
      summarizeOp({ op: "bridge.port.add", target: "bridge:pve1:vmbr0", params: { port: "eno3" } }),
    ).toBe("Add port eno3 to bridge vmbr0");
    expect(
      summarizeOp({ op: "bridge.port.remove", target: "bridge:pve1:vmbr0", params: { port: "eno3" } }),
    ).toBe("Remove port eno3 from bridge vmbr0");
  });

  it("renders vlan.create with vid and parent", () => {
    const op: Op = { op: "vlan.create", target: "vlan:pve1:vmbr0.30", params: { parent: "vmbr0", vid: 30 } };
    expect(summarizeOp(op)).toBe("Create VLAN vmbr0.30 (vid 30 on vmbr0)");
  });

  it("renders an update op's field list, falling back to 'no changes'", () => {
    expect(summarizeOp({ op: "iface.update", target: "physnic:pve1:eno3", params: {} })).toBe(
      "Update interface eno3 (no changes)",
    );
    expect(
      summarizeOp({ op: "iface.update", target: "physnic:pve1:eno3", params: { mtu: 9000, autostart: true } }),
    ).toBe("Update interface eno3 (mtu=9000, autostart=true)");
  });

  it("renders guest.nic.update's several optional fields together", () => {
    const op: Op = {
      op: "guest.nic.update",
      target: "guest-nic:pve1:200/net0",
      params: { bridgeOrVnet: "vmbr1", vid: 20, firewall: true, linkDown: false },
    };
    expect(summarizeOp(op)).toBe(
      "Update guest NIC 200/net0 (reattach to vmbr1, vid=20, firewall=true, connect)",
    );
  });

  it("renders sdn.apply (the one op with no target)", () => {
    expect(summarizeOp({ op: "sdn.apply", params: {} })).toBe("Apply pending SDN configuration (cluster-wide)");
  });

  it("falls back to a generic '<op> <id>' for op families the drawer doesn't special-case", () => {
    expect(summarizeOp({ op: "fw.rule.create", target: "fw-ruleset::cluster", params: {} })).toBe(
      "fw.rule.create cluster",
    );
  });
});
