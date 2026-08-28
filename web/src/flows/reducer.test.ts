// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import type { FlowRecord } from "../api/types";
import {
  aggregateConversations,
  emptyFlowFilter,
  flowReducer,
  initialFlowViewState,
  matchesFlowFilter,
  selectVisibleFlows,
  sortConversations,
  sortFlows,
} from "./reducer";

function rec(overrides: Partial<FlowRecord>): FlowRecord {
  return {
    at: 1000,
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

describe("flowReducer", () => {
  it("loaded replaces the buffer", () => {
    const s1 = flowReducer(initialFlowViewState, { type: "loaded", items: [rec({ at: 1 }), rec({ at: 2 })] });
    expect(s1.records).toHaveLength(2);
    const s2 = flowReducer(s1, { type: "loaded", items: [rec({ at: 3 })] });
    expect(s2.records).toHaveLength(1);
  });

  it("batch appends and tracks server-reported drops", () => {
    const s1 = flowReducer(initialFlowViewState, { type: "batch", entries: [rec({ at: 1 })], droppedTotal: 5 });
    expect(s1.records).toHaveLength(1);
    expect(s1.serverDroppedTotal).toBe(5);
    const s2 = flowReducer(s1, { type: "batch", entries: [rec({ at: 2 })], droppedTotal: 7 });
    expect(s2.records).toHaveLength(2);
    expect(s2.serverDroppedTotal).toBe(7);
  });

  it("batch with an empty entries array still updates droppedTotal", () => {
    const s1 = flowReducer(initialFlowViewState, { type: "batch", entries: [], droppedTotal: 3 });
    expect(s1.records).toHaveLength(0);
    expect(s1.serverDroppedTotal).toBe(3);
  });

  it("clear empties the buffer and resets the client drop counter", () => {
    const s1 = flowReducer(initialFlowViewState, { type: "loaded", items: [rec({})] });
    const s2 = flowReducer(s1, { type: "clear" });
    expect(s2.records).toHaveLength(0);
    expect(s2.clientDroppedTotal).toBe(0);
  });

  it("setFilter/setSort/setView patch only the named field", () => {
    const s1 = flowReducer(initialFlowViewState, { type: "setFilter", filter: { guest: "bridge:pve1:vmbr0" } });
    expect(s1.filter).toEqual({ ...emptyFlowFilter, guest: "bridge:pve1:vmbr0" });
    const s2 = flowReducer(s1, { type: "setSort", sort: "bytes" });
    expect(s2.sort).toBe("bytes");
    const s3 = flowReducer(s2, { type: "setView", view: "conversations" });
    expect(s3.view).toBe("conversations");
  });
});

describe("matchesFlowFilter / selectVisibleFlows", () => {
  const recordA = rec({ srcRef: "bridge:pve1:vmbr0", dstRef: "sdn-vnet::z/v100", vlan: 100, srcPort: 51000, dstPort: 443, proto: 6 });
  const recordB = rec({ srcIp: "10.1.1.5", dstIp: "10.1.1.50", proto: 17, srcPort: 34567, dstPort: 53 });
  const records = [recordA, recordB];

  it("guest filter matches either srcRef or dstRef exactly", () => {
    expect(matchesFlowFilter(recordA, { ...emptyFlowFilter, guest: "sdn-vnet::z/v100" })).toBe(true);
    expect(matchesFlowFilter(recordA, { ...emptyFlowFilter, guest: "bridge:pve1:vmbr0" })).toBe(true);
    expect(matchesFlowFilter(recordB, { ...emptyFlowFilter, guest: "bridge:pve1:vmbr0" })).toBe(false);
  });

  it("vlan filter matches exactly", () => {
    expect(matchesFlowFilter(recordA, { ...emptyFlowFilter, vlan: "100" })).toBe(true);
    expect(matchesFlowFilter(recordB, { ...emptyFlowFilter, vlan: "100" })).toBe(false);
  });

  it("subnet filter (CIDR) matches either srcIp or dstIp within it", () => {
    expect(matchesFlowFilter(recordB, { ...emptyFlowFilter, subnet: "10.1.1.0/24" })).toBe(true);
    expect(matchesFlowFilter(recordA, { ...emptyFlowFilter, subnet: "10.1.1.0/24" })).toBe(false);
  });

  it("port filter matches either srcPort or dstPort", () => {
    expect(matchesFlowFilter(recordA, { ...emptyFlowFilter, port: "443" })).toBe(true);
    expect(matchesFlowFilter(recordA, { ...emptyFlowFilter, port: "51000" })).toBe(true);
    expect(matchesFlowFilter(recordA, { ...emptyFlowFilter, port: "22" })).toBe(false);
  });

  it("protocol filter accepts a name or a raw number", () => {
    expect(matchesFlowFilter(recordA, { ...emptyFlowFilter, protocol: "tcp" })).toBe(true);
    expect(matchesFlowFilter(recordA, { ...emptyFlowFilter, protocol: "6" })).toBe(true);
    expect(matchesFlowFilter(recordB, { ...emptyFlowFilter, protocol: "tcp" })).toBe(false);
  });

  it("an unrecognized protocol name/number matches nothing (never a throw)", () => {
    expect(matchesFlowFilter(recordA, { ...emptyFlowFilter, protocol: "bogus" })).toBe(false);
  });

  it("filters are ANDed together", () => {
    expect(matchesFlowFilter(recordA, { ...emptyFlowFilter, vlan: "100", protocol: "tcp" })).toBe(true);
    expect(matchesFlowFilter(recordA, { ...emptyFlowFilter, vlan: "100", protocol: "udp" })).toBe(false);
  });

  it("selectVisibleFlows returns everything when no filter field is set", () => {
    const state = { ...initialFlowViewState, records };
    expect(selectVisibleFlows(state)).toHaveLength(2);
  });

  it("selectVisibleFlows narrows by the active filter", () => {
    const state = { ...initialFlowViewState, records, filter: { ...emptyFlowFilter, protocol: "udp" } };
    expect(selectVisibleFlows(state)).toEqual([recordB]);
  });
});

describe("sortFlows", () => {
  const records = [rec({ at: 1, bytes: 100, packets: 5 }), rec({ at: 3, bytes: 300, packets: 1 }), rec({ at: 2, bytes: 200, packets: 9 })];

  it("recency sorts newest-first", () => {
    expect(sortFlows(records, "recency").map((r) => r.at)).toEqual([3, 2, 1]);
  });
  it("bytes sorts largest-first", () => {
    expect(sortFlows(records, "bytes").map((r) => r.bytes)).toEqual([300, 200, 100]);
  });
  it("packets sorts largest-first", () => {
    expect(sortFlows(records, "packets").map((r) => r.packets)).toEqual([9, 5, 1]);
  });
  it("never mutates its input", () => {
    const copy = [...records];
    sortFlows(records, "bytes");
    expect(records).toEqual(copy);
  });
});

describe("aggregateConversations / sortConversations", () => {
  it("groups by directed (srcRef||srcIp, dstRef||dstIp) pair, summing bytes/packets", () => {
    const records = [
      rec({ srcRef: "a", dstRef: "b", bytes: 100, packets: 1, at: 1 }),
      rec({ srcRef: "a", dstRef: "b", bytes: 200, packets: 2, at: 2 }),
      rec({ srcRef: "a", dstRef: "c", bytes: 50, packets: 1, at: 3 }),
    ];
    const rows = aggregateConversations(records);
    expect(rows).toHaveLength(2);
    const ab = rows.find((r) => r.key === "a=>b");
    expect(ab).toMatchObject({ bytes: 300, packets: 3, recordCount: 2, lastAt: 2 });
  });

  it("falls back to raw IPs when a record has no resolved ref", () => {
    const records = [rec({ srcRef: undefined, dstRef: undefined, srcIp: "10.0.0.5", dstIp: "10.0.0.10" })];
    const rows = aggregateConversations(records);
    expect(rows).toEqual([
      expect.objectContaining({ key: "10.0.0.5=>10.0.0.10", srcIp: "10.0.0.5", dstIp: "10.0.0.10" }),
    ]);
  });

  it("returns [] for an empty input", () => {
    expect(aggregateConversations([])).toEqual([]);
  });

  it("sortConversations mirrors sortFlows' vocabulary", () => {
    const rows = aggregateConversations([
      rec({ srcRef: "a", dstRef: "b", bytes: 100, packets: 9, at: 1 }),
      rec({ srcRef: "c", dstRef: "d", bytes: 300, packets: 1, at: 5 }),
    ]);
    expect(sortConversations(rows, "bytes").map((r) => r.key)).toEqual(["c=>d", "a=>b"]);
    expect(sortConversations(rows, "packets").map((r) => r.key)).toEqual(["a=>b", "c=>d"]);
    expect(sortConversations(rows, "recency").map((r) => r.key)).toEqual(["c=>d", "a=>b"]);
  });
});
