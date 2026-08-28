// SPDX-License-Identifier: Apache-2.0

package snmp

import (
	"errors"
	"testing"
)

func TestEncodeGetRequest_DecodesBack(t *testing.T) {
	oids := []OID{MustParseOID("1.3.6.1.2.1.1.1.0"), MustParseOID("1.3.6.1.2.1.2.2.1.14.1")}
	raw, err := EncodeGetRequest([]byte("public"), 42, oids)
	if err != nil {
		t.Fatalf("EncodeGetRequest: %v", err)
	}
	msg, err := DecodeMessage(raw)
	if err != nil {
		t.Fatalf("DecodeMessage: %v", err)
	}
	if msg.Version != version2c {
		t.Errorf("version = %d, want %d", msg.Version, version2c)
	}
	if string(msg.Community) != "public" {
		t.Errorf("community = %q, want %q", msg.Community, "public")
	}
	if msg.PDU.Type != GetRequestPDU {
		t.Errorf("PDU type = %s, want GetRequest", msg.PDU.Type)
	}
	if msg.PDU.RequestID != 42 {
		t.Errorf("request-id = %d, want 42", msg.PDU.RequestID)
	}
	if len(msg.PDU.Varbinds) != 2 {
		t.Fatalf("varbinds = %d, want 2", len(msg.PDU.Varbinds))
	}
	for i, vb := range msg.PDU.Varbinds {
		if !vb.Name.Equal(oids[i]) {
			t.Errorf("varbind %d name = %s, want %s", i, vb.Name, oids[i])
		}
		if vb.Value.Kind != KindNull {
			t.Errorf("varbind %d value kind = %v, want KindNull (a request's placeholder value)", i, vb.Value.Kind)
		}
	}
}

func TestEncodeGetBulkRequest_FieldsCarryNonRepeatersMaxRepetitions(t *testing.T) {
	raw, err := EncodeGetBulkRequest([]byte("public"), 7, 0, 10, []OID{MustParseOID("1.3.6.1.2.1.2.2.1.2")})
	if err != nil {
		t.Fatalf("EncodeGetBulkRequest: %v", err)
	}
	msg, err := DecodeMessage(raw)
	if err != nil {
		t.Fatalf("DecodeMessage: %v", err)
	}
	if msg.PDU.Type != GetBulkRequestPDU {
		t.Fatalf("PDU type = %s, want GetBulkRequest", msg.PDU.Type)
	}
	if msg.PDU.Field2 != 0 {
		t.Errorf("non-repeaters = %d, want 0", msg.PDU.Field2)
	}
	if msg.PDU.Field3 != 10 {
		t.Errorf("max-repetitions = %d, want 10", msg.PDU.Field3)
	}
}

// buildGetResponse hand-assembles a GetResponse datagram carrying one
// varbind of the given tag/content, for decode tests (and FuzzDecodeMessage's
// corpus seeding, fuzz_test.go) that need to control exactly what a "device"
// sent without going through a Client/UDP socket. Deliberately takes no
// *testing.T: it makes no assertions of its own, and fuzz_test.go needs to
// call it outside of any single test's context to seed f.Add.
func buildGetResponse(requestID int32, valueTag byte, valueContent []byte, oid OID) []byte {
	var vb []byte
	vb = appendOID(vb, oid)
	vb = appendTLV(vb, valueTag, valueContent)
	var vbList []byte
	vbList = appendTLV(vbList, tagSequence, vb)

	var pduBody []byte
	pduBody = appendInteger(pduBody, tagInteger, requestID)
	pduBody = appendInteger(pduBody, tagInteger, 0)
	pduBody = appendInteger(pduBody, tagInteger, 0)
	pduBody = appendTLV(pduBody, tagSequence, vbList)
	pduBytes := appendTLV(nil, byte(GetResponsePDU), pduBody)

	var body []byte
	body = appendInteger(body, tagInteger, version2c)
	body = appendOctetString(body, []byte("public"))
	body = append(body, pduBytes...)
	return appendTLV(nil, tagSequence, body)
}

func TestDecodeMessage_ValueKinds(t *testing.T) {
	oid := MustParseOID("1.3.6.1.2.1.2.2.1.14.1")
	tests := []struct {
		name    string
		content []byte
		tag     byte
		want    ValueKind
	}{
		{name: "Counter32", tag: tagCounter32, content: []byte{0x00, 0xff, 0xff, 0xff, 0xff}, want: KindCounter32},
		{name: "Counter64", tag: tagCounter64, content: []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}, want: KindCounter64},
		{name: "Gauge32", tag: tagGauge32, content: []byte{0x00}, want: KindGauge32},
		{name: "NoSuchInstance", tag: tagNoSuchInstance, content: nil, want: KindNoSuchInstance},
		{name: "NoSuchObject", tag: tagNoSuchObject, content: nil, want: KindNoSuchObject},
		{name: "EndOfMibView", tag: tagEndOfMibView, content: nil, want: KindEndOfMibView},
		{name: "OctetString", tag: tagOctetString, content: []byte("eth0"), want: KindOctetString},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := buildGetResponse(1, tt.tag, tt.content, oid)
			msg, err := DecodeMessage(raw)
			if err != nil {
				t.Fatalf("DecodeMessage: %v", err)
			}
			if len(msg.PDU.Varbinds) != 1 {
				t.Fatalf("varbinds = %d, want 1", len(msg.PDU.Varbinds))
			}
			got := msg.PDU.Varbinds[0].Value
			if got.Kind != tt.want {
				t.Errorf("kind = %v, want %v", got.Kind, tt.want)
			}
		})
	}
}

func TestDecodeMessage_TrailingBytesRejected(t *testing.T) {
	raw := buildGetResponse(1, tagCounter32, []byte{0x01}, MustParseOID("1.3.6.1.2.1.2.2.1.14.1"))
	raw = append(raw, 0xde, 0xad)
	_, err := DecodeMessage(raw)
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("DecodeMessage with trailing bytes = %v, want ErrMalformed", err)
	}
}

func TestDecodeMessage_WrongEnvelopeTag(t *testing.T) {
	_, err := DecodeMessage([]byte{tagInteger, 0x01, 0x00})
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("DecodeMessage with non-SEQUENCE envelope = %v, want ErrMalformed", err)
	}
}

func TestDecodeMessage_TooManyVarbinds(t *testing.T) {
	oid := MustParseOID("1.3.6.1.2.1.2.2.1.14.1")
	var vbList []byte
	for i := 0; i < maxVarbindsPerMessage+1; i++ {
		var vb []byte
		vb = appendOID(vb, oid)
		vb = appendNull(vb)
		vbList = appendTLV(vbList, tagSequence, vb)
	}
	var pduBody []byte
	pduBody = appendInteger(pduBody, tagInteger, 1)
	pduBody = appendInteger(pduBody, tagInteger, 0)
	pduBody = appendInteger(pduBody, tagInteger, 0)
	pduBody = appendTLV(pduBody, tagSequence, vbList)
	pduBytes := appendTLV(nil, byte(GetResponsePDU), pduBody)
	var body []byte
	body = appendInteger(body, tagInteger, version2c)
	body = appendOctetString(body, []byte("public"))
	body = append(body, pduBytes...)
	raw := appendTLV(nil, tagSequence, body)

	_, err := DecodeMessage(raw)
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("DecodeMessage with %d varbinds = %v, want ErrMalformed", maxVarbindsPerMessage+1, err)
	}
}

func TestEncodePDU_UnsupportedTypeErrorPropagates(t *testing.T) {
	_, err := encodeMessage([]byte("public"), PDUType(0xff), 1, 0, 0, nil)
	if !errors.Is(err, ErrUnsupportedPDUType) {
		t.Fatalf("encodeMessage(unsupported type) = %v, want ErrUnsupportedPDUType", err)
	}
}
