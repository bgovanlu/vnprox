// SPDX-License-Identifier: Apache-2.0

// Inline, as-you-type validation for the guided SDN wizards (issue #3:
// "Guided should always verify input inline to warn and guide users at time
// of input"). Every check here mirrors a real PVE constraint that would
// otherwise only surface as a mid-apply "Parameter verification failed"
// 400 — and each mirrors a server-side counterpart so the wizard and the
// change engine agree:
//
//   - name charset  ⇄ internal/change.schemaSDNName / codeSDNNameInvalid
//   - VNI required  ⇄ internal/change.vniRequiredFindings / codeSDNVNIRequired
//   - gateway-in-CIDR ⇄ internal/change.schemaGatewayInCIDR / codeGatewayNotInSubnet
//   - VNI/VID range ⇄ internal/change.checkVIDRange (schema class)
//
// Framework-free (no React import) so it is directly Vitest-able, matching
// web/src/ipam/nextFree.ts's own convention.

/** PVE's SDN zone/vnet id charset: a leading letter, then letters/digits
 * only (real PVE validates case-insensitively against `[a-z][a-z0-9]*`). */
const SDN_ID_RE = /^[A-Za-z][A-Za-z0-9]*$/;

/** PVE's usual SDN id length cap. Enforced only as a non-blocking warning
 * (sdnNameWarning) — the exact cap is version-dependent and unverified
 * against live hardware, and blocking it hard risks rejecting an id real
 * PVE would accept, so length only *guides*. */
export const SDN_ID_MAX = 8;

/** Highest VNI/VID the change engine currently accepts for a vnet tag
 * (internal/change.maxVID). Full VXLAN VNI range (up to 16777215) is a
 * documented follow-up — keeping the wizard aligned with the validator
 * avoids drafting an op the review screen would then block. */
export const VNI_MAX = 4094;

/** Hard error for an SDN object name (blocks Next). Empty is left to the
 * step's own required-field check, so this only flags a non-empty name with
 * characters PVE rejects. */
export function sdnNameError(name: string): string | undefined {
  const n = name.trim();
  if (n === "") return undefined;
  if (!SDN_ID_RE.test(n)) {
    return "Use only letters and digits, starting with a letter — Proxmox rejects spaces, hyphens, dots, and other characters.";
  }
  return undefined;
}

/** Non-blocking guidance for an over-long SDN name. */
export function sdnNameWarning(name: string): string | undefined {
  if (name.trim().length > SDN_ID_MAX) {
    return `Proxmox usually rejects SDN names longer than ${String(SDN_ID_MAX)} characters — consider shortening this.`;
  }
  return undefined;
}

/** Hard error for a required VNI (vxlan/evpn). 0/empty means "not set". */
export function vniError(vni: number): string | undefined {
  if (!vni) return "A VNI is required — Proxmox rejects a VXLAN/EVPN network without one.";
  if (!Number.isInteger(vni) || vni < 1 || vni > VNI_MAX) {
    return `The VNI must be a whole number between 1 and ${String(VNI_MAX)}.`;
  }
  return undefined;
}

function parseIPv4(s: string): number | undefined {
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(s.trim());
  if (!m) return undefined;
  const octets = [m[1], m[2], m[3], m[4]].map((o) => Number(o));
  if (octets.some((o) => o > 255)) return undefined;
  const [a = 0, b = 0, c = 0, d = 0] = octets;
  return ((a << 24) | (b << 16) | (c << 8) | d) >>> 0;
}

/** True for a syntactically valid IPv4 or (loosely) IPv6 literal. */
export function isValidIp(s: string): boolean {
  const t = s.trim();
  if (parseIPv4(t) !== undefined) return true;
  // Loose IPv6: hex groups and colons only, at least one colon.
  return t.includes(":") && /^[0-9a-fA-F:]+$/.test(t);
}

/** Hard error for a peer/underlay address field (vxlan/evpn). */
export function ipError(value: string): string | undefined {
  const t = value.trim();
  if (t === "") return "An address is required.";
  if (!isValidIp(t)) return `"${t}" is not a valid IP address.`;
  return undefined;
}

function parseIPv4CIDR(s: string): { base: number; prefix: number } | undefined {
  const m = /^(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3})\/(\d{1,2})$/.exec(s.trim());
  if (!m) return undefined;
  const base = parseIPv4(m[1] ?? "");
  const prefix = Number(m[2]);
  if (base === undefined || prefix > 32) return undefined;
  return { base, prefix };
}

/** Hard error for a CIDR field. Empty is allowed (the subnet step is
 * optional); a present-but-malformed CIDR is flagged. IPv6 CIDRs are passed
 * through (not flagged) since this only understands IPv4 syntax. */
export function cidrError(value: string): string | undefined {
  const t = value.trim();
  if (t === "") return undefined;
  if (t.includes(":")) return undefined; // IPv6 — out of scope for inline parse
  if (!parseIPv4CIDR(t)) return `"${t}" is not a valid CIDR, e.g. 10.10.0.0/24.`;
  return undefined;
}

/** True if ip falls inside cidr (both plain IPv4). False if either doesn't
 * parse — callers gate on cidrError/ipError first. Mirrors
 * internal/change.schemaGatewayInCIDR. */
export function ipv4InCidr(ip: string, cidr: string): boolean {
  const parsed = parseIPv4CIDR(cidr);
  const addr = parseIPv4(ip);
  if (!parsed || addr === undefined) return false;
  const hostBits = 32 - parsed.prefix;
  const mask = hostBits >= 32 ? 0 : (0xffffffff << hostBits) >>> 0;
  return (parsed.base & mask) === (addr & mask);
}

/** Hard error for a gateway field, given the subnet's CIDR: bad syntax, or a
 * syntactically-valid IPv4 that falls outside the (IPv4) CIDR. */
export function gatewayError(gateway: string, cidr: string): string | undefined {
  const g = gateway.trim();
  if (g === "") return undefined; // "keep isolated" / optional is handled by the step
  if (!isValidIp(g)) return `"${g}" is not a valid IP address.`;
  // Only cross-check containment when both sides are plain IPv4.
  if (parseIPv4(g) !== undefined && parseIPv4CIDR(cidr.trim()) && !ipv4InCidr(g, cidr)) {
    return `The gateway ${g} is not inside ${cidr.trim()} — Proxmox rejects a gateway outside the subnet.`;
  }
  return undefined;
}

/** Whether the shared subnet ("Addresses") step is free of hard errors. The
 * step is optional (an empty CIDR is fine), so this only fails on a
 * present-but-malformed CIDR or an out-of-range/invalid gateway. */
export function subnetStepValid(v: { cidr: string; gateway: string; isolated: boolean }): boolean {
  if (cidrError(v.cidr)) return false;
  if (!v.isolated && gatewayError(v.gateway, v.cidr)) return false;
  return true;
}
