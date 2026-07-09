import { describe, expect, it } from "vitest";
import type { EntityDetail } from "../api/types";
import { InvalidGuestGroupIdError, expandGuestGroup, guestNicRefsForGroup, nicDetailToTopologyElements } from "./expand";

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
