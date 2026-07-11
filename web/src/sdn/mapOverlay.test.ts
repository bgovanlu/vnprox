// T-401 acceptance criteria 3 & 4: verifies the topology map overlay's SDN
// layer against a real captured fixture (evpn-lab.yaml run through the
// actual pvemock -> collect -> inventory.Graph -> topology.Project
// pipeline — the same capture process
// topology/threeNodeVlan.render.test.tsx's doc comment describes for its
// own fixture; see this task's completion report for exactly how this one
// was captured).
//
// AC3: "VNet plane connects the right bridges on the right nodes; VTEP
// mesh renders for the VXLAN zone."
// AC4: "A zone with a node reporting error status paints amber/red in the
// tree and map consistently" — the map half; sdn/status.test.ts covers the
// tree-side half of the same mapping.
import fixture from "./__fixtures__/evpn-lab-topology.json";
import { describe, expect, it } from "vitest";
import type { TopologyResponse } from "../api/types";

const topo = fixture as unknown as TopologyResponse;

function edgesOfKind(kind: string) {
  return topo.edges.filter((e) => e.kind === kind);
}

function nodeById(id: string) {
  const n = topo.nodes.find((node) => node.id === id);
  if (!n) throw new Error(`fixture missing node ${id}`);
  return n;
}

describe("evpn-lab map overlay: VNet planes (realizes edges)", () => {
  it("vnet-vlan1 (vlan zone, bridge on every node) connects to vmbr0 on all three nodes", () => {
    const realizes = edgesOfKind("realizes").filter((e) => e.from === "sdn-vnet::vlanz/vnet-vlan1");
    const targets = realizes.map((e) => e.to).sort();
    expect(targets).toEqual(["bridge:pve1:vmbr0", "bridge:pve2:vmbr0", "bridge:pve3:vmbr0"]);
  });

  it("vnet-simple1 (simple zone, bridge missing on pve3) connects only to the nodes that actually have vmbr1", () => {
    const realizes = edgesOfKind("realizes").filter((e) => e.from === "sdn-vnet::simplez/vnet-simple1");
    const targets = realizes.map((e) => e.to).sort();
    // Exactly pve1/pve2 — never pve3, which the fixture deliberately omits
    // vmbr1 from (T-401 AC4's error-status scenario).
    expect(targets).toEqual(["bridge:pve1:vmbr1", "bridge:pve2:vmbr1"]);
  });

  it("every realizes edge is badged with the node it lands on", () => {
    for (const e of edgesOfKind("realizes")) {
      const node = e.to.split(":")[1] ?? "";
      expect(node).not.toBe("");
      expect(e.badges).toContain(`node=${node}`);
    }
  });
});

describe("evpn-lab map overlay: VTEP mesh (vtep-peer edges)", () => {
  it("renders a full 3-node mesh (3 edges) for the vxlan zone", () => {
    const mesh = edgesOfKind("vtep-peer").filter((e) => e.badges.includes("zone=vxlanz"));
    expect(mesh).toHaveLength(3);
    const endpoints = new Set(mesh.flatMap((e) => [e.from, e.to]));
    expect(endpoints).toEqual(new Set(["bridge:pve1:vmbr0", "bridge:pve2:vmbr0", "bridge:pve3:vmbr0"]));
  });

  it("also renders the mesh for the evpn zone, distinctly badged", () => {
    const mesh = edgesOfKind("vtep-peer").filter((e) => e.badges.includes("zone=evpnz"));
    expect(mesh).toHaveLength(3);
  });

  it("every VTEP mesh edge carries the zone's VNI MTU annotation", () => {
    for (const e of edgesOfKind("vtep-peer")) {
      expect(e.badges.some((b) => b.startsWith("vniMtu="))).toBe(true);
    }
  });
});

describe("evpn-lab map overlay: status painting consistency (AC4)", () => {
  it("simplez (a node missing its bridge) paints down/red on the map", () => {
    expect(nodeById("sdn-zone::simplez").status).toBe("down");
  });

  it("a healthy zone with no per-node problems paints ok", () => {
    expect(nodeById("sdn-zone::vxlanz").status).toBe("ok");
  });

  it("a zone with a staged-but-unapplied edit paints degraded (amber), not down", () => {
    expect(nodeById("sdn-zone::vlanz").status).toBe("degraded");
  });
});
