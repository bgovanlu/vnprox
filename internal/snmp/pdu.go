// SPDX-License-Identifier: Apache-2.0

package snmp

import "fmt"

// version2c is the SNMP message version field's value for v2c (RFC 3416)
// — 0 is v1, 1 is v2c, 3 is v3 (this package never sends v1 or v3: no v1
// community-based trap quirks to support, and v3 needs USM auth/priv this
// package deliberately does not implement, see doc.go).
const version2c int32 = 1

// PDUType identifies an SNMP PDU by its BER context-class constructed tag.
// This is a CLOSED set: the only values this package declares are the ones
// listed below — no constant for RFC 3416's write PDU, its two notification
// PDUs, or its Report-PDU, and encodePDU rejects every PDUType value that is
// not one of these four by construction, not by convention — see
// TestEncodePDU_RejectsEveryUndeclaredType in noset_test.go, which checks
// that exhaustively over the full byte range.
type PDUType byte

const (
	GetRequestPDU     PDUType = 0xA0
	GetNextRequestPDU PDUType = 0xA1
	GetResponsePDU    PDUType = 0xA2
	GetBulkRequestPDU PDUType = 0xA5
)

func (t PDUType) String() string {
	switch t {
	case GetRequestPDU:
		return "GetRequest"
	case GetNextRequestPDU:
		return "GetNextRequest"
	case GetResponsePDU:
		return "GetResponse"
	case GetBulkRequestPDU:
		return "GetBulkRequest"
	default:
		return fmt.Sprintf("PDUType(0x%02x)", byte(t))
	}
}

// Varbind is one name/value pair from a PDU's variable-bindings list.
type Varbind struct {
	Name  OID
	Value Value
}

// PDU is the decoded shape of an SNMP request or response body, common to
// all four PDUTypes this package handles. Field2/Field3 carry different
// meanings depending on Type: for GetRequest/GetNextRequest/GetResponse they
// are error-status/error-index (0/0 on a request, an agent's report on a
// response); for GetBulkRequest they are non-repeaters/max-repetitions
// (RFC 3416 §3's GetBulkRequest-PDU reuses the same two integer slots for a
// different purpose, which is why this package names them positionally
// rather than duplicating the struct).
type PDU struct {
	// Varbinds is a slice (pointer-containing) and ordered first per
	// docs/development.md's densest-pointer-first field order.
	Varbinds  []Varbind
	Type      PDUType
	RequestID int32
	Field2    int32 // error-status | non-repeaters
	Field3    int32 // error-index  | max-repetitions
}

// Message is a full SNMPv2c datagram: version, community, and one PDU.
type Message struct {
	Community []byte
	PDU       PDU
	Version   int32
}

// encodePDU builds the wire bytes for one PDU. Returns ErrUnsupportedPDUType
// for any pduType this package does not declare above — the single
// enforcement point for "this client can never emit a write PDU" (see
// doc.go and noset_test.go).
func encodePDU(pduType PDUType, requestID, field2, field3 int32, varbinds []Varbind) ([]byte, error) {
	switch pduType {
	case GetRequestPDU, GetNextRequestPDU, GetBulkRequestPDU, GetResponsePDU:
		// declared, allowed
	default:
		return nil, fmt.Errorf("%w: 0x%02x", ErrUnsupportedPDUType, byte(pduType))
	}

	var vbList []byte
	for _, vb := range varbinds {
		var content []byte
		content = appendOID(content, vb.Name)
		content = appendNull(content) // every request this package sends uses a NULL placeholder value
		vbList = appendTLV(vbList, tagSequence, content)
	}

	var body []byte
	body = appendInteger(body, tagInteger, requestID)
	body = appendInteger(body, tagInteger, field2)
	body = appendInteger(body, tagInteger, field3)
	body = appendTLV(body, tagSequence, vbList)

	return appendTLV(nil, byte(pduType), body), nil
}

// EncodeGetRequest builds a full SNMPv2c GetRequest datagram for oids.
func EncodeGetRequest(community []byte, requestID int32, oids []OID) ([]byte, error) {
	return encodeMessage(community, GetRequestPDU, requestID, 0, 0, oids)
}

// EncodeGetNextRequest builds a full SNMPv2c GetNextRequest datagram for
// oids.
func EncodeGetNextRequest(community []byte, requestID int32, oids []OID) ([]byte, error) {
	return encodeMessage(community, GetNextRequestPDU, requestID, 0, 0, oids)
}

// EncodeGetBulkRequest builds a full SNMPv2c GetBulkRequest datagram.
// nonRepeaters/maxRepetitions are RFC 3416 §3's own fields — for a single
// table walk (this package's only use, in internal/ifcounters), nonRepeaters
// is 0 and maxRepetitions bounds how many rows the agent may return.
func EncodeGetBulkRequest(community []byte, requestID, nonRepeaters, maxRepetitions int32, oids []OID) ([]byte, error) {
	return encodeMessage(community, GetBulkRequestPDU, requestID, nonRepeaters, maxRepetitions, oids)
}

func encodeMessage(community []byte, pduType PDUType, requestID, field2, field3 int32, oids []OID) ([]byte, error) {
	varbinds := make([]Varbind, len(oids))
	for i, oid := range oids {
		varbinds[i] = Varbind{Name: oid}
	}
	pduBytes, err := encodePDU(pduType, requestID, field2, field3, varbinds)
	if err != nil {
		return nil, err
	}
	var body []byte
	body = appendInteger(body, tagInteger, version2c)
	body = appendOctetString(body, community)
	body = append(body, pduBytes...)
	return appendTLV(nil, tagSequence, body), nil
}

// maxVarbindsPerMessage bounds how many varbinds this package will decode
// out of a single response — a defensive cap independent of maxMessageSize,
// since a pathological encoding could in principle pack many small varbinds
// into a bounded byte count.
const maxVarbindsPerMessage = 256

// DecodeMessage decodes a raw SNMPv2c datagram. Never panics: every error
// path returns a wrapped ErrMalformed/ErrTruncated instead (Client.Get/
// GetBulk additionally recover as belt-and-braces — see client.go).
func DecodeMessage(raw []byte) (Message, error) {
	if len(raw) > maxMessageSize {
		return Message{}, fmt.Errorf("%w: datagram is %d bytes (max %d)", ErrMalformed, len(raw), maxMessageSize)
	}
	tag, content, rest, err := readTLV(raw)
	if err != nil {
		return Message{}, fmt.Errorf("reading message envelope: %w", err)
	}
	if tag != tagSequence {
		return Message{}, fmt.Errorf("%w: message envelope has tag 0x%02x, want SEQUENCE", ErrMalformed, tag)
	}
	if len(rest) != 0 {
		return Message{}, fmt.Errorf("%w: %d trailing bytes after message", ErrMalformed, len(rest))
	}

	tag, verContent, content, err := readTLV(content)
	if err != nil {
		return Message{}, fmt.Errorf("reading version: %w", err)
	}
	if tag != tagInteger {
		return Message{}, fmt.Errorf("%w: version has tag 0x%02x, want INTEGER", ErrMalformed, tag)
	}
	version, err := decodeSignedInt(verContent)
	if err != nil {
		return Message{}, fmt.Errorf("decoding version: %w", err)
	}

	tag, commContent, content, err := readTLV(content)
	if err != nil {
		return Message{}, fmt.Errorf("reading community: %w", err)
	}
	if tag != tagOctetString {
		return Message{}, fmt.Errorf("%w: community has tag 0x%02x, want OCTET STRING", ErrMalformed, tag)
	}

	pduTag, pduContent, content, err := readTLV(content)
	if err != nil {
		return Message{}, fmt.Errorf("reading PDU: %w", err)
	}
	if len(content) != 0 {
		return Message{}, fmt.Errorf("%w: %d bytes after PDU", ErrMalformed, len(content))
	}

	pdu, err := decodePDU(PDUType(pduTag), pduContent)
	if err != nil {
		return Message{}, err
	}

	return Message{
		Version:   int32(version),
		Community: append([]byte(nil), commContent...),
		PDU:       pdu,
	}, nil
}

func decodePDU(pduType PDUType, content []byte) (PDU, error) {
	tag, ridContent, rest, err := readTLV(content)
	if err != nil {
		return PDU{}, fmt.Errorf("reading PDU request-id: %w", err)
	}
	if tag != tagInteger {
		return PDU{}, fmt.Errorf("%w: request-id has tag 0x%02x, want INTEGER", ErrMalformed, tag)
	}
	requestID, err := decodeSignedInt(ridContent)
	if err != nil {
		return PDU{}, fmt.Errorf("decoding request-id: %w", err)
	}

	tag, f2Content, rest, err := readTLV(rest)
	if err != nil {
		return PDU{}, fmt.Errorf("reading PDU field2: %w", err)
	}
	if tag != tagInteger {
		return PDU{}, fmt.Errorf("%w: PDU field2 has tag 0x%02x, want INTEGER", ErrMalformed, tag)
	}
	field2, err := decodeSignedInt(f2Content)
	if err != nil {
		return PDU{}, fmt.Errorf("decoding PDU field2: %w", err)
	}

	tag, f3Content, rest, err := readTLV(rest)
	if err != nil {
		return PDU{}, fmt.Errorf("reading PDU field3: %w", err)
	}
	if tag != tagInteger {
		return PDU{}, fmt.Errorf("%w: PDU field3 has tag 0x%02x, want INTEGER", ErrMalformed, tag)
	}
	field3, err := decodeSignedInt(f3Content)
	if err != nil {
		return PDU{}, fmt.Errorf("decoding PDU field3: %w", err)
	}

	tag, vbListContent, rest, err := readTLV(rest)
	if err != nil {
		return PDU{}, fmt.Errorf("reading varbind list: %w", err)
	}
	if tag != tagSequence {
		return PDU{}, fmt.Errorf("%w: varbind list has tag 0x%02x, want SEQUENCE", ErrMalformed, tag)
	}
	if len(rest) != 0 {
		return PDU{}, fmt.Errorf("%w: %d bytes after varbind list", ErrMalformed, len(rest))
	}

	varbinds, err := decodeVarbinds(vbListContent)
	if err != nil {
		return PDU{}, err
	}

	return PDU{
		Type:      pduType,
		RequestID: int32(requestID),
		Field2:    int32(field2),
		Field3:    int32(field3),
		Varbinds:  varbinds,
	}, nil
}

func decodeVarbinds(content []byte) ([]Varbind, error) {
	var out []Varbind
	for len(content) > 0 {
		if len(out) >= maxVarbindsPerMessage {
			return nil, fmt.Errorf("%w: response has more than %d varbinds", ErrMalformed, maxVarbindsPerMessage)
		}
		tag, vbContent, rest, err := readTLV(content)
		if err != nil {
			return nil, fmt.Errorf("reading varbind %d: %w", len(out), err)
		}
		if tag != tagSequence {
			return nil, fmt.Errorf("%w: varbind %d has tag 0x%02x, want SEQUENCE", ErrMalformed, len(out), tag)
		}
		vb, err := decodeVarbind(vbContent)
		if err != nil {
			return nil, fmt.Errorf("decoding varbind %d: %w", len(out), err)
		}
		out = append(out, vb)
		content = rest
	}
	return out, nil
}

func decodeVarbind(content []byte) (Varbind, error) {
	tag, nameContent, rest, err := readTLV(content)
	if err != nil {
		return Varbind{}, fmt.Errorf("reading name: %w", err)
	}
	if tag != tagObjectID {
		return Varbind{}, fmt.Errorf("%w: varbind name has tag 0x%02x, want OBJECT IDENTIFIER", ErrMalformed, tag)
	}
	name, err := decodeOID(nameContent)
	if err != nil {
		return Varbind{}, fmt.Errorf("decoding name: %w", err)
	}

	valTag, valContent, rest, err := readTLV(rest)
	if err != nil {
		return Varbind{}, fmt.Errorf("reading value: %w", err)
	}
	if len(rest) != 0 {
		return Varbind{}, fmt.Errorf("%w: %d trailing bytes in varbind", ErrMalformed, len(rest))
	}
	value, err := decodeValue(valTag, valContent)
	if err != nil {
		return Varbind{}, fmt.Errorf("decoding value for %s: %w", name, err)
	}
	return Varbind{Name: name, Value: value}, nil
}
