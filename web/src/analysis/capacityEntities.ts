// Which entities `GET /capacity/export` actually has history for, derived
// from the same two sources cmd/vnproxd's capacityBucketSource rolls up
// from. Pure, so the derivation is testable without rendering anything.
//
// The two `kind` values do NOT share a ref scheme, and the difference is
// load-bearing:
//
//   kind=link       ref is a `physnic:<node>:<iface>` inventory Ref, and
//                   only for NICs with a negotiated speed — a link with no
//                   SpeedMbps has no utilization percentage to roll up, so
//                   it has no history either. Bonds are deliberately not
//                   rolled up (bond-aggregate speed is a documented future
//                   refinement), so they are not offered here.
//   kind=ipam_pool  ref is the subnet's CIDR, plain — not a Ref triplet.
//
// Offering an entity the rollup never wrote would produce an empty export
// that reads as "no history" when the truth is "never collected", so this
// module errs toward offering fewer, correct candidates.
import type { IpamSubnet, TopologyNode } from "../api/types";

export interface CapacityEntity {
  /** The `ref` query parameter, verbatim. */
  ref: string;
  /** What to show in the picker. */
  label: string;
}

const PHYSNIC_PREFIX = "physnic:";

/** Every physical NIC the map knows about, including the ones absorbed into
 * a collapsed phys-group pill (T-1907 pills carry their members' Ref
 * strings precisely so a consumer does not have to expand them first). */
export function linkEntities(nodes: readonly TopologyNode[]): CapacityEntity[] {
  const refs = new Set<string>();
  for (const n of nodes) {
    if (n.id.startsWith(PHYSNIC_PREFIX)) {
      refs.add(n.id);
    }
    for (const member of n.members ?? []) {
      if (member.startsWith(PHYSNIC_PREFIX)) {
        refs.add(member);
      }
    }
  }
  return [...refs]
    .sort((a, b) => a.localeCompare(b))
    .map((ref) => ({ ref, label: linkLabel(ref) }));
}

/** `physnic:<node>:<iface>` -> "node / iface". Falls back to the raw Ref
 * for anything that does not split into three parts, rather than showing a
 * confidently wrong label. */
function linkLabel(ref: string): string {
  const parts = ref.split(":");
  const node = parts[1];
  const iface = parts[2];
  if (node === undefined || iface === undefined || node === "" || iface === "") {
    return ref;
  }
  return `${node} / ${iface}`;
}

/** Every IPAM pool with a nonzero size — `capacityBucketSource.poolAggregates`
 * skips a subnet whose `total` is 0, so one has no history to export. */
export function poolEntities(subnets: readonly IpamSubnet[]): CapacityEntity[] {
  return subnets
    .filter((s) => s.total > 0)
    .map((s) => ({ ref: s.cidr, label: s.vnet ? `${s.cidr} (${s.vnet})` : s.cidr }))
    .sort((a, b) => a.ref.localeCompare(b.ref));
}
