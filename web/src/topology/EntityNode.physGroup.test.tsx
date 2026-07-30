// T-1907 acceptance criterion 4 (graph-view render test): a "phys-group:
// <node>" per-node physical-layer summary pill (internal/topology/
// collapse_physical.go) renders with the exact same "pill" treatment
// (rounded-full, "click to expand" hint) a guest-group pill already gets —
// and, unlike a guest-group pill, is keyboard-reachable via the same
// tabIndex=0/role="button" every entity already has (proven directly here,
// not just asserted by code inspection).
import { ReactFlowProvider } from "@xyflow/react";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { EntityNode, type EntityFlowNode, type EntityNodeData } from "./EntityNode";

function renderNode(data: EntityNodeData): void {
  const node: EntityFlowNode = { id: "phys-group:pve1", type: "entity", position: { x: 0, y: 0 }, data };
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

describe("EntityNode: T-1907 phys-group pill", () => {
  const pillData: EntityNodeData = {
    label: "10 NICs",
    kind: "phys-group",
    status: "ok",
    badges: ["count=10"],
    dimmed: false,
    highlighted: false,
    isGuestGroup: false,
    isPhysGroup: true,
    collapsedCount: 10,
  };

  it("renders the pill (rounded-full) shape and the 'click to expand' hint, same as a guest-group pill", () => {
    renderNode(pillData);
    const btn = screen.getByRole("button", { name: "10 NICs" });
    expect(btn.className).toContain("rounded-full");
    expect(screen.getByText("click to expand")).toBeInTheDocument();
  });

  it("is keyboard-reachable: focusable (tabIndex 0) and activatable like any other entity", () => {
    renderNode(pillData);
    const btn = screen.getByRole("button", { name: "10 NICs" });
    expect(btn).toHaveAttribute("tabindex", "0");
    expect(btn).toHaveAttribute("data-entity-ref", "phys-group:pve1");
  });

  it("does not render the kind label chip a normal (non-pill) entity gets", () => {
    renderNode(pillData);
    expect(screen.queryByText("phys-group")).not.toBeInTheDocument();
  });

  it("a normal physnic entity (isPhysGroup false) keeps the plain box, non-pill treatment", () => {
    renderNode({ ...pillData, kind: "physnic", label: "eno1", isPhysGroup: false, collapsedCount: undefined });
    const btn = screen.getByRole("button", { name: "eno1" });
    expect(btn.className).not.toContain("rounded-full");
    expect(screen.queryByText("click to expand")).not.toBeInTheDocument();
  });
});
