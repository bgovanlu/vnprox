import { describe, expect, it } from "vitest";
import type { EntityDetail, TopologyNode } from "../api/types";
import {
  InvalidGuestGroupIdError,
  InvalidPhysGroupIdError,
  expandGuestGroup,
  expandPhysicalGroup,
  guestNicRefsForGroup,
  nicDetailToTopologyElements,
  physNicDetailToTopologyElements,
} from "./expand";

function detail(partial: Partial<EntityDetail> & Pick<EntityDetail, "ref" | "kind" | "label">): EntityDetail {
  return { node: "pve1", fields: {}, provenance: {}, related: [], generatedAt: 1, ...partial };
}

describe("guestNicRefsForGroup", () => {
  it("selects only attached-to/from guest-nic refs matching the pill's node", () => {
    const target = detail({
      ref: "bridge:pve1:vmbr0",
      kind: "bridge",
      label: "vmbr0",
      related: [
        { ref: "guest-nic:pve1:100/net0", edgeKind: "attached-to", direction: "from" },
        { ref: "guest-nic:pve1:101/net0", edgeKind: "attached-to", direction: "from" },
        // Different node's NIC attached to the same cluster-scoped target —
        // belongs to a *different* guest-group pill, must be excluded.
        { ref: "guest-nic:pve2:200/net0", edgeKind: "attached-to", direction: "from" },
        // A bond enslaved by something else entirely — wrong edge kind.
        { ref: "bond:pve1:bond0", edgeKind: "port-of", direction: "from" },
        // Direction "to" means *this* target is the child, not relevant here.
        { ref: "sdn-vnet::z1/v1", edgeKind: "realizes", direction: "to" },
      ],
    });

    expect(guestNicRefsForGroup(target, "pve1")).toEqual(["guest-nic:pve1:100/net0", "guest-nic:pve1:101/net0"]);
  });

  it("returns an empty list when nothing matches", () => {
    const target = detail({ ref: "bridge:pve1:vmbr0", kind: "bridge", label: "vmbr0", related: [] });
    expect(guestNicRefsForGroup(target, "pve1")).toEqual([]);
  });
});

describe("nicDetailToTopologyElements", () => {
  it("synthesizes a guest node + attachment edge carrying the effective VLAN badge", () => {
    const nic = detail({
      ref: "guest-nic:pve1:100/net0",
      kind: "guest-nic",
      label: "web01/net0",
      node: "pve1",
      fields: { effectiveVid: 20, linkDown: false },
    });
    const { node, edge } = nicDetailToTopologyElements(nic, "bridge:pve1:vmbr0");

    expect(node).toMatchObject({
      id: "guest-nic:pve1:100/net0",
      kind: "guest-nic",
      label: "web01/net0",
      layer: "guest",
      nodeGroup: "pve1",
      status: "ok",
      badges: ["vid=20"],
    });
    expect(edge).toMatchObject({
      from: "guest-nic:pve1:100/net0",
      to: "bridge:pve1:vmbr0",
      kind: "attached-to",
      status: "ok",
      badges: ["vid=20"],
    });
  });

  it("marks a down NIC as status=down with a link-down badge", () => {
    const nic = detail({
      ref: "guest-nic:pve1:101/net0",
      kind: "guest-nic",
      label: "db01/net0",
      fields: { effectiveVid: 0, linkDown: true },
    });
    const { node } = nicDetailToTopologyElements(nic, "bridge:pve1:vmbr0");
    expect(node.status).toBe("down");
    expect(node.badges).toContain("link-down");
  });
});

describe("expandGuestGroup", () => {
  it("resolves the target then fetches each member NIC's own detail", async () => {
    const calls: string[] = [];
    const fetchDetail = (ref: string): Promise<EntityDetail> => {
      calls.push(ref);
      if (ref === "bridge:pve1:vmbr0") {
        return Promise.resolve(
          detail({
            ref,
            kind: "bridge",
            label: "vmbr0",
            related: [
              { ref: "guest-nic:pve1:100/net0", edgeKind: "attached-to", direction: "from" },
              { ref: "guest-nic:pve1:101/net0", edgeKind: "attached-to", direction: "from" },
            ],
          }),
        );
      }
      return Promise.resolve(
        detail({ ref, kind: "guest-nic", label: `${ref}-label`, fields: { effectiveVid: 20 } }),
      );
    };

    const result = await expandGuestGroup("guest-group:pve1:bridge:pve1:vmbr0", { fetchDetail });

    expect(calls).toEqual(["bridge:pve1:vmbr0", "guest-nic:pve1:100/net0", "guest-nic:pve1:101/net0"]);
    expect(result.nodes).toHaveLength(2);
    expect(result.edges).toHaveLength(2);
    expect(result.nodes.map((n) => n.id).sort()).toEqual(["guest-nic:pve1:100/net0", "guest-nic:pve1:101/net0"]);
  });

  it("rejects a malformed group id rather than silently no-oping", async () => {
    await expect(
      expandGuestGroup("bridge:pve1:vmbr0", { fetchDetail: () => Promise.reject(new Error("should not be called")) }),
    ).rejects.toBeInstanceOf(InvalidGuestGroupIdError);
  });

  it("tolerates one member NIC's fetch failing without losing the others", async () => {
    const fetchDetail = (ref: string): Promise<EntityDetail> => {
      if (ref === "bridge:pve1:vmbr0") {
        return Promise.resolve(
          detail({
            ref,
            kind: "bridge",
            label: "vmbr0",
            related: [
              { ref: "guest-nic:pve1:100/net0", edgeKind: "attached-to", direction: "from" },
              { ref: "guest-nic:pve1:101/net0", edgeKind: "attached-to", direction: "from" },
            ],
          }),
        );
      }
      if (ref === "guest-nic:pve1:101/net0") {
        return Promise.reject(new Error("network blip"));
      }
      return Promise.resolve(detail({ ref, kind: "guest-nic", label: ref, fields: {} }));
    };

    const result = await expandGuestGroup("guest-group:pve1:bridge:pve1:vmbr0", { fetchDetail });
    expect(result.nodes.map((n) => n.id)).toEqual(["guest-nic:pve1:100/net0"]);
  });
});

// --- physNicDetailToTopologyElements / expandPhysicalGroup (T-1907) -------
//
// AC2's losslessness criterion, tested as an equivalence rather than a spot
// check: for each case below, the expected node/edges are exactly what
// internal/topology/project.go's buildNodes/buildEdges independently
// compute for the same PhysNic entity (kind="physnic", layer="phys",
// badges=[] — badgesOf has no PhysNic case; status from LinkUp/LinkUpSet
// exactly mirroring statusOf's PhysNic branch; an edge only for a
// "to"-direction enslaved-by/port-of related entry, badges=[] and status
// equal to the reconstructed node's own status). Every branch of that rule
// (up+target, down+target, never-reported/unknown, no target at all, a
// port-of target instead of enslaved-by) gets its own case.
describe("physNicDetailToTopologyElements", () => {
  function nicDetail(partial: Partial<EntityDetail> & Pick<EntityDetail, "ref" | "label" | "node">): EntityDetail {
    return {
      kind: "physnic",
      fields: {},
      provenance: {},
      related: [],
      generatedAt: 1,
      ...partial,
    };
  }

  it("reconstructs an up NIC enslaved into a bond identically to the uncollapsed projection", () => {
    const detail = nicDetail({
      ref: "physnic:pve1:eno1",
      label: "eno1",
      node: "pve1",
      fields: { LinkUp: true, LinkUpSet: true },
      related: [
        { ref: "bond:pve1:bond0", edgeKind: "enslaved-by", direction: "to" },
        // A "from"-direction entry (this NIC as the target, never the
        // source, of some other edge) must never be mistaken for an
        // outbound attachment.
        { ref: "lldp-neighbor:pve1:sw1", edgeKind: "lldp-adjacent", direction: "from" },
      ],
    });
    const { node, edges } = physNicDetailToTopologyElements(detail);
    expect(node).toEqual({
      id: "physnic:pve1:eno1",
      kind: "physnic",
      label: "eno1",
      layer: "phys",
      nodeGroup: "pve1",
      status: "ok",
      badges: [],
    });
    expect(edges).toEqual([
      { from: "physnic:pve1:eno1", to: "bond:pve1:bond0", kind: "enslaved-by", status: "ok", badges: [] },
    ]);
  });

  it("reconstructs a down NIC port-of a bridge with status=down on both the node and its edge", () => {
    const detail = nicDetail({
      ref: "physnic:pve1:eno4",
      label: "eno4",
      node: "pve1",
      fields: { LinkUp: false, LinkUpSet: true },
      related: [{ ref: "bridge:pve1:vmbr0", edgeKind: "port-of", direction: "to" }],
    });
    const { node, edges } = physNicDetailToTopologyElements(detail);
    expect(node.status).toBe("down");
    expect(edges).toEqual([
      { from: "physnic:pve1:eno4", to: "bridge:pve1:vmbr0", kind: "port-of", status: "down", badges: [] },
    ]);
  });

  it("reconstructs a never-reported NIC as status=unknown, never down", () => {
    const detail = nicDetail({
      ref: "physnic:pve1:eno9",
      label: "eno9",
      node: "pve1",
      fields: { LinkUp: false, LinkUpSet: false },
    });
    const { node } = physNicDetailToTopologyElements(detail);
    expect(node.status).toBe("unknown");
  });

  it("reconstructs a genuinely free NIC (no related edges at all) with no synthesized edges", () => {
    const detail = nicDetail({ ref: "physnic:pve1:eno5", label: "eno5", node: "pve1", fields: { LinkUp: true, LinkUpSet: true } });
    const { edges } = physNicDetailToTopologyElements(detail);
    expect(edges).toEqual([]);
  });
});

describe("expandPhysicalGroup", () => {
  function groupNode(members: string[]): TopologyNode {
    return {
      id: "phys-group:pve1",
      kind: "phys-group",
      label: `${String(members.length)} NICs`,
      layer: "phys",
      nodeGroup: "pve1",
      status: "ok",
      badges: [`count=${String(members.length)}`],
      collapsedCount: members.length,
      members,
    };
  }

  it("fetches each member ref's own detail directly (no target-lookup phase)", async () => {
    const calls: string[] = [];
    const fetchDetail = (ref: string): Promise<EntityDetail> => {
      calls.push(ref);
      return Promise.resolve({
        ref,
        kind: "physnic",
        node: "pve1",
        label: ref,
        fields: { LinkUp: true, LinkUpSet: true },
        provenance: {},
        related: [],
        generatedAt: 1,
      });
    };

    const result = await expandPhysicalGroup(groupNode(["physnic:pve1:eno1", "physnic:pve1:eno2"]), { fetchDetail });

    expect(calls.sort()).toEqual(["physnic:pve1:eno1", "physnic:pve1:eno2"]);
    expect(result.nodes.map((n) => n.id).sort()).toEqual(["physnic:pve1:eno1", "physnic:pve1:eno2"]);
  });

  it("rejects a node whose id isn't a phys-group id rather than silently no-oping", async () => {
    const notAGroup: TopologyNode = {
      id: "bridge:pve1:vmbr0",
      kind: "bridge",
      label: "vmbr0",
      layer: "l2",
      nodeGroup: "pve1",
      status: "ok",
      badges: [],
    };
    await expect(
      expandPhysicalGroup(notAGroup, { fetchDetail: () => Promise.reject(new Error("should not be called")) }),
    ).rejects.toBeInstanceOf(InvalidPhysGroupIdError);
  });

  it("tolerates one member's fetch failing without losing the others", async () => {
    const fetchDetail = (ref: string): Promise<EntityDetail> => {
      if (ref === "physnic:pve1:eno2") return Promise.reject(new Error("network blip"));
      return Promise.resolve({
        ref,
        kind: "physnic",
        node: "pve1",
        label: ref,
        fields: { LinkUp: true, LinkUpSet: true },
        provenance: {},
        related: [],
        generatedAt: 1,
      });
    };

    const result = await expandPhysicalGroup(groupNode(["physnic:pve1:eno1", "physnic:pve1:eno2"]), { fetchDetail });
    expect(result.nodes.map((n) => n.id)).toEqual(["physnic:pve1:eno1"]);
  });
});
