import { describe, expect, it } from "vitest";
import type { Blueprint } from "../api/types";
import { buildPreview, layerOf } from "./preview";

function bp(entities: Blueprint["entities"]): Blueprint {
  return {
    blueprintVersion: 1,
    id: "t",
    name: "t",
    nodeSelector: { mode: "all" },
    params: [],
    entities,
  };
}

describe("buildPreview", () => {
  it("wires a bridge's ports as incoming edges, including a port that isn't itself an entity", () => {
    const preview = buildPreview(
      bp([{ kind: "bridge", idTemplate: "vmbr0", fields: { ports: ["{{uplink}}"] } }]),
    );
    expect(preview.nodes.map((n) => n.id).sort()).toEqual(["vmbr0", "{{uplink}}"]);
    expect(preview.edges).toEqual([{ from: "{{uplink}}", to: "vmbr0" }]);
    const uplink = preview.nodes.find((n) => n.id === "{{uplink}}");
    expect(uplink?.kind).toBe("input");
  });

  it("wires bond -> bridge -> vlan for the LACP-bond-storage-vlan shape", () => {
    const preview = buildPreview(
      bp([
        { kind: "bond", idTemplate: "bond0", fields: { slaves: ["{{nic1}}", "{{nic2}}"] } },
        { kind: "bridge", idTemplate: "vmbr0", fields: { ports: ["bond0"] } },
        { kind: "vlan", idTemplate: "bond0.{{storageVid}}", fields: { parent: "bond0" } },
      ]),
    );
    expect(preview.nodes.map((n) => n.id).sort()).toEqual(
      ["bond0", "bond0.{{storageVid}}", "vmbr0", "{{nic1}}", "{{nic2}}"].sort(),
    );
    expect(preview.edges).toContainEqual({ from: "bond0", to: "vmbr0" });
    expect(preview.edges).toContainEqual({ from: "bond0", to: "bond0.{{storageVid}}" });
    expect(preview.edges).toContainEqual({ from: "{{nic1}}", to: "bond0" });
  });

  it("wires zone -> vnet -> subnet for the SDN shape", () => {
    const preview = buildPreview(
      bp([
        { kind: "sdn-zone", idTemplate: "z1", fields: { type: "vxlan" } },
        { kind: "sdn-vnet", idTemplate: "z1/v1", fields: { zone: "z1" } },
        { kind: "sdn-subnet", idTemplate: "10.0.0.0/24", fields: { vnet: "z1/v1", cidr: "10.0.0.0/24" } },
      ]),
    );
    expect(preview.edges).toContainEqual({ from: "z1", to: "z1/v1" });
    expect(preview.edges).toContainEqual({ from: "z1/v1", to: "10.0.0.0/24" });
  });
});

describe("layerOf", () => {
  it("orders physical/bond inputs before bridge/vlan before SDN layers", () => {
    expect(layerOf("input")).toBeLessThan(layerOf("bridge"));
    expect(layerOf("bond")).toBeLessThan(layerOf("vlan"));
    expect(layerOf("bridge")).toBeLessThan(layerOf("sdn-zone"));
    expect(layerOf("sdn-zone")).toBeLessThan(layerOf("sdn-vnet"));
    expect(layerOf("sdn-vnet")).toBeLessThan(layerOf("sdn-subnet"));
  });
});
