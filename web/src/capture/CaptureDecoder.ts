// SPDX-License-Identifier: Apache-2.0

// In-browser decode of a fetched pcap session file (T-1302): classic-pcap
// framing (internal/capturemock/pcap.go writes exactly this format — magic
// 0xa1b2c3d4 LE, 24-byte global header, then a 16-byte record header +
// frame per packet) down through Ethernet/VLAN/ARP/IPv4/ICMP/TCP/UDP/DNS/
// DHCP (internal/capturemock/frames.go's exact byte layout — this decoder
// is built and tested against that package's testdata/captures/ corpus).
//
// Every read is bounds-checked before it happens — a corrupt/truncated
// sample decodes defensively (whatever was successfully parsed before the
// data ran out, `truncated: true` on the packet or record loop that hit the
// wall), never a thrown exception. This module never trusts anything about
// the byte layout beyond what it explicitly checks.

const PCAP_MAGIC_LE = 0xa1b2c3d4;
const GLOBAL_HEADER_LEN = 24;
const RECORD_HEADER_LEN = 16;

export interface DecodedLayer {
  name: string;
  fields: Record<string, string>;
}

export interface DecodedPacket {
  index: number;
  tsSec: number;
  tsUsec: number;
  capturedLength: number;
  originalLength: number;
  /** One-line summary for the packet list row (Wireshark-lite). */
  summary: string;
  layers: DecodedLayer[];
  /** True when this packet's frame bytes ran out mid-decode — whatever
   * layers were successfully parsed are still included. */
  truncated: boolean;
}

export interface DecodeResult {
  /** False when the global header itself isn't a recognized classic-pcap
   * header (bad magic, or fewer than 24 bytes) — packets is always [] in
   * that case. */
  headerValid: boolean;
  packets: DecodedPacket[];
  /** True when the record loop stopped early because a record's declared
   * length ran past the end of the buffer (a mid-write truncation) —
   * distinct from headerValid: the header was fine, decoding just couldn't
   * reach every byte the file nominally contains. */
  truncatedTail: boolean;
}

/** A read-only cursor over a DataView that never throws: every read
 * bounds-checks first and returns undefined (and flags `.truncated`) if
 * there isn't enough data left, rather than letting DataView throw a
 * RangeError. */
class Cursor {
  readonly view: DataView;
  offset: number;
  readonly end: number;
  truncated = false;

  constructor(view: DataView, offset: number, end: number) {
    this.view = view;
    this.offset = offset;
    this.end = end;
  }

  remaining(): number {
    return Math.max(0, this.end - this.offset);
  }

  private need(n: number): boolean {
    if (this.remaining() < n) {
      this.truncated = true;
      return false;
    }
    return true;
  }

  u8(): number | undefined {
    if (!this.need(1)) return undefined;
    const v = this.view.getUint8(this.offset);
    this.offset += 1;
    return v;
  }

  u16be(): number | undefined {
    if (!this.need(2)) return undefined;
    const v = this.view.getUint16(this.offset, false);
    this.offset += 2;
    return v;
  }

  u32be(): number | undefined {
    if (!this.need(4)) return undefined;
    const v = this.view.getUint32(this.offset, false);
    this.offset += 4;
    return v;
  }

  /** Reads n raw bytes as a Uint8Array, or undefined if not enough remain. */
  bytes(n: number): Uint8Array | undefined {
    if (!this.need(n)) return undefined;
    const out = new Uint8Array(this.view.buffer, this.view.byteOffset + this.offset, n);
    this.offset += n;
    return out;
  }

  /** Advances past n bytes without reading them (for fixed-size fields this
   * decoder doesn't surface), returning false (and flagging truncated) if
   * fewer than n bytes remain — the caller should stop decoding further
   * fields in that case. */
  skip(n: number): boolean {
    if (!this.need(n)) return false;
    this.offset += n;
    return true;
  }
}

function macString(bytes: Uint8Array): string {
  return Array.from(bytes)
    .map((b) => b.toString(16).padStart(2, "0"))
    .join(":");
}

function ipv4String(bytes: Uint8Array): string {
  return Array.from(bytes).join(".");
}

const TCP_FLAG_BITS: [number, string][] = [
  [0x20, "URG"],
  [0x10, "ACK"],
  [0x08, "PSH"],
  [0x04, "RST"],
  [0x02, "SYN"],
  [0x01, "FIN"],
];

function tcpFlagsString(flags: number): string {
  const names = TCP_FLAG_BITS.filter(([bit]) => (flags & bit) !== 0).map(([, name]) => name);
  return names.length > 0 ? names.join(",") : "-";
}

const DHCP_MESSAGE_TYPES: Record<number, string> = {
  1: "DISCOVER", 2: "OFFER", 3: "REQUEST", 4: "DECLINE",
  5: "ACK", 6: "NAK", 7: "RELEASE", 8: "INFORM",
};

/** Decodes a DNS question section's QNAME (a sequence of length-prefixed
 * labels terminated by a zero-length label) starting at c.offset, without
 * following compression pointers (0xC0 bit set) — the T-1301 corpus never
 * emits one, and a decoder need only handle what it can positively parse;
 * an unsupported/truncated name yields undefined rather than a guess. */
function decodeDnsName(c: Cursor): string | undefined {
  const labels: string[] = [];
  for (let i = 0; i < 64; i++) {
    const len = c.u8();
    if (len === undefined) return undefined;
    if (len === 0) return labels.join(".");
    if ((len & 0xc0) !== 0) return undefined; // compression pointer, not supported
    const label = c.bytes(len);
    if (label === undefined) return undefined;
    labels.push(new TextDecoder().decode(label));
  }
  return undefined; // pathologically long name — bail rather than loop forever
}

function decodeDns(c: Cursor, layers: DecodedLayer[]): string | undefined {
  const id = c.u16be();
  const flags = c.u16be();
  const qdcount = c.u16be();
  if (id === undefined || flags === undefined || qdcount === undefined) return undefined;
  if (!c.skip(6)) return undefined; // ancount/nscount/arcount
  const isResponse = (flags & 0x8000) !== 0;
  let name: string | undefined;
  let qtype: number | undefined;
  if (qdcount > 0) {
    name = decodeDnsName(c);
    qtype = c.u16be();
    c.skip(2); // qclass
  }
  layers.push({
    name: "DNS",
    fields: {
      id: `0x${id.toString(16)}`,
      type: isResponse ? "response" : "query",
      name: name ?? "(unparsed)",
      qtype: qtype !== undefined ? String(qtype) : "-",
    },
  });
  return `DNS ${isResponse ? "response" : "query"} ${name ?? ""}`.trim();
}

function decodeDhcp(c: Cursor, layers: DecodedLayer[]): string | undefined {
  const op = c.u8();
  const htype = c.u8();
  const hlen = c.u8();
  if (!c.skip(1)) return undefined; // hops
  const xid = c.u32be();
  if (op === undefined || htype === undefined || hlen === undefined || xid === undefined) return undefined;
  // secs(2) flags(2) ciaddr(4) yiaddr(4) siaddr(4) giaddr(4) chaddr(16) sname(64) file(128) = 228 bytes
  // (BOOTP's fixed header is 236 bytes total; op/htype/hlen/hops/xid above
  // already consumed the first 8).
  if (!c.skip(228)) return undefined;
  const cookie = c.bytes(4);
  const validCookie = cookie?.[0] === 0x63 && cookie[1] === 0x82 && cookie[2] === 0x53 && cookie[3] === 0x63;
  if (!validCookie) {
    layers.push({ name: "DHCP", fields: { op: op === 1 ? "BOOTREQUEST" : "BOOTREPLY", xid: `0x${xid.toString(16)}` } });
    return "DHCP (no magic cookie)";
  }
  let messageType: number | undefined;
  for (let i = 0; i < 64; i++) {
    const optType = c.u8();
    if (optType === undefined) break;
    if (optType === 0xff) break; // End
    if (optType === 0x00) continue; // Pad
    const optLen = c.u8();
    if (optLen === undefined) break;
    const optVal = c.bytes(optLen);
    if (optVal === undefined) break;
    if (optType === 53 && optVal.length >= 1) messageType = optVal[0];
  }
  const typeName = messageType !== undefined ? (DHCP_MESSAGE_TYPES[messageType] ?? `type ${String(messageType)}`) : "unknown";
  layers.push({
    name: "DHCP",
    fields: { op: op === 1 ? "BOOTREQUEST" : "BOOTREPLY", messageType: typeName, xid: `0x${xid.toString(16)}` },
  });
  return `DHCP ${typeName}`;
}

function decodeUdp(c: Cursor, layers: DecodedLayer[]): string {
  const srcPort = c.u16be();
  const dstPort = c.u16be();
  const length = c.u16be();
  if (!c.skip(2)) { // checksum
    layers.push({ name: "UDP", fields: { srcPort: str(srcPort), dstPort: str(dstPort) } });
    return "UDP (truncated)";
  }
  layers.push({ name: "UDP", fields: { srcPort: str(srcPort), dstPort: str(dstPort), length: str(length) } });

  const isDns = srcPort === 53 || dstPort === 53;
  const isDhcp = srcPort === 67 || dstPort === 67 || srcPort === 68 || dstPort === 68;
  if (isDns) {
    const summary = decodeDns(c, layers);
    if (summary) return summary;
  } else if (isDhcp) {
    const summary = decodeDhcp(c, layers);
    if (summary) return summary;
  }
  return `UDP ${str(srcPort)} → ${str(dstPort)}`;
}

function decodeTcp(c: Cursor, layers: DecodedLayer[]): string {
  const srcPort = c.u16be();
  const dstPort = c.u16be();
  const seq = c.u32be();
  if (!c.skip(4)) { // ack number
    layers.push({ name: "TCP", fields: { srcPort: str(srcPort), dstPort: str(dstPort) } });
    return "TCP (truncated)";
  }
  const offsetByte = c.u8();
  const flagsByte = c.u8();
  const window = c.u16be();
  const flags = flagsByte !== undefined ? tcpFlagsString(flagsByte) : "-";
  layers.push({
    name: "TCP",
    fields: {
      srcPort: str(srcPort), dstPort: str(dstPort), seq: str(seq),
      flags, window: str(window), dataOffset: offsetByte !== undefined ? String((offsetByte >> 4) * 4) : "-",
    },
  });
  return `TCP ${str(srcPort)} → ${str(dstPort)} [${flags}]`;
}

const ICMP_TYPE_NAMES: Record<number, string> = { 0: "echo reply", 8: "echo request", 3: "unreachable", 11: "time exceeded" };

function decodeIcmp(c: Cursor, layers: DecodedLayer[]): string {
  const type = c.u8();
  const code = c.u8();
  if (!c.skip(2)) { // checksum
    layers.push({ name: "ICMP", fields: { type: str(type) } });
    return "ICMP (truncated)";
  }
  const id = c.u16be();
  const seq = c.u16be();
  const typeName = type !== undefined ? (ICMP_TYPE_NAMES[type] ?? `type ${String(type)}`) : "unknown";
  layers.push({ name: "ICMP", fields: { type: typeName, code: str(code), id: str(id), seq: str(seq) } });
  return `ICMP ${typeName}`;
}

const IP_PROTO_NAMES: Record<number, string> = { 1: "ICMP", 6: "TCP", 17: "UDP" };

function decodeIpv4(c: Cursor, layers: DecodedLayer[]): string {
  const versionIhl = c.u8();
  if (!c.skip(1)) { // DSCP/ECN
    layers.push({ name: "IPv4", fields: {} });
    return "IPv4 (truncated)";
  }
  const totalLength = c.u16be();
  if (!c.skip(5)) { // id(2) flags/fragoffset(2) ttl(1)
    layers.push({ name: "IPv4", fields: { totalLength: str(totalLength) } });
    return "IPv4 (truncated)";
  }
  const protocol = c.u8();
  if (!c.skip(2)) { // checksum
    layers.push({ name: "IPv4", fields: { protocol: str(protocol) } });
    return "IPv4 (truncated)";
  }
  const src = c.bytes(4);
  const dst = c.bytes(4);
  if (src === undefined || dst === undefined) {
    layers.push({ name: "IPv4", fields: { protocol: str(protocol) } });
    return "IPv4 (truncated)";
  }
  const ihl = versionIhl !== undefined ? (versionIhl & 0x0f) * 4 : 20;
  if (ihl > 20) c.skip(ihl - 20); // IPv4 options this corpus never emits

  const protoName = protocol !== undefined ? (IP_PROTO_NAMES[protocol] ?? `proto ${String(protocol)}`) : "unknown";
  layers.push({
    name: "IPv4",
    fields: {
      src: ipv4String(src), dst: ipv4String(dst), protocol: protoName, totalLength: str(totalLength),
    },
  });
  const srcStr = ipv4String(src);
  const dstStr = ipv4String(dst);

  switch (protocol) {
    case 1:
      return `${decodeIcmp(c, layers)} ${srcStr} → ${dstStr}`;
    case 6:
      return `${decodeTcp(c, layers)} (${srcStr}→${dstStr})`;
    case 17:
      return `${decodeUdp(c, layers)} (${srcStr}→${dstStr})`;
    default:
      return `IPv4 ${srcStr} → ${dstStr} (${protoName})`;
  }
}

function decodeArp(c: Cursor, layers: DecodedLayer[]): string {
  const htype = c.u16be();
  const ptype = c.u16be();
  const hlen = c.u8();
  const plen = c.u8();
  const oper = c.u16be();
  if (htype === undefined || ptype === undefined || hlen === undefined || plen === undefined || oper === undefined) {
    layers.push({ name: "ARP", fields: {} });
    return "ARP (truncated)";
  }
  const sha = c.bytes(hlen);
  const spa = c.bytes(plen);
  const tha = c.bytes(hlen);
  const tpa = c.bytes(plen);
  if (sha === undefined || spa === undefined || tha === undefined || tpa === undefined) {
    layers.push({ name: "ARP", fields: { operation: oper === 1 ? "request" : "reply" } });
    return "ARP (truncated)";
  }
  const spaStr = plen === 4 ? ipv4String(spa) : macString(spa);
  const tpaStr = plen === 4 ? ipv4String(tpa) : macString(tpa);
  const shaStr = macString(sha);
  layers.push({
    name: "ARP",
    fields: { operation: oper === 1 ? "request" : "reply", senderMac: shaStr, senderIp: spaStr, targetIp: tpaStr },
  });
  return oper === 1 ? `ARP who-has ${tpaStr}? tell ${spaStr}` : `ARP ${spaStr} is at ${shaStr}`;
}

function decodeEthernetPayload(etherType: number, c: Cursor, layers: DecodedLayer[]): string {
  switch (etherType) {
    case 0x0806:
      return decodeArp(c, layers);
    case 0x0800:
      return decodeIpv4(c, layers);
    default:
      return `unknown EtherType 0x${etherType.toString(16)}`;
  }
}

function str(v: number | undefined): string {
  return v !== undefined ? String(v) : "-";
}

/** Decodes one Ethernet frame (starting at byte 0 of `frame`) into its
 * layer stack + a one-line summary, defensively — running out of bytes
 * mid-decode stops adding fields/layers rather than throwing. */
function decodeFrame(frame: Uint8Array): { layers: DecodedLayer[]; summary: string; truncated: boolean } {
  const view = new DataView(frame.buffer, frame.byteOffset, frame.byteLength);
  const c = new Cursor(view, 0, frame.byteLength);
  const layers: DecodedLayer[] = [];

  const dst = c.bytes(6);
  const src = c.bytes(6);
  const etherType = c.u16be();
  if (dst === undefined || src === undefined || etherType === undefined) {
    return { layers, summary: "Ethernet (truncated)", truncated: true };
  }
  layers.push({ name: "Ethernet", fields: { dst: macString(dst), src: macString(src), etherType: `0x${etherType.toString(16)}` } });

  let effectiveType = etherType;
  if (etherType === 0x8100) {
    const tci = c.u16be();
    const innerType = c.u16be();
    if (tci === undefined || innerType === undefined) {
      return { layers, summary: "VLAN (truncated)", truncated: true };
    }
    const vid = tci & 0x0fff;
    const priority = (tci >> 13) & 0x07;
    layers.push({ name: "VLAN", fields: { vid: String(vid), priority: String(priority) } });
    effectiveType = innerType;
  }

  const summary = decodeEthernetPayload(effectiveType, c, layers);
  return { layers, summary, truncated: c.truncated };
}

/** Decodes a classic-pcap file's bytes into a packet list. Never throws:
 * a bad global header yields `{headerValid: false, packets: []}`, and a
 * mid-write truncated record stops the loop (`truncatedTail: true`) while
 * keeping every packet successfully parsed before it. */
export function decodePcap(buf: ArrayBuffer): DecodeResult {
  if (buf.byteLength < GLOBAL_HEADER_LEN) {
    return { headerValid: false, packets: [], truncatedTail: false };
  }
  const view = new DataView(buf);
  const magic = view.getUint32(0, true); // little-endian, matching pcap.go's pcapMagic
  if (magic !== PCAP_MAGIC_LE) {
    return { headerValid: false, packets: [], truncatedTail: false };
  }

  const packets: DecodedPacket[] = [];
  let offset = GLOBAL_HEADER_LEN;
  let truncatedTail = false;

  while (offset < buf.byteLength) {
    if (offset + RECORD_HEADER_LEN > buf.byteLength) {
      truncatedTail = true;
      break;
    }
    const tsSec = view.getUint32(offset, true);
    const tsUsec = view.getUint32(offset + 4, true);
    const inclLen = view.getUint32(offset + 8, true);
    const origLen = view.getUint32(offset + 12, true);
    const frameStart = offset + RECORD_HEADER_LEN;
    if (frameStart + inclLen > buf.byteLength) {
      truncatedTail = true;
      break;
    }
    const frame = new Uint8Array(buf, frameStart, inclLen);
    const { layers, summary, truncated } = decodeFrame(frame);
    packets.push({
      index: packets.length,
      tsSec, tsUsec,
      capturedLength: inclLen, originalLength: origLen,
      summary, layers, truncated,
    });
    offset = frameStart + inclLen;
  }

  return { headerValid: true, packets, truncatedTail };
}
