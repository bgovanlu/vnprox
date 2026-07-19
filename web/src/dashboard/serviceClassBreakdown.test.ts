// Pure-logic coverage for the service-network-traffic tile's breakdown
// (T-1504), independent of React/TanStack Query — see
// serviceClassBreakdown.ts's own doc comment for why this stays
// framework-free.
import { describe, expect, it } from "vitest";
import type { FlowRecord } from "../api/types";
import { computeServiceClassBreakdown } from "./serviceClassBreakdown";

function rec(overrides: Partial<FlowRecord> = {}): FlowRecord {
  return {
    at: 1000,
    node: "pve1",
    srcIp: "10.0.0.1",
    dstIp: "10.0.0.2",
    proto: 6,
    bytes: 1000,
    packets: 10,
    source: "netflow5",
    ...overrides,
  };
}

describe("computeServiceClassBreakdown", () => {
  it("buckets bytes by serviceClass and computes bytes/sec over the observed window", () => {
    const records: FlowRecord[] = [
      rec({ at: 1000, bytes: 1000, serviceClass: "corosync" }),
      rec({ at: 1010, bytes: 2000, serviceClass: "corosync" }),
      rec({ at: 1005, bytes: 500, serviceClass: "migration" }),
    ];
    const result = computeServiceClassBreakdown(records);
    expect(result).toBeDefined();
    expect(result?.windowSeconds).toBe(10); // 1010 - 1000

    const corosync = result?.entries.find((e) => e.serviceClass === "corosync");
    expect(corosync?.bytes).toBe(3000);
    expect(corosync?.bytesPerSec).toBe(300);

    const migration = result?.entries.find((e) => e.serviceClass === "migration");
    expect(migration?.bytes).toBe(500);
    expect(migration?.bytesPerSec).toBe(50);
  });

  it("sorts entries busiest-first", () => {
    const records: FlowRecord[] = [
      rec({ bytes: 100, serviceClass: "backup" }),
      rec({ bytes: 5000, serviceClass: "ceph-cluster" }),
      rec({ bytes: 2000, serviceClass: "unclassified" }),
    ];
    const result = computeServiceClassBreakdown(records);
    expect(result?.entries.map((e) => e.serviceClass)).toEqual(["ceph-cluster", "unclassified", "backup"]);
  });

  it("includes an explicit unclassified bucket (a real classifier verdict, not absence)", () => {
    const records: FlowRecord[] = [rec({ serviceClass: "unclassified", bytes: 42 })];
    const result = computeServiceClassBreakdown(records);
    expect(result?.entries).toEqual([{ serviceClass: "unclassified", bytes: 42, bytesPerSec: 42 }]);
  });

  it("excludes records with no serviceClass field at all (no FlowClassifier wired)", () => {
    const records: FlowRecord[] = [rec({ serviceClass: undefined, bytes: 999 })];
    const result = computeServiceClassBreakdown(records);
    expect(result).toBeUndefined();
  });

  it("returns undefined for an empty record set", () => {
    expect(computeServiceClassBreakdown([])).toBeUndefined();
  });

  it("floors the window at 1s to avoid a divide-by-zero/inflated rate on a single-instant sample", () => {
    const records: FlowRecord[] = [rec({ at: 500, bytes: 10, serviceClass: "corosync" })];
    const result = computeServiceClassBreakdown(records);
    expect(result?.windowSeconds).toBe(1);
    expect(result?.entries[0]?.bytesPerSec).toBe(10);
  });
});
