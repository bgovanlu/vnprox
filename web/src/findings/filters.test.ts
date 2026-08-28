// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { EMPTY_FILTER, filterFindings, nodesIn } from "./filters";
import type { StreamFinding } from "../api/types";

function finding(partial: Partial<StreamFinding> & Pick<StreamFinding, "id">): StreamFinding {
  return {
    source: "drift",
    check: "bridge_divergence",
    severity: "warning",
    detail: "d",
    nodes: [],
    fixable: false,
    ...partial,
  };
}

const sample: StreamFinding[] = [
  finding({ id: "drift:1", source: "drift", severity: "warning", nodes: ["pve1"] }),
  finding({ id: "lldp:1", source: "lldp", severity: "warning", nodes: ["pve2"] }),
  finding({ id: "health:1", source: "health", severity: "error", nodes: ["pve1"] }),
  finding({ id: "ipam:1", source: "ipam", severity: "info", nodes: ["pve3"] }),
];

describe("filterFindings", () => {
  it("returns everything when the filter is empty (AC2 baseline)", () => {
    expect(filterFindings(sample, EMPTY_FILTER)).toHaveLength(4);
  });

  it("filters by source alone", () => {
    const got = filterFindings(sample, { ...EMPTY_FILTER, source: "lldp" });
    expect(got.map((f) => f.id)).toEqual(["lldp:1"]);
  });

  it("filters by severity alone", () => {
    const got = filterFindings(sample, { ...EMPTY_FILTER, severity: "error" });
    expect(got.map((f) => f.id)).toEqual(["health:1"]);
  });

  it("filters by node alone (a node can carry findings from multiple sources)", () => {
    const got = filterFindings(sample, { ...EMPTY_FILTER, node: "pve1" });
    expect(got.map((f) => f.id).sort()).toEqual(["drift:1", "health:1"]);
  });

  it("AND-combines multiple filters", () => {
    const got = filterFindings(sample, { source: "health", severity: "error", node: "pve1" });
    expect(got.map((f) => f.id)).toEqual(["health:1"]);
  });

  it("returns nothing when filters don't intersect", () => {
    const got = filterFindings(sample, { ...EMPTY_FILTER, source: "drift", node: "pve2" });
    expect(got).toHaveLength(0);
  });

  it("works uniformly across every source — no source gets special-cased", () => {
    for (const src of ["drift", "lldp", "ipam", "health"] as const) {
      const got = filterFindings(sample, { ...EMPTY_FILTER, source: src });
      expect(got.every((f) => f.source === src)).toBe(true);
      expect(got.length).toBeGreaterThan(0);
    }
  });
});

describe("nodesIn", () => {
  it("returns every distinct node, sorted", () => {
    expect(nodesIn(sample)).toEqual(["pve1", "pve2", "pve3"]);
  });

  it("returns an empty array for no findings", () => {
    expect(nodesIn([])).toEqual([]);
  });
});
