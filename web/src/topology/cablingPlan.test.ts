// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import type { TopologyEdge, TopologyNode } from "../api/types";
import { buildSwitchModel } from "./switchModel";
import { buildCablingPlan, cablingPlanRowCount, cablingPlanUnknownCount, computeCablingDiagramLayout } from "./cablingPlan";

function node(partial: Partial<TopologyNode> & Pick<TopologyNode, "id" | "kind" | "layer">): TopologyNode {
  return { label: partial.id, nodeGroup: "pve1", status: "ok", badges: [], ...partial };
}
function edge(from: string, to: string, kind: string, badges: string[] = []): TopologyEdge {
  return { from, to, kind, status: "ok", badges };
}

describe("buildCablingPlan", () => {
  it("flattens a bridge's bond uplink into one row per member, discovered vs unknown", () => {
    const nodes: TopologyNode[] = [
      node({ id: "bridge:pve1:vmbr0", kind: "bridge", layer: "l2", label: "vmbr0" }),
      node({ id: "bond:pve1:bond0", kind: "bond", layer: "l2", label: "bond0" }),
      node({
        id: "physnic:pve1:eno1",
        kind: "physnic",
        layer: "phys",
        label: "eno1",
        speedMbps: 1000,
        mediaPort: "tp",
        duplex: "full",
      }),
      node({ id: "physnic:pve1:eno2", kind: "physnic", layer: "phys", label: "eno2" }),
      node({
        id: "lldp-neighbor:pve1:sw-core-01",
        kind: "lldp-neighbor",
        layer: "phys",
        label: "sw-core-01",
        badges: ["port=Te1/0/1"],
      }),
    ];
    const edges: TopologyEdge[] = [
      edge("bond:pve1:bond0", "bridge:pve1:vmbr0", "port-of"),
      edge("physnic:pve1:eno1", "bond:pve1:bond0", "enslaved-by", ["active"]),
      edge("physnic:pve1:eno2", "bond:pve1:bond0", "enslaved-by", ["active"]),
      edge("physnic:pve1:eno1", "lldp-neighbor:pve1:sw-core-01", "lldp-adjacent"),
    ];

    const plan = buildCablingPlan(buildSwitchModel(nodes, edges));
    expect(plan.nodes).toHaveLength(1);
    const rows = plan.nodes[0]?.rows ?? [];
    expect(rows.map((r) => r.nicLabel)).toEqual(["eno1", "eno2"]);

    const eno1 = rows[0];
    expect(eno1).toBeDefined();
    expect(eno1?.linkState).toBe("discovered");
    expect(eno1?.farEndSwitch).toBe("sw-core-01");
    expect(eno1?.farEndPort).toBe("Te1/0/1");
    expect(eno1?.bridgeName).toBe("vmbr0");
    expect(eno1?.bondLabel).toBe("bond0");
    expect(eno1?.speedMbps).toBe(1000);
    expect(eno1?.mediaPort).toBe("tp");
    expect(eno1?.duplex).toBe("full");

    const eno2 = rows[1];
    expect(eno2?.linkState).toBe("unknown");
    expect(eno2?.farEndSwitch).toBeUndefined();
    expect(eno2?.farEndPort).toBeUndefined();
    expect(eno2?.bondLabel).toBe("bond0");
  });

  it("marks a NIC not wired into any bridge as a row with no bridgeName", () => {
    const nodes: TopologyNode[] = [node({ id: "physnic:pve1:eno9", kind: "physnic", layer: "phys" })];
    const plan = buildCablingPlan(buildSwitchModel(nodes, []));
    const rows = plan.nodes[0]?.rows ?? [];
    expect(rows).toHaveLength(1);
    expect(rows[0]?.bridgeName).toBeUndefined();
    expect(rows[0]?.bondLabel).toBeUndefined();
    expect(rows[0]?.linkState).toBe("unknown");
  });

  it("marks a collapsed phys-group pill 'grouped', never 'unknown' — it is not a real single unresolved link", () => {
    const nodes: TopologyNode[] = [
      node({ id: "phys-group:pve1", kind: "phys-group", layer: "phys", collapsedCount: 6, label: "6 more NICs" }),
    ];
    const plan = buildCablingPlan(buildSwitchModel(nodes, []));
    const rows = plan.nodes[0]?.rows ?? [];
    expect(rows).toHaveLength(1);
    expect(rows[0]?.linkState).toBe("grouped");
    expect(rows[0]?.isGroup).toBe(true);
    expect(rows[0]?.groupCount).toBe(6);
  });

  it("drops node groups with zero rows rather than rendering an empty node section", () => {
    const plan = buildCablingPlan(buildSwitchModel([], []));
    expect(plan.nodes).toEqual([]);
  });

  it("cablingPlanRowCount sums rows across nodes; cablingPlanUnknownCount excludes grouped rows", () => {
    const nodes: TopologyNode[] = [
      node({ id: "physnic:pve1:eno1", kind: "physnic", layer: "phys" }),
      node({ id: "phys-group:pve1", kind: "phys-group", layer: "phys", collapsedCount: 3 }),
    ];
    const plan = buildCablingPlan(buildSwitchModel(nodes, []));
    expect(cablingPlanRowCount(plan)).toBe(2);
    expect(cablingPlanUnknownCount(plan)).toBe(1); // eno1 only — the group row is "grouped", not "unknown"
  });
});

describe("computeCablingDiagramLayout", () => {
  it("lays out one row per node and one port box per NIC, with a non-blank far-end label per state", () => {
    const nodes: TopologyNode[] = [
      node({ id: "physnic:pve1:eno1", kind: "physnic", layer: "phys", label: "eno1" }),
      node({ id: "lldp-neighbor:pve1:sw1", kind: "lldp-neighbor", layer: "phys", label: "sw-core-01", badges: ["port=Te1/0/1"] }),
      node({ id: "physnic:pve1:eno2", kind: "physnic", layer: "phys", label: "eno2" }),
    ];
    const edges: TopologyEdge[] = [edge("physnic:pve1:eno1", "lldp-neighbor:pve1:sw1", "lldp-adjacent")];

    const plan = buildCablingPlan(buildSwitchModel(nodes, edges));
    const layout = computeCablingDiagramLayout(plan);

    expect(layout.rows).toHaveLength(1);
    const row = layout.rows[0];
    expect(row?.node).toBe("pve1");
    expect(row?.ports).toHaveLength(2);

    const discoveredPort = row?.ports.find((p) => p.label === "eno1");
    expect(discoveredPort?.linkState).toBe("discovered");
    expect(discoveredPort?.farEndLabel).toBe("sw-core-01 Te1/0/1");

    const unknownPort = row?.ports.find((p) => p.label === "eno2");
    expect(unknownPort?.linkState).toBe("unknown");
    expect(unknownPort?.farEndLabel).toBe("Not discovered");

    // Positions never overlap: the second port starts at or beyond the
    // first port's right edge.
    expect(discoveredPort && unknownPort && unknownPort.x >= discoveredPort.x + discoveredPort.width).toBe(true);
  });

  it("labels a grouped pill's far end distinctly from both discovered and unknown", () => {
    const nodes: TopologyNode[] = [
      node({ id: "phys-group:pve1", kind: "phys-group", layer: "phys", collapsedCount: 4, label: "4 more NICs" }),
    ];
    const plan = buildCablingPlan(buildSwitchModel(nodes, []));
    const layout = computeCablingDiagramLayout(plan);
    const port = layout.rows[0]?.ports[0];
    expect(port?.linkState).toBe("grouped");
    expect(port?.farEndLabel).toBe("4 NICs grouped");
  });

  it("computes a minimal, finite layout for an empty plan rather than NaN/negative dimensions", () => {
    const layout = computeCablingDiagramLayout({ nodes: [] });
    expect(layout.rows).toEqual([]);
    expect(Number.isFinite(layout.width)).toBe(true);
    expect(Number.isFinite(layout.height)).toBe(true);
    expect(layout.width).toBeGreaterThan(0);
    expect(layout.height).toBeGreaterThan(0);
  });
});
