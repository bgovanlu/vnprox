// SPDX-License-Identifier: Apache-2.0

// Lab-simulated fixtures: T-4110 is hardware-flagged (no real switch to
// observe against — see this file's imported module's own header comment
// and planning/reports/needs-hardware-validation.md's T-4110 entry), so
// every FlowRecord/HashSlave below is hand-constructed, never captured
// from real traffic. Labeled as such, not presented as an observation.
import { describe, expect, it } from "vitest";
import type { FlowRecord } from "../api/types";
import {
  isLacpHashPolicy,
  isPredictablePolicy,
  predictSlaveDistribution,
  type HashSlave,
} from "./lacpHashPredict";

function rec(over: Partial<FlowRecord> = {}): FlowRecord {
  return {
    at: 1_700_000_000,
    node: "pve1",
    srcIp: "10.0.0.1",
    dstIp: "10.0.0.2",
    proto: 6,
    bytes: 100,
    packets: 1,
    source: "fixture",
    ...over,
  };
}

const slaves: HashSlave[] = [
  { ref: "physnic:pve1:eno1", name: "eno1", up: true },
  { ref: "physnic:pve1:eno2", name: "eno2", up: true },
];

describe("isLacpHashPolicy / isPredictablePolicy", () => {
  it("recognizes all five kernel policies", () => {
    for (const p of ["layer2", "layer2+3", "layer3+4", "encap2+3", "encap3+4"]) {
      expect(isLacpHashPolicy(p)).toBe(true);
    }
    expect(isLacpHashPolicy("layer5")).toBe(false);
    expect(isLacpHashPolicy("")).toBe(false);
  });

  it("only layer3+4/encap3+4 are predictable from flow-record data", () => {
    expect(isPredictablePolicy("layer3+4")).toBe(true);
    expect(isPredictablePolicy("encap3+4")).toBe(true);
    expect(isPredictablePolicy("layer2")).toBe(false);
    expect(isPredictablePolicy("layer2+3")).toBe(false);
    expect(isPredictablePolicy("encap2+3")).toBe(false);
  });
});

describe("predictSlaveDistribution", () => {
  it("buckets deterministically and weighs by bytes, not just flow count", () => {
    const records = [
      rec({ srcIp: "10.0.0.1", dstIp: "10.0.0.2", srcPort: 1000, dstPort: 443, bytes: 500 }),
      rec({ srcIp: "10.0.0.1", dstIp: "10.0.0.2", srcPort: 1000, dstPort: 443, bytes: 500 }),
    ];
    const result = predictSlaveDistribution("layer3+4", slaves, records);
    expect(result.classified).toBe(2);
    expect(result.unclassified).toBe(0);
    const totalFlows = result.slaves.reduce((sum, s) => sum + s.flows, 0);
    const totalBytes = result.slaves.reduce((sum, s) => sum + s.bytes, 0);
    expect(totalFlows).toBe(2);
    expect(totalBytes).toBe(1000);
    // Identical tuples must land on the identical slave.
    const nonZero = result.slaves.filter((s) => s.flows > 0);
    expect(nonZero).toHaveLength(1);
    expect(nonZero[0]?.flows).toBe(2);
  });

  it("distributes distinct-port flows across more than one slave", () => {
    const records: FlowRecord[] = [];
    for (let port = 0; port < 200; port += 7) {
      records.push(rec({ srcIp: "10.0.0.1", dstIp: "10.0.0.2", srcPort: port, dstPort: 443, bytes: 10 }));
    }
    const result = predictSlaveDistribution("layer3+4", slaves, records);
    const usedSlaves = result.slaves.filter((s) => s.flows > 0).length;
    expect(usedSlaves).toBeGreaterThan(1);
  });

  it("excludes down slaves entirely", () => {
    const mixedSlaves: HashSlave[] = [
      { ref: "physnic:pve1:eno1", name: "eno1", up: true },
      { ref: "physnic:pve1:eno2", name: "eno2", up: false },
    ];
    const result = predictSlaveDistribution("layer3+4", mixedSlaves, [rec()]);
    expect(result.slaves).toHaveLength(1);
    expect(result.slaves[0]?.ref).toBe("physnic:pve1:eno1");
  });

  it("reports zero eligible slaves as an honest empty state, not a crash", () => {
    const result = predictSlaveDistribution("layer3+4", [], [rec(), rec()]);
    expect(result.slaves).toHaveLength(0);
    expect(result.classified).toBe(0);
    expect(result.unclassified).toBe(2);
    expect(result.unclassifiedReason).toMatch(/no eligible/);
  });

  it("MAC-dependent policies unclassify every flow with an explicit reason", () => {
    for (const policy of ["layer2", "layer2+3", "encap2+3"] as const) {
      const result = predictSlaveDistribution(policy, slaves, [rec(), rec()]);
      expect(result.classified).toBe(0);
      expect(result.unclassified).toBe(2);
      expect(result.unclassifiedReason).toMatch(/MAC/);
    }
  });

  it("unclassifies a non-IPv4 (IPv6) flow with an explicit reason", () => {
    const result = predictSlaveDistribution("layer3+4", slaves, [rec({ srcIp: "fe80::1", dstIp: "fe80::2" })]);
    expect(result.classified).toBe(0);
    expect(result.unclassified).toBe(1);
    expect(result.unclassifiedReason).toMatch(/IPv4/);
  });

  it("ignores ports for a non-TCP/UDP protocol", () => {
    const a = predictSlaveDistribution("layer3+4", slaves, [
      rec({ srcIp: "10.0.0.1", dstIp: "10.0.0.1", proto: 1, srcPort: 111, dstPort: 222 }),
    ]);
    const b = predictSlaveDistribution("layer3+4", slaves, [
      rec({ srcIp: "10.0.0.1", dstIp: "10.0.0.1", proto: 1, srcPort: 333, dstPort: 444 }),
    ]);
    // Identical src==dst IP (cancels to 0) and icmp (proto 1, ports
    // ignored) must land on the same slave regardless of port values.
    const aSlave = a.slaves.findIndex((s) => s.flows > 0);
    const bSlave = b.slaves.findIndex((s) => s.flows > 0);
    expect(aSlave).toBe(bSlave);
  });
});
