// SPDX-License-Identifier: Apache-2.0

package lacphash

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
)

// Policy is one of the five xmit_hash_policy values the Linux bonding
// driver accepts — the identical vocabulary internal/change/
// validate_schema.go's validXmitHashPolicies already validates a
// changeset's BondCreateParams/BondUpdateParams.XmitHashPolicy against
// (kept as a distinct, named type here rather than a bare string so an
// unrecognized value is caught at any switch site, not just at validation
// time).
type Policy string

const (
	PolicyLayer2  Policy = "layer2"
	PolicyLayer23 Policy = "layer2+3"
	PolicyLayer34 Policy = "layer3+4"
	PolicyEncap23 Policy = "encap2+3"
	PolicyEncap34 Policy = "encap3+4"
)

// Valid reports whether p is one of the five policies the kernel bonding
// driver (and validate_schema.go's validXmitHashPolicies) recognizes.
func (p Policy) Valid() bool {
	switch p {
	case PolicyLayer2, PolicyLayer23, PolicyLayer34, PolicyEncap23, PolicyEncap34:
		return true
	default:
		return false
	}
}

var (
	// ErrUnknownPolicy is returned by Hash/SelectSlave for a Policy value
	// outside the five Valid() recognizes.
	ErrUnknownPolicy = errors.New("lacphash: unrecognized xmit_hash_policy")
	// ErrMACRequired is returned when policy is PolicyLayer2 or
	// PolicyLayer23 (or PolicyEncap23, which this package treats the same
	// way — see predict.go's doc comment on the encap simplification) and
	// the tuple carries no source/destination MAC. See doc.go: this is
	// the expected, structural outcome for every flow this package is
	// asked to hash from internal/flow.Record data, since that type never
	// carries MAC addresses at all.
	ErrMACRequired = errors.New("lacphash: policy requires source/destination MAC addresses, tuple has none")
	// ErrIPRequired is returned when the policy needs IP addresses (every
	// policy except plain PolicyLayer2) and the tuple carries none.
	ErrIPRequired = errors.New("lacphash: policy requires source/destination IP addresses, tuple has none")
	// ErrNoSlaves is returned by SelectSlave when numSlaves <= 0 — a bond
	// with no eligible (up) slave cannot select one.
	ErrNoSlaves = errors.New("lacphash: bond has no eligible slaves")
)

// FlowTuple is the subset of a flow's identity the kernel's xmit hash
// policies consult. A nil/zero field means "unknown to the caller", never
// "hash as zero" — Hash returns ErrMACRequired/ErrIPRequired rather than
// silently hashing an absent value that would look like real data.
type FlowTuple struct {
	// SrcMAC/DstMAC are the flow's Ethernet source/destination hardware
	// addresses, consulted by PolicyLayer2/PolicyLayer23/PolicyEncap23
	// only. internal/flow.Record never carries these (see doc.go) —
	// populated here only when a caller has them from some other source
	// (a lab-simulated fixture, or a test tuple).
	SrcMAC, DstMAC net.HardwareAddr
	SrcIP, DstIP   net.IP

	// EtherType is the Ethernet frame's type field (0x0800 = IPv4,
	// 0x86DD = IPv6 — IANA "IEEE 802 Numbers"), folded into the
	// layer2/layer2+3 hash per bonding.rst's stated formula ("packet type
	// ID field"). Every IPv4 flow carries the same EtherType, so on its
	// own it never discriminates between two IPv4 flows — only between an
	// IPv4 and an IPv6 one — but is included because the documented
	// formula includes it. Zero defaults to the value implied by
	// SrcIP/DstIP's own address family (see etherType), since every
	// tuple this package is fed in practice comes from an IPv4 or IPv6
	// flow, never a genuinely unknown L2 payload.
	EtherType uint16

	SrcPort, DstPort uint16
	// Proto is the IP protocol number (6=tcp, 17=udp, ... — the same
	// vocabulary internal/flow.Record.Proto already uses).
	Proto uint8
}

// Hash computes the kernel bonding driver's xmit-hash value for t under
// policy, per Documentation/networking/bonding.rst's formula for that
// policy (see doc.go for how closely this is believed to track the actual
// kernel arithmetic). It returns the *pre-modulo* 32-bit hash —
// SelectSlave applies the final "modulo slave count" step, kept separate
// so a caller can re-bucket into a slave count that changes (a slave
// joining/leaving the active aggregate) without recomputing the flow-side
// hash.
func Hash(policy Policy, t FlowTuple) (uint32, error) {
	switch policy {
	case PolicyLayer2:
		return hashLayer2(t)
	case PolicyLayer23, PolicyEncap23:
		return hashLayer23(t)
	case PolicyLayer34, PolicyEncap34:
		return hashLayer34(t)
	default:
		return 0, fmt.Errorf("%w: %q", ErrUnknownPolicy, policy)
	}
}

// SelectSlave computes the final "modulo slave count" step of the kernel's
// xmit path: Hash(policy, t) taken modulo numSlaves is the index into an
// ordered slave list — ordered exactly as the bond's own enslave order
// (inventory.Bond.Slaves / BondSlaveState, index 0 the first-enslaved
// slave) — that the kernel would choose. Returns ErrNoSlaves if
// numSlaves <= 0, and whatever Hash itself returns
// (ErrMACRequired/ErrIPRequired/ErrUnknownPolicy) when the tuple can't be
// hashed under policy.
func SelectSlave(policy Policy, t FlowTuple, numSlaves int) (int, error) {
	if numSlaves <= 0 {
		return 0, ErrNoSlaves
	}
	hash, err := Hash(policy, t)
	if err != nil {
		return 0, err
	}
	return int(hash % uint32(numSlaves)), nil
}

// hashLayer2 implements bonding.rst's layer2 formula verbatim:
//
//	hash = source MAC XOR destination MAC XOR packet type ID
//	slave number = hash modulo slave count
//
// No shift/fold step — bonding.rst states one only for layer2+3/layer3+4,
// not for plain layer2.
func hashLayer2(t FlowTuple) (uint32, error) {
	if len(t.SrcMAC) == 0 || len(t.DstMAC) == 0 {
		return 0, ErrMACRequired
	}
	return macXOR(t.SrcMAC, t.DstMAC) ^ uint32(etherType(t)), nil
}

// hashLayer23 implements bonding.rst's layer2+3 formula:
//
//	hash = source MAC XOR destination MAC XOR packet type ID
//	hash = hash XOR source IP XOR destination IP
//	hash = hash XOR (hash RSHIFT 16)
//	hash = hash XOR (hash RSHIFT 8)
//	slave number = hash modulo slave count
func hashLayer23(t FlowTuple) (uint32, error) {
	if len(t.SrcMAC) == 0 || len(t.DstMAC) == 0 {
		return 0, ErrMACRequired
	}
	if t.SrcIP == nil || t.DstIP == nil {
		return 0, ErrIPRequired
	}
	hash := macXOR(t.SrcMAC, t.DstMAC) ^ uint32(etherType(t))
	hash ^= ipXOR(t.SrcIP)
	hash ^= ipXOR(t.DstIP)
	return foldHash(hash), nil
}

// hashLayer34 implements bonding.rst's layer3+4 formula:
//
//	hash = source IP XOR destination IP
//	hash = hash XOR (hash RSHIFT 16)
//	hash = hash XOR (hash RSHIFT 8)
//	[if the upper-layer protocol is TCP or UDP:]
//	hash = hash XOR (source port XOR destination port)
//	slave number = hash modulo slave count
//
// bonding.rst states the port XOR is added in "when available" for TCP/UDP
// specifically — this package follows that literally (not SCTP or any
// other port-bearing protocol) and folds once, after combining IP and
// port, rather than folding twice; that ordering choice is this package's
// own reading of the documented formula, not independently confirmed
// against kernel source (see doc.go).
func hashLayer34(t FlowTuple) (uint32, error) {
	if t.SrcIP == nil || t.DstIP == nil {
		return 0, ErrIPRequired
	}
	hash := ipXOR(t.SrcIP) ^ ipXOR(t.DstIP)
	if isTCPOrUDP(t.Proto) && (t.SrcPort != 0 || t.DstPort != 0) {
		hash ^= uint32(t.SrcPort) ^ uint32(t.DstPort)
	}
	return foldHash(hash), nil
}

// isTCPOrUDP reports whether proto is the IP protocol number for TCP (6)
// or UDP (17) — the two protocols bonding.rst names for the port-XOR step.
func isTCPOrUDP(proto uint8) bool {
	return proto == 6 || proto == 17
}

// foldHash applies bonding.rst's two-step shift-fold, shared verbatim by
// layer2+3 and layer3+4: hash ^= hash>>16; hash ^= hash>>8.
func foldHash(hash uint32) uint32 {
	hash ^= hash >> 16
	hash ^= hash >> 8
	return hash
}

// etherType returns t.EtherType when set, else the value implied by
// whichever of SrcIP/DstIP is present: 0x0800 (IPv4) or 0x86DD (IPv6),
// preferring DstIP since it is populated in every flow direction Predict
// feeds this package. Defaults to 0x0800 when neither IP is set (the
// PolicyLayer2 path, which needs no IP at all).
func etherType(t FlowTuple) uint16 {
	if t.EtherType != 0 {
		return t.EtherType
	}
	ip := t.DstIP
	if ip == nil {
		ip = t.SrcIP
	}
	if ip != nil && ip.To4() == nil && ip.To16() != nil {
		return 0x86DD
	}
	return 0x0800
}

// macXOR XORs two hardware addresses byte-wise (missing bytes on either
// side treated as zero, so a caller need not zero-pad) and folds the
// resulting 48-bit value down to 32 bits by XORing the high 16 bits (the
// address's 6th-from-last/5th-from-last bytes) into the low 32 — the same
// "fold the wide XOR down by combining the extra high bits into the low
// word" idea foldHash applies to the full hash, applied here first because
// a MAC XOR is 48 bits wide and this package works in native uint32
// hashes throughout. This fold is this package's own choice for
// compressing a MAC-width value into the formula's word size, not itself
// part of bonding.rst's stated text (which just says "XOR of hardware MAC
// addresses" without specifying a bit width) — flagged per doc.go's
// documentation-derived caveat.
func macXOR(a, b net.HardwareAddr) uint32 {
	var x [6]byte
	for i := range x {
		var av, bv byte
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		x[i] = av ^ bv
	}
	hi := uint32(x[0])<<8 | uint32(x[1])
	lo := binary.BigEndian.Uint32(x[2:6])
	return lo ^ hi
}

// ipXOR reduces an IP address to a 32-bit value for the XOR formula: an
// IPv4 address maps directly (bonding.rst's formula predates any stated
// IPv6 handling). An IPv6 address is folded down by XORing its four
// 32-bit words together — this package's own, undocumented-upstream
// choice (flagged per doc.go), consistent with the "fold a wider value
// down by XORing its words" approach macXOR/foldHash already use.
func ipXOR(ip net.IP) uint32 {
	if v4 := ip.To4(); v4 != nil {
		return binary.BigEndian.Uint32(v4)
	}
	v6 := ip.To16()
	if v6 == nil {
		return 0
	}
	var h uint32
	for i := 0; i < 16; i += 4 {
		h ^= binary.BigEndian.Uint32(v6[i : i+4])
	}
	return h
}
