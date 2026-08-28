// SPDX-License-Identifier: Apache-2.0

// T-3501: the map must say which KIND of finding an entity carries and HOW
// BAD, not paint the single literal word "drift" (and pulse uniformly)
// regardless of source/severity. Mirrors EntityNode.mgmt.test.tsx/EntityNode
// .qos.test.tsx's exact pattern — badges[] already flows through GET
// /topology generically; this proves the new "finding:<source>:<severity>"
// token specifically renders the source word (never the literal word
// "drift" for a non-drift source), that severity is legible without colour
// (a glyph precedes the source name), and that motion is reserved for
// "error" severity.
import fullFixture from "./__fixtures__/three-node-vlan-topology.json";
import { ReactFlowProvider } from "@xyflow/react";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { FindingBadge, TopologyResponse } from "../api/types";
import { EntityNode, type EntityFlowNode } from "./EntityNode";

const full = fullFixture as unknown as TopologyResponse;
const vmbr0 = full.nodes.find((n) => n.id === "bridge:pve1:vmbr0");
if (!vmbr0) throw new Error("fixture missing bridge:pve1:vmbr0");
// Captured by value here (not re-read off `vmbr0` inside renderNode below):
// TS control-flow narrowing from the guard above does not propagate into a
// nested function's later references to the same module-scope const.
const nodeId = vmbr0.id;
const nodeLabel = vmbr0.label;
const nodeKind = vmbr0.kind;
const nodeStatus = vmbr0.status;

function renderNode(badges: string[], findings?: FindingBadge[]): void {
  const node: EntityFlowNode = {
    id: nodeId,
    type: "entity",
    position: { x: 0, y: 0 },
    data: {
      label: nodeLabel,
      kind: nodeKind,
      status: nodeStatus,
      badges,
      findings,
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

describe("EntityNode: T-3501 finding badge (source + severity)", () => {
  it("never renders the literal word 'drift' for a health finding — this task's core defect", () => {
    renderNode(["drift", "finding:health:error"]);
    expect(screen.queryByText("drift")).toBeNull();
    expect(screen.getByText("■ health")).toBeInTheDocument();
  });

  it("renders the source word for a genuine drift finding too (not just health)", () => {
    renderNode(["drift", "finding:drift:warning"]);
    expect(screen.getByText("▲ drift")).toBeInTheDocument();
  });

  it("colours an error finding red and a warning finding amber — never identically", () => {
    renderNode(["drift", "finding:health:error"]);
    expect(screen.getByText("■ health").className).toContain("red");
  });

  it("colours a warning finding amber, distinct from an error's red", () => {
    renderNode(["drift", "finding:drift:warning"]);
    const chip = screen.getByText("▲ drift");
    expect(chip.className).toContain("amber");
    expect(chip.className).not.toContain("red");
  });

  it("surfaces the finding's own detail text as the chip's hover title", () => {
    renderNode(
      ["drift", "finding:health:error"],
      [{ source: "health", severity: "error", check: "bridge_no_carrier", detail: "no carrier on enp2s0" }],
    );
    expect(screen.getByText("■ health").title).toBe("no carrier on enp2s0");
  });

  it("pulses for an error-severity finding when motion is allowed", () => {
    renderNode(["drift", "finding:health:error"]);
    const card = screen.getByRole("button", { name: nodeLabel });
    expect(card.className).toContain("animate-pulse");
    expect(card.className).toContain("border-dashed");
  });

  it("does not pulse for a warning-severity finding (only its dashed outline shows)", () => {
    renderNode(["drift", "finding:drift:warning"]);
    const card = screen.getByRole("button", { name: nodeLabel });
    expect(card.className).not.toContain("animate-pulse");
    expect(card.className).toContain("border-dashed");
  });

  it("legacy-badge-only fixtures (no finding: token) still get the dashed/pulse affordance (no regression to motionless)", () => {
    renderNode(["drift"]);
    const card = screen.getByRole("button", { name: nodeLabel });
    expect(card.className).toContain("border-dashed");
    expect(card.className).toContain("animate-pulse");
  });

  it("renders nothing finding-related when the entity carries no open finding", () => {
    renderNode(["vlans=10-20"]);
    const card = screen.getByRole("button", { name: nodeLabel });
    expect(card.className).not.toContain("border-dashed");
    expect(card.className).not.toContain("animate-pulse");
  });
});
