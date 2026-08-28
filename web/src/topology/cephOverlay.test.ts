// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import type { CephOverlay } from "../api/types";
import { attributionForNode, computeCephBadges, osdsForNode } from "./cephOverlay";

function overlay(overrides: Partial<CephOverlay> = {}): CephOverlay {
  return {
    publicNetwork: "10.20.0.0/24",
    clusterNetwork: "10.30.0.0/24",
    nodes: [],
    osds: [],
    ...overrides,
  };
}

describe("computeCephBadges", () => {
  it("paints a public and cluster badge on each node's riding-on bond", () => {
    const badges = computeCephBadges(
      overlay({
        nodes: [
          {
            node: "pve1",
            publicRidingOn: "bond:pve1:bond1",
            clusterRidingOn: "bond:pve1:bond2",
          },
        ],
      }),
    );
    expect(badges).toEqual([
      { nodeId: "bond:pve1:bond1", kind: "ceph-public" },
      { nodeId: "bond:pve1:bond2", kind: "ceph-cluster" },
    ]);
  });

  // single-nic footgun shape: one bond/NIC carries both networks — both
  // badges land on the same node id, never merged into one.
  it("paints both badges on the same node id when public and cluster share one NIC", () => {
    const badges = computeCephBadges(
      overlay({
        nodes: [{ node: "pve1", publicRidingOn: "physnic:pve1:eno1", clusterRidingOn: "physnic:pve1:eno1" }],
      }),
    );
    expect(badges).toHaveLength(2);
    expect(badges.every((b) => b.nodeId === "physnic:pve1:eno1")).toBe(true);
    expect(badges.map((b) => b.kind).sort()).toEqual(["ceph-cluster", "ceph-public"]);
  });

  it("an unresolved carrier (undeclared CIDR / ambiguous path) contributes no badge, never a guess", () => {
    const badges = computeCephBadges(overlay({ nodes: [{ node: "pve1" }] }));
    expect(badges).toHaveLength(0);
  });

  it("an empty overlay (no Ceph installed) produces no badges", () => {
    expect(computeCephBadges(overlay({ nodes: [] }))).toHaveLength(0);
  });

  it("deduplicates and sorts output deterministically", () => {
    const badges = computeCephBadges(
      overlay({
        nodes: [
          { node: "pve2", publicRidingOn: "bond:pve2:bond1" },
          { node: "pve1", publicRidingOn: "bond:pve1:bond1" },
        ],
      }),
    );
    expect(badges.map((b) => b.nodeId)).toEqual(["bond:pve1:bond1", "bond:pve2:bond1"]);
  });
});

describe("osdsForNode", () => {
  it("returns only the selected node's OSDs, sorted by id", () => {
    const o = overlay({
      osds: [
        { ref: "ceph-osd:pve1:osd2", id: 2, node: "pve1", up: true, in: true },
        { ref: "ceph-osd:pve2:osd1", id: 1, node: "pve2", up: true, in: true },
        { ref: "ceph-osd:pve1:osd0", id: 0, node: "pve1", up: true, in: true },
      ],
    });
    expect(osdsForNode(o, "pve1").map((x) => x.id)).toEqual([0, 2]);
  });

  it("a node with no OSDs returns an empty list, not undefined", () => {
    expect(osdsForNode(overlay(), "pve9")).toEqual([]);
  });
});

describe("attributionForNode", () => {
  it("finds the matching node's attribution", () => {
    const o = overlay({ nodes: [{ node: "pve1", publicMtu: 9000 }] });
    expect(attributionForNode(o, "pve1")?.publicMtu).toBe(9000);
  });

  it("returns undefined for a node hosting no OSDs", () => {
    expect(attributionForNode(overlay(), "pve9")).toBeUndefined();
  });
});
