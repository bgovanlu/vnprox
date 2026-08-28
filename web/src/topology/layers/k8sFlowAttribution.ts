// SPDX-License-Identifier: Apache-2.0

// T-1502 AC2: attributes a flow-explorer row's srcIp/dstIp to a k8s
// service/pod/pod-subnet, mirroring internal/k8s/resolver.go's
// K8sResolver.Resolve match precedence exactly (its own doc comment):
//
//   1. Exact Service ClusterIP match -> that service's ref.
//   2. Exact Pod IP match -> that pod's ref.
//   3. Pod-CIDR containment -> the owning node's pod-subnet ref (names the
//      subnet, never invents a specific pod/service identity).
//
// An address matching none of the above resolves to undefined — never
// guessed, the same contract K8sResolver.Resolve documents. This is a
// client-side port of that same algorithm (not a call into the backend):
// docs/api.md's Flows section documents no `k8sService` wire field on
// flow.Record (T-1504's serviceClass is a genuinely backend-computed
// classification; k8s attribution here is purely a display-time join
// between two GET routes the frontend already has open — GET /flows and
// GET /k8s/{clusterId}/overlay — following the same "kept in sync by hand"
// convention wgEdgeStatus.ts/mtuOverlay.ts already establish for
// mirroring backend thresholds/algorithms client-side). Attribution is
// display-only, never a filter that hides an unresolved row (this task's
// card, verbatim).
import type { K8sOverlay } from "../../api/types";
import { k8sPodId, k8sPodSubnetId, k8sServiceId, parseK8sSyntheticId } from "./k8sOverlay";

function parseIPv4(ip: string): number | undefined {
  const parts = ip.trim().split(".");
  if (parts.length !== 4) return undefined;
  let out = 0;
  for (const part of parts) {
    if (!/^\d{1,3}$/.test(part)) return undefined;
    const n = Number(part);
    if (n < 0 || n > 255) return undefined;
    out = (out << 8) | n;
  }
  return out >>> 0;
}

function parseCIDR(cidr: string): { base: number; prefix: number } | undefined {
  const [addr, prefixStr] = cidr.split("/", 2);
  if (!addr || prefixStr === undefined) return undefined;
  const prefix = Number(prefixStr);
  if (!Number.isInteger(prefix) || prefix < 0 || prefix > 32) return undefined;
  const base = parseIPv4(addr);
  if (base === undefined) return undefined;
  return { base, prefix };
}

function containsIp(entry: { base: number; prefix: number }, ip: number): boolean {
  if (entry.prefix === 0) return true;
  const mask = entry.prefix === 32 ? 0xffffffff : (0xffffffff << (32 - entry.prefix)) >>> 0;
  return (entry.base & mask) === (ip & mask);
}

interface PodSubnetEntry {
  base: number;
  prefix: number;
  ref: string;
}

export interface K8sAttributionIndex {
  /** ip (string, as reported) -> resolved ref. Services indexed first,
   * then pods, so a pod build that happens to also match on IP overwrite
   * order never matters — see buildK8sAttributionIndex's own comment for
   * why services win when both would otherwise claim the same key (they
   * never legitimately would, but insertion order settles it the same way
   * K8sResolver.Refresh's own doc comment describes). */
  exact: Map<string, string>;
  podSubnets: PodSubnetEntry[];
}

/** Indexes every currently-known cluster's overlay for attribution lookups
 * — additive across clusters, exactly like K8sResolver.Refresh being
 * called once per registered cluster. */
export function buildK8sAttributionIndex(overlays: readonly K8sOverlay[]): K8sAttributionIndex {
  const exact = new Map<string, string>();
  const podSubnets: PodSubnetEntry[] = [];

  for (const overlay of overlays) {
    for (const s of overlay.services) {
      if (!s.clusterIp || s.clusterIp.toLowerCase() === "none") continue; // headless — no single address to index
      exact.set(s.clusterIp, k8sServiceId(overlay.clusterId, s.namespace, s.name));
    }
    for (const p of overlay.pods) {
      if (!p.podIp) continue;
      if (!exact.has(p.podIp)) exact.set(p.podIp, k8sPodId(overlay.clusterId, p.namespace, p.name));
    }
    for (const pc of overlay.podCidrs) {
      const parsed = parseCIDR(pc.cidr);
      if (!parsed) continue;
      podSubnets.push({ ...parsed, ref: k8sPodSubnetId(overlay.clusterId, pc.node) });
    }
  }

  return { exact, podSubnets };
}

/** Resolves one address against index, per this file's documented
 * precedence. undefined when nothing matches — never guessed. */
export function resolveK8sAddress(index: K8sAttributionIndex, ip: string): string | undefined {
  const trimmed = ip.trim();
  const exact = index.exact.get(trimmed);
  if (exact) return exact;

  const parsedIp = parseIPv4(trimmed);
  if (parsedIp === undefined) return undefined;
  for (const s of index.podSubnets) {
    if (containsIp(s, parsedIp)) return s.ref;
  }
  return undefined;
}

/** Attributes one flow record's k8sService column: checks dstIp first
 * (the common "traffic to a service/pod" case), falling back to srcIp (a
 * pod's own outbound traffic) — undefined when neither address resolves
 * against any registered cluster's overlay. */
export function attributeK8sFlow(
  index: K8sAttributionIndex,
  record: { srcIp: string; dstIp: string },
): string | undefined {
  return resolveK8sAddress(index, record.dstIp) ?? resolveK8sAddress(index, record.srcIp);
}

/** Renders an attribution ref (k8sPodId/k8sServiceId/k8sPodSubnetId's own
 * format) as the short, human-legible label the flow explorer's
 * `k8sService` column shows. Falls back to the raw ref for anything this
 * module didn't itself produce (defensive; should not happen given
 * resolveK8sAddress only ever returns its own refs). */
export function formatK8sAttribution(ref: string): string {
  const parsed = parseK8sSyntheticId(ref);
  if (!parsed) return ref;
  switch (parsed.kind) {
    case "service":
      return `${parsed.namespace}/${parsed.name} (svc)`;
    case "pod":
      return `${parsed.namespace}/${parsed.name} (pod)`;
    case "pod-cidr":
      return `${parsed.node} (pod net)`;
  }
}
