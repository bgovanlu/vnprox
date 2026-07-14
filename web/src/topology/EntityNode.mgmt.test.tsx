// T-702 acceptance criterion 4 (graph-view render test): the mgmt/corosync/
// mgmt-path badge vocabulary (docs/features/topology.md §3) gets a distinct
// (amber) treatment in EntityNode, additive to the plain-grey treatment
// every other badge already gets — proven directly against the real
// three-node-vlan fixture's vmbr0 node shape (bridge, badges augmented with
// "mgmt" the way ApplyMgmtBadges/paintMgmtStatus would).
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
        // The remaining NodeProps fields are unused by EntityNode's own
        // rendering (it only reads `data`/`selected`); minimal stand-ins.
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

describe("EntityNode: T-702 mgmt badge treatment", () => {
  it("renders vmbr0's real three-node-vlan badges plus mgmt/mgmt-path with the amber marker treatment", () => {
    const vmbr0 = full.nodes.find((n) => n.id === "bridge:pve1:vmbr0");
    if (!vmbr0) throw new Error("bridge:pve1:vmbr0 missing from three-node-vlan fixture");
    renderNode([...vmbr0.badges, "mgmt", "corosync"]);

    const mgmtBadge = screen.getByText("mgmt");
    expect(mgmtBadge.className).toContain("amber");
    const corosyncBadge = screen.getByText("corosync");
    expect(corosyncBadge.className).toContain("amber");
  });

  it("leaves a plain badge (e.g. vlans=...) with the original grey treatment", () => {
    renderNode(["vlans=10-20", "mgmt"]);
    const plain = screen.getByText("vlans=10-20");
    expect(plain.className).not.toContain("amber");
    expect(plain.className).toContain("slate");
  });

  it("mgmt-path gets the same amber treatment as mgmt/corosync on a non-carrier path member (e.g. a bond)", () => {
    renderNode(["mode=802.3ad", "mgmt-path"]);
    const pathBadge = screen.getByText("mgmt-path");
    expect(pathBadge.className).toContain("amber");
  });
});
