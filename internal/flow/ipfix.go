// SPDX-License-Identifier: Apache-2.0

package flow

import "fmt"

// IPFIX Set id conventions (RFC 7011 §3.3.2).
const (
	ipfixTemplateSetID        = 2
	ipfixOptionsTemplateSetID = 3
	ipfixMinDataSetID         = 256
)

// ipfixEnterpriseBit marks a template field's Information Element id as
// enterprise-specific (RFC 7011 §3.4.2.2): the top bit of the 16-bit field
// id is set, and the field spec carries an extra 4-byte enterprise number
// this decoder reads past (to stay aligned) but never maps onto Record —
// this package understands only the standard IANA element set (template.go's
// ie* constants), which by definition never sets this bit.
const ipfixEnterpriseBit = 0x8000

// DecodeIPFIX decodes one IPFIX UDP payload (RFC 7011) into normalized
// Records, sharing NetFlow v9's exact template-cache mechanics (cache) —
// IPFIX is NetFlow v9's IETF-standardized successor with a near-identical
// template/data-set structure, differing mainly in header layout and the
// enterprise-bit template field extension handled here. Same dropped/
// "unknown template" convention as DecodeNetFlow9.
//
// Variable-length Information Elements (RFC 7011 §7's length 0xFFFF
// encoding) are not supported: a template field declaring that sentinel
// length is recorded as length 0 (its data-record bytes are then
// undecodable and that data set is skipped, counted via dropped, not
// errored) — every fixture this package ships uses fixed-length fields
// only; see planning/reports/needs-hardware-validation.md for real-world
// exporters that might require it.
func DecodeIPFIX(data []byte, node, exporterKey string, cache *TemplateCache) (records []Record, dropped int, err error) {
	r := newBreader(data)

	version, ok := r.u16()
	if !ok {
		return nil, 0, fmt.Errorf("ipfix: reading version: %w", ErrMalformed)
	}
	if version != 10 {
		return nil, 0, fmt.Errorf("ipfix: version %d: %w", version, ErrUnsupportedVersion)
	}
	msgLength, ok := r.u16()
	if !ok || msgLength < 16 {
		return nil, 0, fmt.Errorf("ipfix: reading message length: %w", ErrMalformed)
	}
	exportTime, ok := r.u32()
	if !ok {
		return nil, 0, fmt.Errorf("ipfix: reading export time: %w", ErrMalformed)
	}
	if !r.skip(4) { // sequence number
		return nil, 0, fmt.Errorf("ipfix: reading sequence number: %w", ErrMalformed)
	}
	domainID, ok := r.u32()
	if !ok {
		return nil, 0, fmt.Errorf("ipfix: reading observation domain id: %w", ErrMalformed)
	}
	at := int64(exportTime)

	for i := 0; r.remaining() >= 4; i++ {
		setID, ok := r.u16()
		if !ok {
			break
		}
		length, ok := r.u16()
		if !ok || length < 4 {
			return records, dropped, fmt.Errorf("ipfix: set %d: reading length: %w", i, ErrMalformed)
		}
		body, ok := r.sub(int(length) - 4)
		if !ok {
			return records, dropped, fmt.Errorf("ipfix: set %d: body shorter than declared length: %w", i, ErrMalformed)
		}

		switch {
		case setID == ipfixTemplateSetID:
			decodeIPFIXTemplates(body, exporterKey, domainID, cache)
		case setID == ipfixOptionsTemplateSetID:
			// Options templates/data: exporter-scoped metadata, not flow
			// records — same treatment as NetFlow v9's options flowset.
		case setID >= ipfixMinDataSetID:
			recs, n := decodeIPFIXData(body, node, at, exporterKey, domainID, setID, cache)
			records = append(records, recs...)
			dropped += n
		default:
			// Reserved set id: unknown, skip.
		}
	}
	return records, dropped, nil
}

func decodeIPFIXTemplates(body *breader, exporterKey string, domainID uint32, cache *TemplateCache) {
	for body.remaining() >= 4 {
		templateID, ok := body.u16()
		if !ok {
			return
		}
		fieldCount, ok := body.u16()
		if !ok {
			return
		}
		fields := make([]templateField, 0, fieldCount)
		truncated := false
		for i := 0; i < int(fieldCount); i++ {
			rawTyp, ok := body.u16()
			if !ok {
				truncated = true
				break
			}
			length, ok := body.u16()
			if !ok {
				truncated = true
				break
			}
			typ := rawTyp
			if rawTyp&ipfixEnterpriseBit != 0 {
				typ = rawTyp &^ ipfixEnterpriseBit
				if !body.skip(4) { // enterprise number
					truncated = true
					break
				}
			}
			if length == 0xFFFF {
				// Variable-length IE: unsupported (see DecodeIPFIX's doc
				// comment) — record it with length 0 so the field count
				// stays aligned with what the exporter declared, but its
				// bytes can never be read correctly, so any data set using
				// this template is effectively undecodable and will be
				// dropped by decodeDataRecordFields running out of a
				// coherent length to consume.
				length = 0
			}
			fields = append(fields, templateField{typ: typ, length: length})
		}
		if truncated {
			return
		}
		cache.set(templateKey{exporter: exporterKey, domain: domainID, id: templateID}, fields)
	}
}

func decodeIPFIXData(body *breader, node string, at int64, exporterKey string, domainID uint32, setID uint16, cache *TemplateCache) (records []Record, dropped int) {
	fields, ok := cache.get(templateKey{exporter: exporterKey, domain: domainID, id: setID})
	if !ok {
		return nil, 1
	}
	recordLen := 0
	for _, f := range fields {
		recordLen += int(f.length)
	}
	if recordLen == 0 {
		return nil, 1
	}
	for body.remaining() >= recordLen {
		rec := Record{At: at, Node: node, Source: SourceIPFIX}
		if !decodeDataRecordFields(body, fields, &rec) {
			dropped++
			break
		}
		records = append(records, rec)
	}
	return records, dropped
}
