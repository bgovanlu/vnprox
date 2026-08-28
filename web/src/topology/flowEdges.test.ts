// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import type { FlowRecord } from "../api/types";
import { computeFlowEdges, flowEdgeId, flowEdgeStrokeWidth } from "./flowEdges";

function rec(overrides: Partial<FlowRecord>): FlowRecord {
  return {
    at: 1_000,
    node: "pve1",
    srcIp: "10.0.0.5",
    dstIp: "10.0.0.10",
    proto: 6,
    bytes: 1000,
    packets: 10,
    source: "netflow5",
    ...overrides,
  };
}

describe("computeFlowEdges", () => {
  it("returns [] for no records (the empty/no-active-flows case)", () => {
    expect(computeFlowEdges({ records: [], now: 1_000 })).toEqual([]);
  });

  it("drops records with no resolved srcRef/dstRef (never guessed)", () => {
    const records = [rec({ srcRef: undefined, dstRef: "bridge:pve1:vmbr0" }), rec({ srcRef: "bridge:pve1:vmbr0", dstRef: undefined })];
    expect(computeFlowEdges({ records, now: 1_000 })).toEqual([]);
  });

  it("drops same-ref (self-loop) conversations — no distinct second endpoint to draw", () => {
    const records = [rec({ srcRef: "bridge:pve1:vmbr0", dstRef: "bridge:pve1:vmbr0" })];
    expect(computeFlowEdges({ records, now: 1_000 })).toEqual([]);
  });

  it("aggregates multiple records for the same directed pair (bytes/packets summed, count tracked)", () => {
    const records = [
      rec({ srcRef: "bridge:pve1:vmbr0", dstRef: "sdn-vnet::z/v100", bytes: 1000, packets: 10, at: 945 }),
      rec({ srcRef: "bridge:pve1:vmbr0", dstRef: "sdn-vnet::z/v100", bytes: 2000, packets: 20, at: 950 }),
    ];
    const edges = computeFlowEdges({ records, now: 1_000, windowSeconds: 60 });
    expect(edges).toHaveLength(1);
    expect(edges[0]).toMatchObject({
      id: flowEdgeId("bridge:pve1:vmbr0", "sdn-vnet::z/v100"),
      from: "bridge:pve1:vmbr0",
      to: "sdn-vnet::z/v100",
      bytes: 3000,
      packets: 30,
      recordCount: 2,
      lastAt: 950,
    });
    expect(edges[0]?.bytesPerSec).toBeCloseTo(50, 5); // 3000 / 60
  });

  it("keeps a->b and b->a as distinct directed edges", () => {
    const records = [
      rec({ srcRef: "bridge:pve1:vmbr0", dstRef: "sdn-vnet::z/v100" }),
      rec({ srcRef: "sdn-vnet::z/v100", dstRef: "bridge:pve1:vmbr0" }),
    ];
    const edges = computeFlowEdges({ records, now: 1_000 });
    expect(edges.map((e) => e.id).sort()).toEqual(
      [flowEdgeId("bridge:pve1:vmbr0", "sdn-vnet::z/v100"), flowEdgeId("sdn-vnet::z/v100", "bridge:pve1:vmbr0")].sort(),
    );
  });

  it("excludes records outside the active window", () => {
    const records = [
      rec({ srcRef: "a", dstRef: "b", at: 950 }), // within 60s of now=1000
      rec({ srcRef: "a", dstRef: "b", at: 800 }), // outside
    ];
    const edges = computeFlowEdges({ records, now: 1_000, windowSeconds: 60 });
    expect(edges).toHaveLength(1);
    expect(edges[0]?.recordCount).toBe(1);
    expect(edges[0]?.lastAt).toBe(950);
  });

  it("restricts to nodeIds when provided, dropping edges with an endpoint not on the canvas", () => {
    const records = [rec({ srcRef: "a", dstRef: "b" }), rec({ srcRef: "a", dstRef: "c" })];
    const edges = computeFlowEdges({ records, now: 1_000, nodeIds: new Set(["a", "b"]) });
    expect(edges.map((e) => e.id)).toEqual([flowEdgeId("a", "b")]);
  });

  it("sorts output deterministically by id regardless of input order", () => {
    const records = [rec({ srcRef: "z", dstRef: "y" }), rec({ srcRef: "a", dstRef: "b" })];
    const edges = computeFlowEdges({ records, now: 1_000 });
    expect(edges.map((e) => e.id)).toEqual([flowEdgeId("a", "b"), flowEdgeId("z", "y")]);
  });
});

describe("flowEdgeStrokeWidth", () => {
  it("returns the minimum width for zero/negative/non-finite input", () => {
    expect(flowEdgeStrokeWidth(0)).toBe(1.5);
    expect(flowEdgeStrokeWidth(-5)).toBe(1.5);
    expect(flowEdgeStrokeWidth(Number.NaN)).toBe(1.5);
  });

  it("increases monotonically with bytesPerSec", () => {
    const low = flowEdgeStrokeWidth(10);
    const mid = flowEdgeStrokeWidth(10_000);
    const high = flowEdgeStrokeWidth(1_000_000);
    expect(low).toBeLessThan(mid);
    expect(mid).toBeLessThan(high);
  });

  it("clamps at the maximum for very large bytesPerSec", () => {
    expect(flowEdgeStrokeWidth(1_000_000)).toBeCloseTo(5, 5);
    expect(flowEdgeStrokeWidth(1_000_000_000)).toBeCloseTo(5, 5);
  });
});
