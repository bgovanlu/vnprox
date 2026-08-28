// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import type { MTUProbeResult } from "../api/types";
import { computeMTUOverlayEdges, formatMTUBadgeLabel } from "./mtuOverlay";

function result(overrides: Partial<MTUProbeResult>): MTUProbeResult {
  return {
    linkId: "guest:vmbr0|pve1->pve2",
    fabric: "guest",
    fromNode: "pve1",
    toNode: "pve2",
    mtu: 1450,
    at: 100,
    probeCount: 8,
    ...overrides,
  };
}

describe("computeMTUOverlayEdges", () => {
  const nodeIdForName = (name: string): string | undefined => {
    const known: Record<string, string> = { pve1: "node:pve1", pve2: "node:pve2", pve3: "node:pve3" };
    return known[name];
  };

  // AC2 (three-node-vlan/evpn-lab-style fixture): a probed link renders a
  // verified-MTU badge whose value is the measured reading — distinct from
  // (i.e. never merged with, never overwriting) wherever a "configured"
  // MTU value is shown elsewhere. This test asserts the badge carries
  // exactly the probed value, formatted as its own labeled element, not a
  // bare number a "configured MTU" display could be mistaken for.
  it("a probed link renders a verified-MTU badge carrying the measured value", () => {
    const edges = computeMTUOverlayEdges([result({ mtu: 1450 })], nodeIdForName);
    expect(edges).toHaveLength(1);
    const [badge] = edges;
    if (!badge) throw new Error("expected exactly one badge");
    expect(badge).toMatchObject({
      id: "guest:vmbr0|pve1->pve2",
      from: "node:pve1",
      to: "node:pve2",
      mtu: 1450,
    });
    expect(formatMTUBadgeLabel(badge.mtu)).toBe("MTU 1450");
    // Distinctness: the badge label is never a bare configured-style
    // number (e.g. a zone editor might just show "1450") — it always
    // carries a "MTU" qualifier so it reads as a separate, verified-only
    // annotation wherever it's rendered alongside a configured value.
    expect(formatMTUBadgeLabel(badge.mtu)).not.toBe(String(badge.mtu));
  });

  // AC2's other half: a path with no probe result yet — simply absent from
  // GET /mtuprobe/results' items — produces no badge at all, never a
  // stale/zero placeholder.
  it("a link with no probe result yet produces no badge (not a stale/zero value)", () => {
    // Only pve1<->pve2 has a reading; pve1<->pve3 has never been probed and
    // is simply not present in `results` at all (Service.Results' own
    // contract) — computeMTUOverlayEdges must not synthesize an entry for
    // it from node adjacency or any other source.
    const edges = computeMTUOverlayEdges([result({})], nodeIdForName);
    expect(edges.find((e) => e.to === "node:pve3")).toBeUndefined();
    expect(edges).toHaveLength(1);
  });

  it("an empty results list renders no badges at all", () => {
    expect(computeMTUOverlayEdges([], nodeIdForName)).toHaveLength(0);
  });

  it("drops a link whose endpoint isn't currently rendered on the map", () => {
    const edges = computeMTUOverlayEdges([result({ toNode: "pve9" })], nodeIdForName);
    expect(edges).toHaveLength(0);
  });

  it("drops a link that resolves to the same node on both ends", () => {
    const edges = computeMTUOverlayEdges([result({ toNode: "pve1" })], nodeIdForName);
    expect(edges).toHaveLength(0);
  });

  it("sorts output by id for deterministic rendering", () => {
    const edges = computeMTUOverlayEdges(
      [
        result({ linkId: "guest:vmbr1|pve1->pve2", toNode: "pve2" }),
        result({ linkId: "corosync:ring0|pve1->pve2", toNode: "pve2" }),
      ],
      nodeIdForName,
    );
    expect(edges.map((e) => e.id)).toEqual(["corosync:ring0|pve1->pve2", "guest:vmbr1|pve1->pve2"]);
  });
});

describe("formatMTUBadgeLabel", () => {
  it("formats as 'MTU <value>'", () => {
    expect(formatMTUBadgeLabel(9000)).toBe("MTU 9000");
    expect(formatMTUBadgeLabel(1500)).toBe("MTU 1500");
  });
});
