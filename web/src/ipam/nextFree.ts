// Pure "next free address" picker logic (docs/features/ipam.md §3:
// "'Next free address' picker exposed everywhere an IP is entered elsewhere
// in the UI"). Framework-free (no React import) so it's directly
// Vitest-able and shareable with any consumer that already has a resolved
// cell list, independent of how that consumer chose to fetch it.
import type { IpamCell } from "../api/types";

/** Returns the lowest-addressed free cell's IP, or undefined if none is
 * free (or `cells` is empty/undefined). `cells` is expected in ascending
 * address order — every backend response (internal/ipam's hostAddresses)
 * already is — so the first "free" match is the lowest free address,
 * skipping allocated/observed/reserved/gateway/conflict cells exactly as
 * T-405 acceptance criterion 4 requires. */
export function nextFreeAddress(cells: IpamCell[] | undefined): string | undefined {
  if (!cells) {
    return undefined;
  }
  return cells.find((c) => c.state === "free")?.ip;
}

/** Parses a plain IPv4 CIDR (e.g. "10.50.0.0/24") into its 32-bit network
 * base and prefix length, or undefined if s isn't one — IPv6/malformed
 * input is deliberately out of scope (the SDN subnet wizard step's gateway
 * pre-fill only needs IPv4). */
function parseIPv4CIDR(s: string): { base: number; prefix: number } | undefined {
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})\/(\d{1,2})$/.exec(s.trim());
  if (!m) return undefined;
  const [, o1, o2, o3, o4, prefixStr] = m;
  if (o1 === undefined || o2 === undefined || o3 === undefined || o4 === undefined || prefixStr === undefined) {
    return undefined;
  }
  const octets = [o1, o2, o3, o4].map(Number);
  if (octets.some((o) => o < 0 || o > 255)) return undefined;
  const prefix = Number(prefixStr);
  if (prefix < 0 || prefix > 32) return undefined;
  const [b0, b1, b2, b3] = octets;
  const base = (((b0 ?? 0) << 24) | ((b1 ?? 0) << 16) | ((b2 ?? 0) << 8) | (b3 ?? 0)) >>> 0;
  return { base, prefix };
}

function ipv4ToString(n: number): string {
  return [(n >>> 24) & 0xff, (n >>> 16) & 0xff, (n >>> 8) & 0xff, n & 0xff].join(".");
}

/** T-701 acceptance criterion 1's live gateway pre-fill: cidr's first
 * usable IPv4 address — network address + 1 for a /30 or wider (mirrors
 * internal/ipam/addr.go's hostAddresses "exclude network/broadcast"
 * convention server-side, and internal/change/validate_fix.go's
 * firstUsableIP, the fix patch T-701's validators compute for the exact
 * same shapes — the wizard's live pre-fill and the validator's fix must
 * always agree), or the network address itself for a /31 or /32 (no
 * meaningful network/broadcast pair to exclude). Returns undefined for
 * anything that doesn't parse as a plain IPv4 CIDR, leaving the field for
 * the user to keep typing into. Purely a *guess* absent live allocation
 * data — SubnetStep only falls back to this when the CIDR doesn't overlap
 * a subnet vnprox already has an IPAM grid for (see that component's own
 * doc comment): when it does, `nextFreeAddress` above (via
 * `useIpamAllocationsQuery`) is used instead, so a re-used/overlapping
 * CIDR never suggests an address that's actually already taken. */
export function firstUsableIPv4(cidr: string): string | undefined {
  const parsed = parseIPv4CIDR(cidr);
  if (!parsed) return undefined;
  const { base, prefix } = parsed;
  const hostBits = 32 - prefix;
  const mask = hostBits >= 32 ? 0 : (0xffffffff << hostBits) >>> 0;
  const network = base & mask;
  const offset = hostBits >= 2 ? 1 : 0;
  return ipv4ToString((network + offset) >>> 0);
}
