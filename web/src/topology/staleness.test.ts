// SPDX-License-Identifier: Apache-2.0

// Unit tests for the §5 staleness projection (docs/features/topology.md §5,
// docs/api.md's GET /topology staleness section). The stale fixture's
// staleness object was captured from a live vnproxd (three-node-vlan
// pvemock killed mid-run, so the "pve" source is genuinely stale with a
// real lastError) — see __fixtures__/three-node-vlan-topology-stale.json.
import staleFixture from "./__fixtures__/three-node-vlan-topology-stale.json";
import { describe, expect, it } from "vitest";
import type { Staleness, TopologyResponse } from "../api/types";
import { describeLastSuccess, describeScope, summarizeStaleness } from "./staleness";

const stale = staleFixture as unknown as TopologyResponse;

describe("summarizeStaleness", () => {
  it("treats an absent staleness section (no collector status) as healthy", () => {
    const s = summarizeStaleness(undefined);
    expect(s.stale).toBe(false);
    expect(s.clusterWide).toBe(false);
    expect(s.staleNodeGroups.size).toBe(0);
    expect(s.staleSources).toHaveLength(0);
  });

  it("treats a fully healthy staleness section as nothing to report", () => {
    const healthy: Staleness = {
      stale: false,
      sources: [
        { name: "pve", stale: false, lastSuccess: 1720512345 },
        { name: "host", node: "pve1", stale: false, lastSuccess: 1720512345 },
      ],
    };
    const s = summarizeStaleness(healthy);
    expect(s.stale).toBe(false);
    expect(s.staleSources).toHaveLength(0);
  });

  it("flags cluster-wide staleness from a node-less stale source (captured fixture: dead pvemock)", () => {
    const s = summarizeStaleness(stale.staleness);
    expect(s.stale).toBe(true);
    // The fixture's "pve" source is stale and has no node scope.
    expect(s.clusterWide).toBe(true);
    expect(s.staleSources.map((src) => src.name)).toContain("pve");
  });

  it("collects node-scoped stale sources into staleNodeGroups (fixture: lldp on pve1)", () => {
    const s = summarizeStaleness(stale.staleness);
    expect(s.staleNodeGroups.has("pve1")).toBe(true);
    // The fixture's "host" source on pve1 is NOT stale — only stale
    // sources contribute, so pve1 is in the set solely because of lldp.
    expect(s.staleSources.some((src) => src.name === "host")).toBe(false);
  });

  it("does not grey any band for a purely cluster-wide staleness", () => {
    const clusterOnly: Staleness = {
      stale: true,
      sources: [{ name: "pve", stale: true, lastSuccess: 1720512345, lastError: "connection refused" }],
    };
    const s = summarizeStaleness(clusterOnly);
    expect(s.clusterWide).toBe(true);
    expect(s.staleNodeGroups.size).toBe(0);
  });
});

describe("describeLastSuccess", () => {
  it("renders the last-success timestamp (§5: 'staleness banner and timestamp')", () => {
    const text = describeLastSuccess({ name: "pve", stale: true, lastSuccess: 1720512345 });
    expect(text).toContain("last successful data");
    expect(text).toContain(new Date(1720512345 * 1000).toLocaleString());
  });

  it("says so when no poll has ever succeeded (lastSuccess absent)", () => {
    expect(describeLastSuccess({ name: "pve", stale: true })).toBe("no successful poll yet");
  });
});

describe("describeScope", () => {
  it("names the node for node-scoped sources and the cluster otherwise", () => {
    expect(describeScope({ name: "host", node: "pve2", stale: true })).toBe("node pve2");
    expect(describeScope({ name: "pve", stale: true })).toBe("whole cluster");
  });
});
