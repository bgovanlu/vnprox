import { describe, expect, it } from "vitest";
import type { SimHop, TopologyEdge } from "../api/types";
import { computePathHighlight, withVerifyHighlight } from "./pathHighlight";

function hop(ref: string | undefined, kind = "bridge"): SimHop {
  return { ref, kind, label: ref ?? kind };
}

function edge(from: string, to: string, kind = "attached-to"): TopologyEdge {
  return { from, to, kind, status: "ok", badges: [] };
}

describe("computePathHighlight", () => {
  it("collects every hop with a real ref into nodeIds, skipping synthetic hops", () => {
    const hops = [hop("guest-nic:pve1:100/net0"), hop(undefined, "external"), hop("bridge:pve1:vmbr0")];
    const result = computePathHighlight(hops, [], "allow");
    expect(result.nodeIds).toEqual(new Set(["guest-nic:pve1:100/net0", "bridge:pve1:vmbr0"]));
    expect(result.verdict).toBe("allow");
  });

  it("matches consecutive hops to topology edges regardless of edge direction", () => {
    const hops = [hop("guest-nic:pve1:100/net0"), hop("bridge:pve1:vmbr0"), hop("guest-nic:pve1:101/net0")];
    const edges = [
      edge("bridge:pve1:vmbr0", "guest-nic:pve1:100/net0"), // reversed vs. the hop order
      edge("guest-nic:pve1:101/net0", "bridge:pve1:vmbr0"),
      edge("bridge:pve1:vmbr0", "irrelevant-node"), // not between consecutive hops
    ];
    const result = computePathHighlight(hops, edges, "deny");
    expect(result.edgeIds.size).toBe(2);
    expect(result.edgeIds.has("bridge:pve1:vmbr0=>guest-nic:pve1:100/net0::attached-to")).toBe(true);
    expect(result.edgeIds.has("guest-nic:pve1:101/net0=>bridge:pve1:vmbr0::attached-to")).toBe(true);
  });

  it("never invents an edge between non-consecutive or unreffed hops", () => {
    const hops = [hop("a"), hop(undefined), hop("b")];
    const edges = [edge("a", "b")];
    const result = computePathHighlight(hops, edges, "allow");
    expect(result.edgeIds.size).toBe(0);
  });

  it("marks the missing-link break point and folds it into nodeIds even off-path", () => {
    const hops = [hop("guest-nic:pve1:200/net0")];
    const result = computePathHighlight(hops, [], "unreachable", {
      code: "vlan_not_trunked",
      message: "VLAN 100 is not trunked on bond0 of node pve2",
      atRef: "bridge:pve2:vmbr0",
      atNode: "pve2",
    });
    expect(result.missingNodeIds).toEqual(new Set(["bridge:pve2:vmbr0"]));
    expect(result.nodeIds.has("bridge:pve2:vmbr0")).toBe(true);
  });

  it("marks the blocking node distinctly from the rest of the path", () => {
    const hops = [hop("guest-nic:pve1:100/net0"), hop("guest-nic:pve1:101/net0")];
    const result = computePathHighlight(hops, [], "deny", undefined, "guest:pve1:101");
    expect(result.blockingNodeId).toBe("guest:pve1:101");
  });

  it("returns empty sets for an empty hop list (indeterminate results before any hop is traced)", () => {
    const result = computePathHighlight([], [], "indeterminate");
    expect(result.nodeIds.size).toBe(0);
    expect(result.edgeIds.size).toBe(0);
    expect(result.missingNodeIds.size).toBe(0);
  });
});

describe("withVerifyHighlight", () => {
  it("leaves the base highlight untouched when no src node id is given (no verify result yet)", () => {
    const base = computePathHighlight([hop("guest-nic:pve1:100/net0")], [], "allow");
    const merged = withVerifyHighlight(base, undefined, "reachable", false);
    expect(merged).toBe(base);
  });

  it("adds the verify fields onto a copy of the base highlight without mutating it", () => {
    const base = computePathHighlight([hop("guest-nic:pve1:100/net0")], [], "deny");
    const merged = withVerifyHighlight(base, "guest-nic:pve1:100/net0", "reachable", true);
    expect(merged.verifyNodeId).toBe("guest-nic:pve1:100/net0");
    expect(merged.verifyOutcome).toBe("reachable");
    expect(merged.verifyDiverges).toBe(true);
    expect(base.verifyNodeId).toBeUndefined();
  });

  it("carries verifyDiverges: false through for a matching (non-divergent) result", () => {
    const base = computePathHighlight([hop("guest-nic:pve1:100/net0")], [], "allow");
    const merged = withVerifyHighlight(base, "guest-nic:pve1:100/net0", "reachable", false);
    expect(merged.verifyDiverges).toBe(false);
  });
});
