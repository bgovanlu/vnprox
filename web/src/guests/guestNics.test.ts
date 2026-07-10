import { describe, expect, it } from "vitest";
import type { TopologyResponse } from "../api/types";
import { filterGuestNicRows, guestGroupPillIds, guestNicRowsFromTopology, targetLabel } from "./guestNics";

const topology: TopologyResponse = {
  layers: ["guest"],
  generatedAt: 1,
  nodes: [
    {
      id: "guest-nic:pve1:200/net0",
      kind: "guest-nic",
      label: "app01/net0",
      layer: "guest",
      nodeGroup: "pve1",
      status: "ok",
      badges: ["vid=100"],
    },
    {
      id: "guest-nic:pve2:201/net0",
      kind: "guest-nic",
      label: "cache01/net0",
      layer: "guest",
      nodeGroup: "pve2",
      status: "down",
      badges: ["link-down"],
    },
    {
      id: "guest-group:pve3:bridge:pve3:vmbr0",
      kind: "guest-group",
      label: "12 guests",
      layer: "guest",
      nodeGroup: "pve3",
      status: "ok",
      badges: [],
      collapsedCount: 12,
    },
  ],
  edges: [
    { from: "guest-nic:pve1:200/net0", to: "bridge:pve1:vmbr0", kind: "attached-to", status: "ok", badges: [] },
    { from: "guest-nic:pve2:201/net0", to: "sdn-vnet::vnet100", kind: "attached-to", status: "down", badges: [] },
  ],
};

describe("guestNicRowsFromTopology", () => {
  it("builds one row per un-collapsed guest-nic node with its resolved target/vid/link state", () => {
    const rows = guestNicRowsFromTopology(topology);
    expect(rows).toHaveLength(2);
    expect(rows[0]).toEqual({
      ref: "guest-nic:pve1:200/net0",
      label: "app01/net0",
      node: "pve1",
      bridgeOrVnet: "bridge:pve1:vmbr0",
      vid: 100,
      linkDown: false,
    });
    expect(rows[1]).toMatchObject({ node: "pve2", bridgeOrVnet: "sdn-vnet::vnet100", linkDown: true, vid: undefined });
  });

  it("never includes a 'guest-group' pill itself as a row", () => {
    const rows = guestNicRowsFromTopology(topology);
    expect(rows.some((r) => r.ref.startsWith("guest-group:"))).toBe(false);
  });
});

describe("guestGroupPillIds", () => {
  it("lists every collapsed pill so callers can expand it", () => {
    expect(guestGroupPillIds(topology)).toEqual(["guest-group:pve3:bridge:pve3:vmbr0"]);
  });
});

describe("filterGuestNicRows", () => {
  const rows = guestNicRowsFromTopology(topology);

  it("filters by node", () => {
    expect(filterGuestNicRows(rows, { node: "pve1" })).toHaveLength(1);
  });

  it("filters by bridge/VNet", () => {
    expect(filterGuestNicRows(rows, { bridgeOrVnet: "sdn-vnet::vnet100" })).toHaveLength(1);
  });

  it("filters by VLAN", () => {
    expect(filterGuestNicRows(rows, { vid: 100 })).toHaveLength(1);
    expect(filterGuestNicRows(rows, { vid: 999 })).toHaveLength(0);
  });

  it("with no filter, returns everything", () => {
    expect(filterGuestNicRows(rows, {})).toHaveLength(2);
  });
});

describe("targetLabel", () => {
  it("shows the bare id, or a placeholder when unattached", () => {
    expect(targetLabel("bridge:pve1:vmbr0")).toBe("vmbr0");
    expect(targetLabel(undefined)).toBe("(unattached)");
  });
});
