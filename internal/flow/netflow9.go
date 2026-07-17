package flow

import "fmt"

// NetFlow v9 FlowSet id conventions (Cisco's original v9 spec).
const (
	netflow9TemplateFlowSetID = 0
	netflow9OptionsFlowSetID  = 1
	netflow9MinDataFlowSetID  = 256
)

// DecodeNetFlow9 decodes one NetFlow v9 UDP payload into normalized
// Records, using/populating cache for template (flowSetID 0) and data
// (flowSetID >= 256) FlowSets. exporterKey should uniquely identify the
// sending exporter (its UDP source address is listener.go's choice) since
// template numbering is only unique per exporter.
//
// dropped counts data records this call could not decode because no
// template was cached yet for their (exporterKey, sourceID, flowSetID) —
// per this task's card, "a data datagram fed with no prior template is
// dropped (counted, not errored)": real NetFlow v9 collectors must tolerate
// this exact ordering dependency, since UDP delivery order and loss are
// both possible and a template can arrive after, or never relative to, any
// given data set.
func DecodeNetFlow9(data []byte, node, exporterKey string, cache *TemplateCache) (records []Record, dropped int, err error) {
	r := newBreader(data)

	version, ok := r.u16()
	if !ok {
		return nil, 0, fmt.Errorf("netflow9: reading version: %w", ErrMalformed)
	}
	if version != 9 {
		return nil, 0, fmt.Errorf("netflow9: version %d: %w", version, ErrUnsupportedVersion)
	}
	flowSetCount, ok := r.u16()
	if !ok {
		return nil, 0, fmt.Errorf("netflow9: reading flowset count: %w", ErrMalformed)
	}
	if !r.skip(4) { // sysUptime
		return nil, 0, fmt.Errorf("netflow9: reading sysUptime: %w", ErrMalformed)
	}
	unixSecs, ok := r.u32()
	if !ok {
		return nil, 0, fmt.Errorf("netflow9: reading unixSecs: %w", ErrMalformed)
	}
	if !r.skip(4) { // package sequence
		return nil, 0, fmt.Errorf("netflow9: reading sequence: %w", ErrMalformed)
	}
	sourceID, ok := r.u32()
	if !ok {
		return nil, 0, fmt.Errorf("netflow9: reading source id: %w", ErrMalformed)
	}

	for i := 0; i < int(flowSetCount); i++ {
		setID, ok := r.u16()
		if !ok {
			return records, dropped, fmt.Errorf("netflow9: flowset %d: reading id: %w", i, ErrMalformed)
		}
		length, ok := r.u16()
		if !ok || length < 4 {
			return records, dropped, fmt.Errorf("netflow9: flowset %d: reading length: %w", i, ErrMalformed)
		}
		body, ok := r.sub(int(length) - 4)
		if !ok {
			return records, dropped, fmt.Errorf("netflow9: flowset %d: body shorter than declared length: %w", i, ErrMalformed)
		}

		switch {
		case setID == netflow9TemplateFlowSetID:
			decodeNetFlow9Templates(body, exporterKey, sourceID, cache)
		case setID == netflow9OptionsFlowSetID:
			// Options templates/data describe exporter-scoped metadata
			// (e.g. sampling interval), not flow records — flow.Record has
			// no slot for them; skipped without penalty.
		case setID >= netflow9MinDataFlowSetID:
			recs, n := decodeNetFlow9Data(body, node, int64(unixSecs), exporterKey, sourceID, setID, cache)
			records = append(records, recs...)
			dropped += n
		default:
			// Reserved flowset id (2-255): unknown, skip.
		}
	}
	return records, dropped, nil
}

func decodeNetFlow9Templates(body *breader, exporterKey string, sourceID uint32, cache *TemplateCache) {
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
			typ, ok := body.u16()
			if !ok {
				truncated = true
				break
			}
			length, ok := body.u16()
			if !ok {
				truncated = true
				break
			}
			fields = append(fields, templateField{typ: typ, length: length})
		}
		if truncated {
			return // rest of this flowset is untrustworthy; stop
		}
		cache.set(templateKey{exporter: exporterKey, domain: sourceID, id: templateID}, fields)
	}
}

func decodeNetFlow9Data(body *breader, node string, at int64, exporterKey string, sourceID uint32, setID uint16, cache *TemplateCache) (records []Record, dropped int) {
	fields, ok := cache.get(templateKey{exporter: exporterKey, domain: sourceID, id: setID})
	if !ok {
		return nil, 1 // whole data set dropped: no template to decode it with
	}
	recordLen := 0
	for _, f := range fields {
		recordLen += int(f.length)
	}
	if recordLen == 0 {
		return nil, 1
	}
	for body.remaining() >= recordLen {
		rec := Record{At: at, Node: node, Source: SourceNetFlow9}
		if !decodeDataRecordFields(body, fields, &rec) {
			dropped++
			break
		}
		records = append(records, rec)
	}
	// Any bytes remaining shorter than one record are padding to the
	// flowset's 4-byte boundary (real exporters do this) — not an error.
	return records, dropped
}
