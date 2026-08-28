// SPDX-License-Identifier: Apache-2.0

package capturemock

import "encoding/binary"

// This file assembles a small, canonical set of Ethernet frames covering the
// protocol corpus T-1301's card requires (Ethernet / VLAN / ARP / IP / ICMP
// / TCP / UDP / DNS / DHCP) — the exact set T-1302's in-browser decoder is
// built and tested against. Frames are constructed byte-for-byte with
// encoding/binary (no packet library, per CLAUDE.md), with correct
// EtherTypes, IPv4 header + checksum, and L4 protocol numbers/ports so a
// decoder parses them structurally; L4 checksums are left zero (decoders do
// not verify them).

var (
	macSrc   = [6]byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x01}
	macDst   = [6]byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x02}
	macBcast = [6]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	ipSrc    = [4]byte{10, 0, 0, 1}
	ipDst    = [4]byte{10, 0, 0, 2}
)

const (
	etherTypeIPv4 = 0x0800
	etherTypeARP  = 0x0806
	etherTypeVLAN = 0x8100
	protoICMP     = 1
	protoTCP      = 6
	protoUDP      = 17
)

// FrameKind names each protocol in the sample corpus, in a stable order.
type FrameKind string

const (
	FrameARP  FrameKind = "arp"
	FrameICMP FrameKind = "icmp"
	FrameTCP  FrameKind = "tcp"
	FrameUDP  FrameKind = "udp"
	FrameDNS  FrameKind = "dns"
	FrameDHCP FrameKind = "dhcp"
	FrameVLAN FrameKind = "vlan"
)

// CorpusOrder is the deterministic order the scripted agent cycles frames in
// and the corpus generator emits them — one frame per protocol the decoder
// must handle.
var CorpusOrder = []FrameKind{FrameARP, FrameICMP, FrameTCP, FrameUDP, FrameDNS, FrameDHCP, FrameVLAN}

// buildFrame returns one Ethernet frame for the given kind.
func buildFrame(kind FrameKind) []byte {
	switch kind {
	case FrameARP:
		return ethFrame(macBcast, macSrc, etherTypeARP, arpRequest())
	case FrameICMP:
		return ethFrame(macDst, macSrc, etherTypeIPv4, ipv4(protoICMP, icmpEcho()))
	case FrameTCP:
		return ethFrame(macDst, macSrc, etherTypeIPv4, ipv4(protoTCP, tcpSyn(51000, 443)))
	case FrameUDP:
		return ethFrame(macDst, macSrc, etherTypeIPv4, ipv4(protoUDP, udp(40000, 9999, []byte("vnprox-udp"))))
	case FrameDNS:
		return ethFrame(macDst, macSrc, etherTypeIPv4, ipv4(protoUDP, udp(52000, 53, dnsQuery())))
	case FrameDHCP:
		return ethFrame(macBcast, macSrc, etherTypeIPv4, ipv4(protoUDP, udp(68, 67, dhcpDiscover())))
	case FrameVLAN:
		// 802.1Q VLAN 100, carrying an IPv4 TCP segment.
		inner := append([]byte{0x08, 0x00}, ipv4(protoTCP, tcpSyn(51001, 80))...)
		tag := []byte{0x00, 0x64} // priority 0, VID 100
		return ethRaw(macDst, macSrc, etherTypeVLAN, append(tag, inner...))
	default:
		return ethFrame(macDst, macSrc, etherTypeIPv4, ipv4(protoICMP, icmpEcho()))
	}
}

// ethFrame builds an Ethernet II frame with a normal EtherType + payload.
func ethFrame(dst, src [6]byte, etherType uint16, payload []byte) []byte {
	frame := make([]byte, 0, 14+len(payload))
	frame = append(frame, dst[:]...)
	frame = append(frame, src[:]...)
	var et [2]byte
	binary.BigEndian.PutUint16(et[:], etherType)
	frame = append(frame, et[:]...)
	return append(frame, payload...)
}

// ethRaw is ethFrame for a payload that already carries its own inner type
// framing (VLAN tag case).
func ethRaw(dst, src [6]byte, etherType uint16, payload []byte) []byte {
	return ethFrame(dst, src, etherType, payload)
}

// ipv4 wraps an L4 payload in a minimal, checksummed IPv4 header.
func ipv4(proto byte, payload []byte) []byte {
	total := 20 + len(payload)
	hdr := make([]byte, 20)
	hdr[0] = 0x45 // version 4, IHL 5
	hdr[1] = 0x00 // DSCP/ECN
	binary.BigEndian.PutUint16(hdr[2:4], uint16(total))
	binary.BigEndian.PutUint16(hdr[4:6], 0x1234) // id
	binary.BigEndian.PutUint16(hdr[6:8], 0x4000) // flags: DF
	hdr[8] = 64                                  // TTL
	hdr[9] = proto
	copy(hdr[12:16], ipSrc[:])
	copy(hdr[16:20], ipDst[:])
	binary.BigEndian.PutUint16(hdr[10:12], ipv4Checksum(hdr))
	return append(hdr, payload...)
}

func ipv4Checksum(hdr []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(hdr); i += 2 {
		sum += uint32(hdr[i])<<8 | uint32(hdr[i+1])
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

// arpRequest builds a "who has ipDst? tell ipSrc" ARP request payload.
func arpRequest() []byte {
	b := make([]byte, 28)
	binary.BigEndian.PutUint16(b[0:2], 1)             // HTYPE: Ethernet
	binary.BigEndian.PutUint16(b[2:4], etherTypeIPv4) // PTYPE: IPv4
	b[4] = 6                                          // HLEN
	b[5] = 4                                          // PLEN
	binary.BigEndian.PutUint16(b[6:8], 1)             // OPER: request
	copy(b[8:14], macSrc[:])                          // SHA
	copy(b[14:18], ipSrc[:])                          // SPA
	// THA left zero (unknown), TPA = ipDst
	copy(b[24:28], ipDst[:])
	return b
}

func icmpEcho() []byte {
	b := make([]byte, 8)
	b[0] = 8 // echo request
	b[1] = 0
	binary.BigEndian.PutUint16(b[4:6], 0x0001) // id
	binary.BigEndian.PutUint16(b[6:8], 0x0001) // seq
	return b
}

func tcpSyn(srcPort, dstPort uint16) []byte {
	b := make([]byte, 20)
	binary.BigEndian.PutUint16(b[0:2], srcPort)
	binary.BigEndian.PutUint16(b[2:4], dstPort)
	binary.BigEndian.PutUint32(b[4:8], 0x0000abcd) // seq
	b[12] = 0x50                                   // data offset 5
	b[13] = 0x02                                   // SYN
	binary.BigEndian.PutUint16(b[14:16], 0xffff)   // window
	return b
}

func udp(srcPort, dstPort uint16, payload []byte) []byte {
	b := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint16(b[0:2], srcPort)
	binary.BigEndian.PutUint16(b[2:4], dstPort)
	binary.BigEndian.PutUint16(b[4:6], uint16(8+len(payload)))
	copy(b[8:], payload)
	return b
}

// dnsQuery is a minimal DNS query for "vnprox.local" A record.
func dnsQuery() []byte {
	b := []byte{
		0x12, 0x34, // id
		0x01, 0x00, // flags: standard query, recursion desired
		0x00, 0x01, // qdcount
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	}
	// QNAME: 6"vnprox" 5"local" 0
	b = append(b, 0x06)
	b = append(b, []byte("vnprox")...)
	b = append(b, 0x05)
	b = append(b, []byte("local")...)
	b = append(b, 0x00)
	b = append(b, 0x00, 0x01) // QTYPE A
	b = append(b, 0x00, 0x01) // QCLASS IN
	return b
}

// dhcpDiscover is a minimal BOOTP/DHCP DISCOVER payload (enough for a
// decoder to recognize op/htype/magic-cookie/message-type).
func dhcpDiscover() []byte {
	b := make([]byte, 240)
	b[0] = 0x01                                    // op: BOOTREQUEST
	b[1] = 0x01                                    // htype: Ethernet
	b[2] = 0x06                                    // hlen
	binary.BigEndian.PutUint32(b[4:8], 0x3903f326) // xid
	copy(b[28:34], macSrc[:])                      // chaddr
	// magic cookie
	b[236] = 0x63
	b[237] = 0x82
	b[238] = 0x53
	b[239] = 0x63
	// options: DHCP Message Type = DISCOVER, then End
	b = append(b, 0x35, 0x01, 0x01, 0xff)
	return b
}
