// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import {
  buildBondSlaveUpdateOps,
  buildBondUplinkOps,
  buildDedicatedVlanOps,
  nextFreeBondName,
  parseRef,
} from "./mgmtWizardOps";

describe("parseRef", () => {
  it("splits a kind:node:id triplet", () => {
    expect(parseRef("physnic:pve1:eno1")).toEqual({ kind: "physnic", node: "pve1", id: "eno1" });
  });
  it("returns undefined for a malformed ref", () => {
    expect(parseRef("nope")).toBeUndefined();
    expect(parseRef("a:b")).toBeUndefined();
  });
});

describe("buildBondUplinkOps (flow A)", () => {
  it("matches the backend golden ops for active-backup (no LACP fields)", () => {
    const ops = buildBondUplinkOps({
      node: "pve1",
      bridgeRef: "bridge:pve1:vmbr0",
      currentPortNic: "eno1",
      candidateNic: "eno2",
      bondName: "bond0",
      mode: "active-backup",
    });
    expect(ops).toEqual([
      { op: "bridge.port.remove", target: "bridge:pve1:vmbr0", params: { port: "eno1" } },
      { op: "bond.create", target: "bond:pve1:bond0", params: { mode: "active-backup", slaves: ["eno1", "eno2"], miimon: 100 } },
      { op: "bridge.port.add", target: "bridge:pve1:vmbr0", params: { port: "bond0" } },
    ]);
  });

  it("adds LACP fields for 802.3ad", () => {
    const ops = buildBondUplinkOps({
      node: "pve1",
      bridgeRef: "bridge:pve1:vmbr0",
      currentPortNic: "eno1",
      candidateNic: "eno2",
      bondName: "bond0",
      mode: "802.3ad",
    });
    expect(ops[1]?.params).toEqual({
      mode: "802.3ad",
      slaves: ["eno1", "eno2"],
      miimon: 100,
      lacpRate: "fast",
      xmitHashPolicy: "layer3+4",
    });
  });

  it("never emits an address-bearing op — the mgmt IP is untouched by construction (AC2)", () => {
    const ops = buildBondUplinkOps({
      node: "pve1",
      bridgeRef: "bridge:pve1:vmbr0",
      currentPortNic: "eno1",
      candidateNic: "eno2",
      bondName: "bond0",
      mode: "active-backup",
    });
    for (const op of ops) {
      expect(JSON.stringify(op.params)).not.toContain("address");
      expect(JSON.stringify(op.params)).not.toContain("gateway");
    }
  });

  it("always ends by re-adding a port to the bridge — the wizard cannot construct a port-less mgmt bridge (AC2)", () => {
    const ops = buildBondUplinkOps({
      node: "pve1",
      bridgeRef: "bridge:pve1:vmbr0",
      currentPortNic: "eno1",
      candidateNic: "eno2",
      bondName: "bond0",
      mode: "active-backup",
    });
    const last = ops[ops.length - 1];
    expect(last?.op).toBe("bridge.port.add");
    expect(last?.target).toBe("bridge:pve1:vmbr0");
  });
});

describe("buildBondSlaveUpdateOps (flow B)", () => {
  it("adds a slave", () => {
    expect(buildBondSlaveUpdateOps({ bondRef: "bond:pve1:bond0", slaves: ["eno1", "eno2", "eno3"] })).toEqual([
      { op: "bond.update", target: "bond:pve1:bond0", params: { slaves: ["eno1", "eno2", "eno3"] } },
    ]);
  });
  it("replaces a slave", () => {
    expect(buildBondSlaveUpdateOps({ bondRef: "bond:pve1:bond0", slaves: ["eno1", "eno3"] })).toEqual([
      { op: "bond.update", target: "bond:pve1:bond0", params: { slaves: ["eno1", "eno3"] } },
    ]);
  });
});

describe("buildDedicatedVlanOps (flow C)", () => {
  it("strips the address off the old carrier first, then re-creates it verbatim on a new VLAN (order + value preserved)", () => {
    const ops = buildDedicatedVlanOps({
      node: "pve1",
      oldCarrierRef: "vlan:pve1:vmbr0.30",
      parentBridge: "vmbr0",
      vid: 40,
      vlanName: "vmbr0.40",
      addresses: ["10.20.30.11/24"],
      gateway: "10.20.30.1",
      mtu: 1500,
    });
    expect(ops).toEqual([
      { op: "iface.update", target: "vlan:pve1:vmbr0.30", params: { removeAddress: true, removeGateway: true } },
      {
        op: "vlan.create",
        target: "vlan:pve1:vmbr0.40",
        params: { parent: "vmbr0", vid: 40, addresses: ["10.20.30.11/24"], gateway: "10.20.30.1", mtu: 1500 },
      },
    ]);
  });

  it("carries the same address value — the wizard cannot re-address the mgmt IP (AC2)", () => {
    const ops = buildDedicatedVlanOps({
      node: "pve1",
      oldCarrierRef: "bridge:pve1:vmbr0",
      parentBridge: "vmbr0",
      vid: 10,
      vlanName: "vmbr0.10",
      addresses: ["192.168.1.10/24"],
      gateway: "192.168.1.1",
    });
    // The create op's address equals the input address; there is no code
    // path that lets the UI substitute a different IP.
    const createOp = ops[1];
    expect((createOp?.params as { addresses: string[] }).addresses).toEqual(["192.168.1.10/24"]);
  });
});

describe("nextFreeBondName", () => {
  it("picks bond0 when free", () => {
    expect(nextFreeBondName(["eno1", "eno2"])).toBe("bond0");
  });
  it("skips taken names", () => {
    expect(nextFreeBondName(["bond0", "bond1"])).toBe("bond2");
  });
});
