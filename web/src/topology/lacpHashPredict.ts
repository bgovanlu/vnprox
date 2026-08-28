// SPDX-License-Identifier: Apache-2.0

// T-4110 (LACP hash visualizer, hardware-flagged per CLAUDE.md's "needs
// real NICs / physical switch" category — see
// planning/reports/needs-hardware-validation.md's T-4110 entry). Pure,
// framework-free client-side prediction of which bond slave the Linux
// kernel's xmit_hash_policy would route a flow to — a TypeScript port of
// internal/lacphash's Go implementation, "kept in sync by hand" the same
// way this codebase's other client-side overlay algorithms already are
// (k8sFlowAttribution.ts's own doc comment, docs/features/monitoring.md
// §3), so this predicted-vs-actual view needs no new backend round trip.
//
// Deliberately NARROWER than internal/lacphash: this port only ever
// receives web/src/api/types.ts's FlowRecord, which — like every
// sFlow/NetFlow/IPFIX/conntrack record internal/flow.Record represents —
// never carries an Ethernet source/destination MAC address (see
// internal/lacphash/doc.go for the full explanation of why). So layer2,
// layer2+3, and encap2+3 (all MAC-dependent) are never computed here at
// all — predictSlaveDistribution reports every flow as unclassified for
// those three policies with an explicit reason, rather than silently
// hashing a zero/absent MAC. Only layer3+4/encap3+4 are ever actually
// predicted from real flow data, on both the Go and TypeScript sides —
// this file does not need to reimplement the MAC-XOR arithmetic the Go
// package carries for completeness/testability, since it would never be
// exercised by real input here.
//
// IPv4 only: this port's ipXOR only parses dotted-quad IPv4 addresses.
// internal/lacphash's Go package does handle IPv6 (see its own tests) —
// this simplification is specific to the lighter-weight client-side port,
// not a claim that IPv6 traffic can't hash under layer3+4 in general. An
// IPv6 flow here is reported unclassified with an explicit reason, the
// same honest-gap treatment as a MAC-dependent policy.
import type { FlowRecord } from "../api/types";

export type LacpHashPolicy = "layer2" | "layer2+3" | "layer3+4" | "encap2+3" | "encap3+4";

const MAC_DEPENDENT_POLICIES: ReadonlySet<LacpHashPolicy> = new Set(["layer2", "layer2+3", "encap2+3"]);

export function isLacpHashPolicy(v: string): v is LacpHashPolicy {
  return v === "layer2" || v === "layer2+3" || v === "layer3+4" || v === "encap2+3" || v === "encap3+4";
}

/** True for a policy this client-side port can actually compute a
 * prediction for, given FlowRecord's fields (no MAC, IPv4 dotted-quad
 * only — see this file's header comment). */
export function isPredictablePolicy(policy: LacpHashPolicy): boolean {
  return !MAC_DEPENDENT_POLICIES.has(policy);
}

/** One bond slave this predictor buckets flows into. `ref` is the slave's
 * inventory Ref (`physnic:<node>:<name>`), matching metrics.SlaveRate.ref
 * so predicted and actual rows can be joined by it. */
export interface HashSlave {
  ref: string;
  name: string;
  up: boolean;
}

export interface PredictedSlave {
  ref: string;
  name: string;
  flows: number;
  bytes: number;
}

export interface PredictionResult {
  policy: LacpHashPolicy;
  slaves: PredictedSlave[];
  classified: number;
  unclassified: number;
  /** Set whenever unclassified > 0 — every unclassified flow in one
   * prediction fails for the same structural reason (see this file's
   * header), so one reason string is informative enough. */
  unclassifiedReason?: string;
}

/** IPv4 dotted-quad -> 32-bit unsigned int, or undefined for anything else
 * (IPv6, malformed). `>>> 0` throughout keeps every intermediate value an
 * unsigned 32-bit int, matching Go's uint32 arithmetic bit-for-bit — a
 * signed `<<`/`^` in JS would otherwise flip the sign bit before the final
 * modulo. */
function parseIPv4(ip: string): number | undefined {
  const parts = ip.split(".");
  if (parts.length !== 4) return undefined;
  let n = 0;
  for (const p of parts) {
    if (!/^\d{1,3}$/.test(p)) return undefined;
    const v = Number(p);
    if (v > 255) return undefined;
    n = ((n << 8) | v) >>> 0;
  }
  return n >>> 0;
}

/** bonding.rst's two-step shift-fold, identical to internal/lacphash's
 * foldHash: hash ^= hash>>16; hash ^= hash>>8. */
function foldHash(hash: number): number {
  hash = (hash ^ (hash >>> 16)) >>> 0;
  hash = (hash ^ (hash >>> 8)) >>> 0;
  return hash;
}

function isTCPOrUDP(proto: number): boolean {
  return proto === 6 || proto === 17;
}

/** internal/lacphash.hashLayer34's formula, ported: hash = srcIP XOR
 * dstIP, folded, then XORed with srcPort XOR dstPort when the protocol is
 * TCP/UDP and at least one port is known. Returns undefined when either
 * address isn't a parseable IPv4 dotted-quad. */
function hashLayer34(r: FlowRecord): number | undefined {
  const s = parseIPv4(r.srcIp);
  const d = parseIPv4(r.dstIp);
  if (s === undefined || d === undefined) return undefined;
  let hash = (s ^ d) >>> 0;
  const srcPort = r.srcPort ?? 0;
  const dstPort = r.dstPort ?? 0;
  if (isTCPOrUDP(r.proto) && (srcPort !== 0 || dstPort !== 0)) {
    hash = (hash ^ (srcPort ^ dstPort)) >>> 0;
  }
  return foldHash(hash);
}

/** Predicts which of slaves' Up members each of records would hash to
 * under policy, weighting each bucket by the record's own byte count —
 * the client-side mirror of internal/lacphash.Predict. Never throws: a
 * record this policy/port combination can't classify (MAC-dependent
 * policy, non-IPv4 address) is counted into `unclassified` instead, and a
 * bond with zero Up slaves returns an empty `slaves` with every record
 * unclassified. Callers render each of those cases as an explicit, honest
 * empty state (see LacpHashSection.tsx) rather than a misleading zeroed
 * table. */
export function predictSlaveDistribution(
  policy: LacpHashPolicy,
  slaves: readonly HashSlave[],
  records: readonly FlowRecord[],
): PredictionResult {
  const up = slaves.filter((s) => s.up);
  const result: PredictionResult = {
    policy,
    slaves: up.map((s) => ({ ref: s.ref, name: s.name, flows: 0, bytes: 0 })),
    classified: 0,
    unclassified: 0,
  };

  const setReason = (reason: string) => {
    result.unclassified += 1;
    result.unclassifiedReason ??= reason;
  };

  if (up.length === 0) {
    for (const _r of records) setReason("bond has no eligible (up) slaves");
    return result;
  }

  if (!isPredictablePolicy(policy)) {
    for (const _r of records) {
      setReason(
        "requires source/destination MAC addresses, which vnprox's flow records (sFlow/NetFlow/IPFIX/conntrack) never carry",
      );
    }
    return result;
  }

  for (const r of records) {
    const hash = hashLayer34(r);
    if (hash === undefined) {
      setReason("source/destination address is not a parseable IPv4 address (IPv6 not supported client-side)");
      continue;
    }
    const idx = hash % up.length;
    result.classified += 1;
    const slave = result.slaves[idx];
    if (slave) {
      slave.flows += 1;
      slave.bytes += r.bytes;
    }
  }
  return result;
}
