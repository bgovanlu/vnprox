// SPDX-License-Identifier: Apache-2.0

// ber.go implements exactly the BER (Basic Encoding Rules) primitives SNMPv2c
// needs: definite-length TLV framing, INTEGER, OCTET STRING, NULL, OBJECT
// IDENTIFIER, and the constructed SEQUENCE / context-tagged PDU wrapper. Not
// a general ASN.1 codec — no indefinite length, no BOOLEAN/REAL/BIT STRING,
// no support for encoding anything this package never sends (see doc.go).
//
// Every decode function here takes the remaining buffer and returns what it
// consumed plus the rest, so callers compose them without ever indexing past
// what has been validated — the discipline that makes the "no slice read
// past the declared/actual length" bound in doc.go true throughout, not just
// at the top level.

package snmp

import "fmt"

// BER universal class tags this package uses.
const (
	tagInteger     byte = 0x02
	tagOctetString byte = 0x04
	tagNull        byte = 0x05
	tagObjectID    byte = 0x06
	tagSequence    byte = 0x30
)

// BER application-class tags (SNMP's own SMI types), each primitive,
// non-constructed: class bits 01, tag number in the low 5 bits.
const (
	tagIPAddress byte = 0x40
	tagCounter32 byte = 0x41
	tagGauge32   byte = 0x42
	tagTimeTicks byte = 0x43
	tagOpaque    byte = 0x44
	tagCounter64 byte = 0x46
)

// BER context-class primitive tags used only inside a GetResponse varbind's
// value position, standing in for "this OID doesn't exist" in its various
// SNMPv2 flavors (RFC 3416 §4.2.1's exception values).
const (
	tagNoSuchObject   byte = 0x80
	tagNoSuchInstance byte = 0x81
	tagEndOfMibView   byte = 0x82
)

// maxLengthOctets bounds the BER long-form length field to 4 octets — a
// length that needs more than a uint32 to represent could never fit in
// maxMessageSize anyway, so anything wider is rejected outright rather than
// accepted into a wide accumulator (defensive parsing, doc.go).
const maxLengthOctets = 4

// appendLength appends n's BER length encoding (short form under 128, long
// form otherwise) to dst.
func appendLength(dst []byte, n int) []byte {
	if n < 0 {
		panic("snmp: negative length")
	}
	if n < 0x80 {
		return append(dst, byte(n))
	}
	var buf [8]byte
	i := len(buf)
	v := n
	for v > 0 {
		i--
		buf[i] = byte(v)
		v >>= 8
	}
	dst = append(dst, 0x80|byte(len(buf)-i))
	return append(dst, buf[i:]...)
}

// readLength decodes a BER length field from the start of b, returning the
// decoded length and the number of bytes the length field itself occupied.
// Bounded: a long-form length wider than maxLengthOctets, or one that
// declares more bytes than actually remain after it, is an error — this is
// the one bound that matters most for untrusted input, since every nested
// TLV's extent is trusted from here on down.
func readLength(b []byte) (length, consumed int, err error) {
	if len(b) == 0 {
		return 0, 0, fmt.Errorf("%w: reading length", ErrTruncated)
	}
	first := b[0]
	if first&0x80 == 0 {
		return int(first), 1, nil
	}
	numBytes := int(first &^ 0x80)
	if numBytes == 0 {
		return 0, 0, fmt.Errorf("%w: indefinite-length BER is not supported", ErrMalformed)
	}
	if numBytes > maxLengthOctets {
		return 0, 0, fmt.Errorf("%w: length field is %d octets wide (max %d)", ErrMalformed, numBytes, maxLengthOctets)
	}
	if len(b) < 1+numBytes {
		return 0, 0, fmt.Errorf("%w: reading %d-octet length field", ErrTruncated, numBytes)
	}
	length = 0
	for i := 0; i < numBytes; i++ {
		length = length<<8 | int(b[1+i])
	}
	if length < 0 || length > maxMessageSize {
		return 0, 0, fmt.Errorf("%w: declared length %d exceeds the %d-byte message bound", ErrMalformed, length, maxMessageSize)
	}
	return length, 1 + numBytes, nil
}

// readTLV reads one tag-length-value element from the start of b, returning
// its tag, its value bytes, and the remainder of b after it. Validates the
// declared length against what actually remains in b — the single check
// that makes every other decoder in this file safe to slice without a
// separate bounds check.
func readTLV(b []byte) (tag byte, value, rest []byte, err error) {
	if len(b) == 0 {
		return 0, nil, nil, fmt.Errorf("%w: reading tag", ErrTruncated)
	}
	tag = b[0]
	length, n, err := readLength(b[1:])
	if err != nil {
		return 0, nil, nil, err
	}
	start := 1 + n
	end := start + length
	if end < start || end > len(b) {
		return 0, nil, nil, fmt.Errorf("%w: declared length %d exceeds %d bytes remaining", ErrMalformed, length, len(b)-start)
	}
	return tag, b[start:end], b[end:], nil
}

// appendTLV appends tag, content's BER length, then content to dst.
func appendTLV(dst []byte, tag byte, content []byte) []byte {
	dst = append(dst, tag)
	dst = appendLength(dst, len(content))
	return append(dst, content...)
}

// --- INTEGER ---------------------------------------------------------------

// appendInteger appends a two's-complement, minimal-length BER INTEGER
// (tagged tag, so this is reused for the SEQUENCE-of-INTEGER-shaped fields:
// version, request-id, error-status/non-repeaters, error-index/
// max-repetitions).
func appendInteger(dst []byte, tag byte, v int32) []byte {
	return appendTLV(dst, tag, encodeSignedMinimal(int64(v)))
}

func encodeSignedMinimal(v int64) []byte {
	// Minimal two's-complement big-endian encoding: strip leading 0x00 (for
	// non-negative v) or 0xff (for negative v) bytes, but keep exactly one
	// byte whose sign bit matches v's sign, per BER INTEGER's definition.
	var buf [8]byte
	for i := range buf {
		buf[i] = byte(v >> uint(8*(7-i)))
	}
	i := 0
	for i < 7 {
		b0, b1 := buf[i], buf[i+1]
		if b0 == 0x00 && b1&0x80 == 0 {
			i++
			continue
		}
		if b0 == 0xff && b1&0x80 != 0 {
			i++
			continue
		}
		break
	}
	return buf[i:]
}

// decodeSignedInt decodes a BER INTEGER's content octets (two's complement,
// big-endian, at most 8 octets — wider is rejected as malformed rather than
// decoded into a value that silently overflows int64).
func decodeSignedInt(content []byte) (int64, error) {
	if len(content) == 0 {
		return 0, fmt.Errorf("%w: zero-length INTEGER", ErrMalformed)
	}
	if len(content) > 8 {
		return 0, fmt.Errorf("%w: INTEGER is %d octets wide (max 8)", ErrMalformed, len(content))
	}
	v := int64(int8(content[0])) // sign-extend from the first octet
	for _, b := range content[1:] {
		v = v<<8 | int64(b)
	}
	return v, nil
}

// decodeUnsignedInt decodes an SNMP unsigned application type's content
// octets (Counter32/Gauge32/Unsigned32/TimeTicks/Counter64) — BER-encoded as
// plain big-endian octets, MSB-first, with a leading 0x00 padding octet
// permitted (and expected, per RFC 2578, whenever the true value's top bit
// is set) purely to keep the encoding from looking like a negative INTEGER.
// Bounded to 9 octets: 8 value octets plus at most one 0x00 pad, enough for
// the full Counter64 range and nothing wider (defensive parsing).
func decodeUnsignedInt(content []byte) (uint64, error) {
	if len(content) == 0 {
		return 0, fmt.Errorf("%w: zero-length unsigned integer", ErrMalformed)
	}
	if len(content) > 9 {
		return 0, fmt.Errorf("%w: unsigned integer is %d octets wide (max 9)", ErrMalformed, len(content))
	}
	var v uint64
	for _, b := range content {
		v = v<<8 | uint64(b)
	}
	return v, nil
}

// --- OCTET STRING / NULL ----------------------------------------------------

func appendOctetString(dst []byte, s []byte) []byte {
	return appendTLV(dst, tagOctetString, s)
}

func appendNull(dst []byte) []byte {
	return appendTLV(dst, tagNull, nil)
}

// --- OBJECT IDENTIFIER -------------------------------------------------------

func appendOID(dst []byte, oid OID) []byte {
	return appendTLV(dst, tagObjectID, encodeOIDContent(oid))
}

func encodeOIDContent(oid OID) []byte {
	if len(oid) == 0 {
		return nil
	}
	first := oid[0]
	second := uint32(0)
	if len(oid) > 1 {
		second = oid[1]
	}
	var out []byte
	out = appendBase128(out, uint64(first)*40+uint64(second))
	for _, sub := range oid[min(2, len(oid)):] {
		out = appendBase128(out, uint64(sub))
	}
	return out
}

func appendBase128(dst []byte, v uint64) []byte {
	var tmp [10]byte
	i := len(tmp)
	i--
	tmp[i] = byte(v & 0x7f)
	v >>= 7
	for v > 0 {
		i--
		tmp[i] = byte(v&0x7f) | 0x80
		v >>= 7
	}
	return append(dst, tmp[i:]...)
}

// decodeOID decodes an OBJECT IDENTIFIER's content octets. Bounded: at most
// maxOIDComponents resulting sub-identifiers, and each base-128 group is
// capped at 5 continuation octets (enough for a full uint32, which is all
// any sub-identifier this package's callers care about needs) — a longer
// group is rejected rather than accumulated into a value that silently
// wraps.
func decodeOID(content []byte) (OID, error) {
	if len(content) == 0 {
		return nil, fmt.Errorf("%w: zero-length OBJECT IDENTIFIER", ErrMalformed)
	}
	var subIDs []uint32
	var acc uint64
	groupLen := 0
	for _, b := range content {
		groupLen++
		if groupLen > 5 {
			return nil, fmt.Errorf("%w: OID sub-identifier encoding is too wide", ErrMalformed)
		}
		acc = acc<<7 | uint64(b&0x7f)
		if b&0x80 != 0 {
			continue // continuation bit set: more octets in this group
		}
		if acc > 0xffffffff {
			return nil, fmt.Errorf("%w: OID sub-identifier %d exceeds uint32", ErrMalformed, acc)
		}
		subIDs = append(subIDs, uint32(acc))
		if len(subIDs) > maxOIDComponents {
			return nil, fmt.Errorf("%w: OID has more than %d components", ErrMalformed, maxOIDComponents)
		}
		acc = 0
		groupLen = 0
	}
	if groupLen != 0 {
		return nil, fmt.Errorf("%w: OID content ends mid sub-identifier", ErrTruncated)
	}
	if len(subIDs) == 0 {
		return nil, fmt.Errorf("%w: OID decoded to zero components", ErrMalformed)
	}
	first := subIDs[0] / 40
	second := subIDs[0] % 40
	if first > 2 {
		// The x*40+y encoding is only well-defined for x in {0,1,2}; a real
		// BER encoder never produces x>2, so treat it as x=2 the way most
		// implementations do (RFC 2578's own convention) rather than reject
		// an otherwise well-formed OID.
		first = 2
		second = subIDs[0] - 80
	}
	out := make(OID, 0, len(subIDs)+1)
	out = append(out, first, second)
	out = append(out, subIDs[1:]...)
	return out, nil
}
