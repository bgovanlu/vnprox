// "IP entry with subnet context" (T-504 deliverable): as the user types a
// literal IP endpoint, name which known SDN subnet (if any) contains it —
// purely client-side, over topology nodes already fetched for the map
// (no extra network round trip). internal/topology's SdnSubnet node label
// *is* its CIDR (docs/data-model.md's `SdnSubnet.ID` doc comment, echoed
// in docs/api.md's `GET /sdn` section: "`id` is the CIDR"), so this only
// needs IPv4 CIDR containment math over `TopologyNode`s already in hand.
// IPv6 is out of scope (every fixture in this repo addresses IPv4 only;
// an unparseable address/CIDR degrades to "no known subnet", never a
// crash or a guess).
import type { TopologyNode } from "../api/types";

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

function containsIp(cidr: string, ip: number): boolean {
  const parsed = parseCIDR(cidr);
  if (!parsed) return false;
  if (parsed.prefix === 0) return true;
  const mask = parsed.prefix === 32 ? 0xffffffff : (0xffffffff << (32 - parsed.prefix)) >>> 0;
  return (parsed.base & mask) === (ip & mask);
}

/** Finds every known SDN subnet (topology nodes of kind "sdn-subnet",
 * whose label is the subnet's CIDR) that contains `ip`. Returns CIDRs,
 * sorted most-specific (longest prefix) first, since a /32 host route
 * naming the address is more informative than a containing /16. */
export function subnetsContaining(ip: string, nodes: readonly TopologyNode[]): string[] {
  const parsedIp = parseIPv4(ip);
  if (parsedIp === undefined) return [];
  return nodes
    .filter((n) => n.kind === "sdn-subnet" && containsIp(n.label, parsedIp))
    .map((n) => n.label)
    .sort((a, b) => (parseCIDR(b)?.prefix ?? 0) - (parseCIDR(a)?.prefix ?? 0));
}

/** The one-line hint string shown under the IP endpoint input. Honest
 * about the negative case — "no known subnet" is a real, useful answer
 * (e.g. the address is on an upstream/external network vnprox never
 * observed), not an error. */
export function describeIpSubnetContext(ip: string, nodes: readonly TopologyNode[]): string {
  if (parseIPv4(ip) === undefined) {
    return "Not a recognized IPv4 address.";
  }
  const matches = subnetsContaining(ip, nodes);
  if (matches.length === 0) {
    return "No known SDN subnet contains this address.";
  }
  return `Within ${matches.join(", ")}.`;
}
