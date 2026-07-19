import { describe, expect, it } from "vitest";
import type { TopologyEdge, TopologyNode } from "../../api/types";
import { computePodUnderlayChain } from "./k8sUnderlay";

function nodes(): TopologyNode[] {
  return [
    { id: "guest:pve1:200", kind: "guest", label: "app01", layer: "guest", nodeGroup: "pve1", status: "ok", badges: [] },
    {
      id: "guest-nic:pve1:200/net0",
      kind: "guest-nic",
      label: "app01/net0",
      layer: "guest",
      nodeGroup: "pve1",
      status: "ok",
      badges: [],
    },
    { id: "bridge:pve1:vmbr0", kind: "bridge", label: "vmbr0", layer: "l2", nodeGroup: "pve1", status: "ok", badges: [] },
    { id: "bond:pve1:bond0", kind: "bond", label: "bond0", layer: "l2", nodeGroup: "pve1", status: "ok", badges: [] },
    {
      id: "physnic:pve1:eth0",
      kind: "physnic",
      label: "eth0",
      layer: "phys",
      nodeGroup: "pve1",
      status: "ok",
      badges: [],
    },
  ];
}

function edges(): TopologyEdge[] {
  return [
    { from: "guest-nic:pve1:200/net0", to: "bridge:pve1:vmbr0", kind: "attached-to", status: "ok", badges: [] },
    { from: "bond:pve1:bond0", to: "bridge:pve1:vmbr0", kind: "port-of", status: "ok", badges: [] },
    { from: "physnic:pve1:eth0", to: "bond:pve1:bond0", kind: "enslaved-by", status: "ok", badges: [] },
  ];
}

describe("computePodUnderlayChain", () => {
  it("traces guest -> guest-nic -> bridge -> bond, in order", () => {
    const paths = computePodUnderlayChain(nodes(), edges(), "guest:pve1:200");
    expect(paths).toHaveLength(1);
    expect(paths[0]?.hops.map((h) => h.kind)).toEqual(["guest", "guest-nic", "bridge", "bond"]);
    expect(paths[0]?.hops.map((h) => h.id)).toEqual([
      "guest:pve1:200",
      "guest-nic:pve1:200/net0",
      "bridge:pve1:vmbr0",
      "bond:pve1:bond0",
    ]);
  });

  it("stops at the bridge when it sits on no bond (a plain physical NIC)", () => {
    const n = nodes().filter((x) => x.id !== "bond:pve1:bond0");
    const e = edges().filter((x) => x.kind !== "port-of" && x.kind !== "enslaved-by");
    const paths = computePodUnderlayChain(n, e, "guest:pve1:200");
    expect(paths[0]?.hops.map((h) => h.kind)).toEqual(["guest", "guest-nic", "bridge"]);
  });

  it("produces one path per guest NIC for a multi-NIC guest", () => {
    const n = [
      ...nodes(),
      {
        id: "guest-nic:pve1:200/net1",
        kind: "guest-nic",
        label: "app01/net1",
        layer: "guest" as const,
        nodeGroup: "pve1",
        status: "ok" as const,
        badges: [],
      },
      { id: "bridge:pve1:vmbr1", kind: "bridge", label: "vmbr1", layer: "l2" as const, nodeGroup: "pve1", status: "ok" as const, badges: [] },
    ];
    const e = [
      ...edges(),
      { from: "guest-nic:pve1:200/net1", to: "bridge:pve1:vmbr1", kind: "attached-to", status: "ok" as const, badges: [] },
    ];
    const paths = computePodUnderlayChain(n, e, "guest:pve1:200");
    expect(paths).toHaveLength(2);
    expect(paths.map((p) => p.nicId).sort()).toEqual(["guest-nic:pve1:200/net0", "guest-nic:pve1:200/net1"]);
  });

  it("returns an empty array (never a guessed hop) when the guest has no rendered guest-nic at all", () => {
    const paths = computePodUnderlayChain(nodes(), edges(), "guest:pve1:999");
    expect(paths).toEqual([]);
  });

  it("returns an empty array for a ref that doesn't parse as a guest ref", () => {
    const paths = computePodUnderlayChain(nodes(), edges(), "bridge:pve1:vmbr0");
    expect(paths).toEqual([]);
  });
});
