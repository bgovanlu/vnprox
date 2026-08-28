// SPDX-License-Identifier: Apache-2.0

// T-1505 acceptance criterion 5: the map renders the shaping-active badge
// for a fixture with an active shape, absent otherwise. Mirrors
// EntityNode.mgmt.test.tsx's exact pattern (T-702) — the badges[] array
// already flows through GET /topology generically (T-901's badge-rendering
// convention); this proves the "qos-shaped" token specifically gets the
// distinct (blue) treatment when present, and that the badge itself is
// simply absent (not just unstyled) when a node carries no shape.
import fullFixture from "./__fixtures__/three-node-vlan-topology.json";
import { ReactFlowProvider } from "@xyflow/react";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { TopologyResponse } from "../api/types";
import { EntityNode, type EntityFlowNode } from "./EntityNode";

const full = fullFixture as unknown as TopologyResponse;

function renderNode(badges: string[]): void {
  const vmbr0 = full.nodes.find((n) => n.id === "bridge:pve1:vmbr0");
  if (!vmbr0) throw new Error("fixture missing bridge:pve1:vmbr0");

  const node: EntityFlowNode = {
    id: vmbr0.id,
    type: "entity",
    position: { x: 0, y: 0 },
    data: {
      label: vmbr0.label,
      kind: vmbr0.kind,
      status: vmbr0.status,
      badges,
      dimmed: false,
      highlighted: false,
      isGuestGroup: false,
    },
  };

  render(
    <ReactFlowProvider>
      <EntityNode
        id={node.id}
        type="entity"
        data={node.data}
        selected={false}
        dragging={false}
        zIndex={0}
        isConnectable={false}
        positionAbsoluteX={0}
        positionAbsoluteY={0}
        deletable
        draggable
        selectable
      />
    </ReactFlowProvider>,
  );
}

describe("EntityNode: T-1505 shaping-active badge", () => {
  it("renders the qos-shaped badge with the distinct blue treatment when the fixture has an active shape", () => {
    renderNode(["qos-shaped"]);
    const badge = screen.getByText("qos-shaped");
    expect(badge.className).toContain("blue");
    expect(badge.title).toContain("QoS shape");
  });

  it("does not render a qos-shaped badge when no shape is active", () => {
    renderNode(["vlans=10-20"]);
    expect(screen.queryByText("qos-shaped")).toBeNull();
    const plain = screen.getByText("vlans=10-20");
    expect(plain.className).not.toContain("blue");
    expect(plain.className).toContain("slate");
  });

  it("qos-shaped coexists with an mgmt badge, each keeping its own distinct treatment", () => {
    renderNode(["mgmt", "qos-shaped"]);
    expect(screen.getByText("mgmt").className).toContain("amber");
    expect(screen.getByText("qos-shaped").className).toContain("blue");
  });
});
