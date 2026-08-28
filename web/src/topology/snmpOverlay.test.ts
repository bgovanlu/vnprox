// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import type { SNMPCounterResult } from "../api/types";
import { computeSNMPCounterOverlayEdges, formatSNMPCounterBadgeLabel } from "./snmpOverlay";

function result(overrides: Partial<SNMPCounterResult>): SNMPCounterResult {
  return {
    chassisId: "aa:bb:cc:dd:ee:ff",
    switchName: "tor-1",
    node: "pve1",
    localIface: "eth0",
    switchPort: "24",
    state: "ok",
    inErrors: 5,
    outErrors: 1,
    inDiscards: 2,
    outDiscards: 0,
    operUp: true,
    at: 100,
    ...overrides,
  };
}

describe("computeSNMPCounterOverlayEdges", () => {
  const edgeIdForPort = (chassisId: string, localIface: string): { from: string; to: string } | undefined => {
    if (chassisId === "aa:bb:cc:dd:ee:ff" && localIface === "eth0") {
      return { from: "node:pve1:eth0", to: "switch:aa:bb:cc:dd:ee:ff" };
    }
    return undefined;
  };

  it("an ok result renders a badge carrying the counter values", () => {
    const edges = computeSNMPCounterOverlayEdges([result({})], edgeIdForPort);
    expect(edges).toHaveLength(1);
    const [badge] = edges;
    if (!badge) throw new Error("expected exactly one badge");
    expect(badge).toMatchObject({
      id: "aa:bb:cc:dd:ee:ff|eth0",
      from: "node:pve1:eth0",
      to: "switch:aa:bb:cc:dd:ee:ff",
      state: "ok",
      inErrors: 5,
      outErrors: 1,
    });
    expect(formatSNMPCounterBadgeLabel(badge)).toBe("ERR 6 / DISC 2");
  });

  // Honest states: every one of the three non-"ok" states still produces a
  // badge — this function does not filter by state, it only resolves
  // endpoints. A rendering layer decides how to draw each state, but the
  // fact of "this edge has a poll relationship" is never hidden.
  it.each(["not_configured", "unreachable", "no_counters"] as const)(
    "a %s result still produces a badge (state is a rendering decision, not a filter)",
    (state) => {
      const edges = computeSNMPCounterOverlayEdges([result({ state, inErrors: undefined, outErrors: undefined })], edgeIdForPort);
      expect(edges).toHaveLength(1);
      expect(edges[0]?.state).toBe(state);
    },
  );

  it("a port the resolver can't place on the current canvas produces no badge", () => {
    const edges = computeSNMPCounterOverlayEdges([result({ localIface: "eth9" })], edgeIdForPort);
    expect(edges).toHaveLength(0);
  });

  it("an empty results list renders no badges at all", () => {
    expect(computeSNMPCounterOverlayEdges([], edgeIdForPort)).toHaveLength(0);
  });

  it("drops an edge that resolves to the same node on both ends", () => {
    const sameNode = (): { from: string; to: string } => ({ from: "node:pve1", to: "node:pve1" });
    const edges = computeSNMPCounterOverlayEdges([result({})], sameNode);
    expect(edges).toHaveLength(0);
  });

  it("sorts output by id for deterministic rendering", () => {
    const resolver = (chassisId: string, localIface: string): { from: string; to: string } => ({
      from: `node:${localIface}`,
      to: `switch:${chassisId}`,
    });
    const edges = computeSNMPCounterOverlayEdges(
      [result({ chassisId: "bb", localIface: "eth1" }), result({ chassisId: "aa", localIface: "eth0" })],
      resolver,
    );
    expect(edges.map((e) => e.id)).toEqual(["aa|eth0", "bb|eth1"]);
  });
});

describe("formatSNMPCounterBadgeLabel", () => {
  const resolveAny = (): { from: string; to: string } => ({ from: "node:a", to: "switch:b" });

  it("sums in+out errors and in+out discards", () => {
    const badge = computeSNMPCounterOverlayEdges(
      [result({ inErrors: 3, outErrors: 4, inDiscards: 1, outDiscards: 2 })],
      resolveAny,
    )[0];
    if (!badge) throw new Error("expected a badge");
    expect(formatSNMPCounterBadgeLabel(badge)).toBe("ERR 7 / DISC 3");
  });

  it("treats missing counters as zero", () => {
    const badge = computeSNMPCounterOverlayEdges(
      [result({ state: "not_configured", inErrors: undefined, outErrors: undefined, inDiscards: undefined, outDiscards: undefined })],
      resolveAny,
    )[0];
    if (!badge) throw new Error("expected a badge");
    expect(formatSNMPCounterBadgeLabel(badge)).toBe("ERR 0 / DISC 0");
  });
});
