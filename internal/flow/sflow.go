package flow

import (
	"fmt"
	"net"
)

// sFlow v5 sample format numbers (enterprise 0, the standard/non-vendor
// structures — sflow.org's sFlow version 5 spec).
const (
	sflowFormatFlowSample         = 1
	sflowFormatCountersSample     = 2
	sflowFormatFlowSampleExpanded = 3
	sflowFormatRawPacketHeader    = 1 // flow_record sub-format, distinct namespace from the sample-level format above
)

// DecodeSFlow decodes one sFlow v5 UDP payload into normalized Records. Only
// flow samples (format 1) and expanded flow samples (format 3) carrying a
// raw packet header flow record (the standard, enterprise-0
// sampled_header) are turned into Records; counters samples (format 2) and
// any enterprise-specific or otherwise-unrecognized sample/flow-record
// format are silently skipped (not an error — a real collector must
// tolerate a datagram mixing sample types it does and doesn't understand).
//
// now is the listener's receive time: sFlow carries no per-sample
// wall-clock timestamp of its own (only a device-uptime counter, not
// correlatable to wall-clock time without out-of-band NTP/boot-time
// knowledge this decoder does not have), so every Record's At is the
// datagram's receive time — see this function's own doc note above.
//
// dropped counts samples/flow-records whose own declared length fields were
// internally consistent (so the surrounding datagram structure stays
// parseable) but whose payload failed to decode as a raw packet header
// (e.g. a non-Ethernet/non-IP frame, or a truncated inner header) — per
// this package's defensive-parsing contract, these are skipped and
// counted, never treated as a fatal decode error for the whole datagram.
//
// IPv6 support is header-only (Ethernet + IPv6 fixed header + best-effort
// TCP/UDP ports): extension header chains are not walked, so a Record's
// Proto reports IPv6's own NextHeader value even when it names an extension
// header rather than the true upper-layer protocol — flagged in
// planning/reports/needs-hardware-validation.md as a gap worth revisiting
// against real IPv6 traffic.
func DecodeSFlow(data []byte, node string, now int64) (records []Record, dropped int, err error) {
	r := newBreader(data)

	version, ok := r.u32()
	if !ok {
		return nil, 0, fmt.Errorf("sflow: reading version: %w", ErrMalformed)
	}
	if version != 5 {
		return nil, 0, fmt.Errorf("sflow: version %d: %w", version, ErrUnsupportedVersion)
	}
	addrType, ok := r.u32()
	if !ok {
		return nil, 0, fmt.Errorf("sflow: reading agent address type: %w", ErrMalformed)
	}
	var addrLen int
	switch addrType {
	case 1:
		addrLen = 4
	case 2:
		addrLen = 16
	default:
		return nil, 0, fmt.Errorf("sflow: unknown agent address type %d: %w", addrType, ErrMalformed)
	}
	if !r.skip(addrLen) {
		return nil, 0, fmt.Errorf("sflow: reading agent address: %w", ErrMalformed)
	}
	if !r.skip(4) { // sub_agent_id
		return nil, 0, fmt.Errorf("sflow: reading sub agent id: %w", ErrMalformed)
	}
	if !r.skip(4) { // sequence_number
		return nil, 0, fmt.Errorf("sflow: reading sequence number: %w", ErrMalformed)
	}
	if !r.skip(4) { // uptime
		return nil, 0, fmt.Errorf("sflow: reading uptime: %w", ErrMalformed)
	}
	numSamples, ok := r.u32()
	if !ok {
		return nil, 0, fmt.Errorf("sflow: reading sample count: %w", ErrMalformed)
	}

	for i := 0; i < int(numSamples); i++ {
		sampleType, ok := r.u32()
		if !ok {
			return records, dropped, fmt.Errorf("sflow: sample %d: reading type: %w", i, ErrMalformed)
		}
		sampleLength, ok := r.u32()
		if !ok {
			return records, dropped, fmt.Errorf("sflow: sample %d: reading length: %w", i, ErrMalformed)
		}
		sub, ok := r.sub(int(sampleLength))
		if !ok {
			return records, dropped, fmt.Errorf("sflow: sample %d: body shorter than declared length: %w", i, ErrMalformed)
		}

		enterprise := sampleType >> 12
		format := sampleType & 0xFFF
		if enterprise != 0 {
			continue // enterprise-specific sample structure: not understood, skip
		}

		var (
			recs  []Record
			decOK bool
		)
		switch format {
		case sflowFormatFlowSample:
			recs, decOK = decodeSFlowFlowSample(sub, node, now, false)
		case sflowFormatFlowSampleExpanded:
			recs, decOK = decodeSFlowFlowSample(sub, node, now, true)
		default:
			continue // counters sample (or unknown format): not a flow, skip
		}
		if !decOK {
			dropped++
			continue
		}
		records = append(records, recs...)
	}
	return records, dropped, nil
}

// decodeSFlowFlowSample decodes one flow_sample (expanded=false) or
// flow_sample_expanded (expanded=true) structure, already scoped to exactly
// its declared byte length by the caller.
func decodeSFlowFlowSample(sub *breader, node string, now int64, expanded bool) ([]Record, bool) {
	if !sub.skip(4) { // sequence_number
		return nil, false
	}
	if expanded {
		if !sub.skip(8) { // sflow_data_source_expanded{type,index}
			return nil, false
		}
	} else {
		if !sub.skip(4) { // sflow_data_source (combined type+index)
			return nil, false
		}
	}
	if !sub.skip(4) { // sampling_rate
		return nil, false
	}
	if !sub.skip(4) { // sample_pool
		return nil, false
	}
	if !sub.skip(4) { // drops
		return nil, false
	}

	var input, output uint32
	var ok bool
	if expanded {
		if !sub.skip(4) { // input interface_expanded.type
			return nil, false
		}
		if input, ok = sub.u32(); !ok { // input interface_expanded.index
			return nil, false
		}
		if !sub.skip(4) { // output interface_expanded.type
			return nil, false
		}
		if output, ok = sub.u32(); !ok { // output interface_expanded.index
			return nil, false
		}
	} else {
		if input, ok = sub.u32(); !ok {
			return nil, false
		}
		if output, ok = sub.u32(); !ok {
			return nil, false
		}
	}

	count, ok := sub.u32()
	if !ok {
		return nil, false
	}

	var records []Record
	for i := 0; i < int(count); i++ {
		rec, present, ok := decodeSFlowFlowRecord(sub, node, now, input, output)
		if !ok {
			return records, false
		}
		if present {
			records = append(records, rec)
		}
	}
	return records, true
}

// decodeSFlowFlowRecord decodes one flow_record's {flow_format,
// flow_data_length, flow_data} envelope, already positioned within sub.
// ok is false only when the envelope itself (the two length-bearing header
// fields, or the declared flow_data_length exceeding what remains) can't be
// read — a real structural truncation that makes every later record in this
// sample untrustworthy too. present is false when the envelope decoded fine
// but its contents are not a raw packet header (counters, enterprise-
// specific, or a malformed inner header) — those bytes are still correctly
// consumed (via sub's own r.sub bookkeeping), so the caller can keep reading
// subsequent records in the same sample.
func decodeSFlowFlowRecord(sub *breader, node string, now int64, input, output uint32) (rec Record, present, ok bool) {
	flowFormat, readOK := sub.u32()
	if !readOK {
		return Record{}, false, false
	}
	dataLength, readOK := sub.u32()
	if !readOK {
		return Record{}, false, false
	}
	data, readOK := sub.sub(int(dataLength))
	if !readOK {
		return Record{}, false, false
	}

	enterprise := flowFormat >> 12
	format := flowFormat & 0xFFF
	if enterprise != 0 || format != sflowFormatRawPacketHeader {
		return Record{}, false, true // not a raw packet header: consumed, skipped
	}

	r, decOK := decodeSFlowRawPacketHeader(data, node, now, input, output)
	if !decOK {
		return Record{}, false, true // malformed inner header: consumed, skipped
	}
	return r, true, true
}

// decodeSFlowRawPacketHeader decodes a sampled_header structure (the
// standard raw-packet flow_record payload): protocol/frame_length/stripped/
// header_length followed by exactly header_length raw bytes — the captured
// Ethernet frame prefix this function parses with parseEthernetHeader.
//
// frame_length (T-3706, fixed — found by decoding a real, wire-crafted sFlow
// v5 datagram against a live listener on the disposable nested lab and
// noticing every resulting flow_samples row showed bytes=0/packets=0: this
// field used to be skip()ped entirely, so it was silently discarded no
// matter what the exporter reported, and the package's own hand-built
// decode_test.go golden fixture never questioned it because its `want`
// Record simply never set Bytes/Packets either — a self-authored fixture
// checking a decoder against its own omission, the exact failure mode
// CLAUDE.md's SDN-zone-status lesson warns about). frame_length is the
// exporter's own reported length of the ONE sampled packet this record
// represents, so Bytes = frame_length, Packets = 1 is the literal, honest
// reading — deliberately NOT sampling_rate*frame_length: sFlow's own spec
// leaves "populate the sampling rate" to the exporter's discretion, and
// this package does not carry a per-record sampling-rate field for a caller
// to later extrapolate from, so inventing an extrapolation policy here
// would silently misrepresent an exact field as an estimate. See
// docs/api.md's Flows section for how this differs from NetFlow/IPFIX's
// exporter-cumulative Bytes/Packets.
func decodeSFlowRawPacketHeader(data *breader, node string, now int64, input, output uint32) (Record, bool) {
	protocol, ok := data.u32()
	if !ok {
		return Record{}, false
	}
	frameLength, ok := data.u32()
	if !ok {
		return Record{}, false
	}
	if !data.skip(4) { // stripped
		return Record{}, false
	}
	headerLength, ok := data.u32()
	if !ok {
		return Record{}, false
	}
	headerBytes, ok := data.bytes(int(headerLength))
	if !ok {
		return Record{}, false
	}
	const headerProtocolEthernet = 1
	if protocol != headerProtocolEthernet {
		return Record{}, false // non-Ethernet captured header: not supported
	}

	rec := Record{
		At: now, Node: node, Source: SourceSFlow,
		IngressIfIndex: int(input), EgressIfIndex: int(output),
		Bytes: int64(frameLength), Packets: 1,
	}
	if !parseEthernetHeader(headerBytes, &rec) {
		return Record{}, false
	}
	return rec, true
}

// parseEthernetHeader parses a raw captured Ethernet frame prefix (dst mac,
// src mac, ethertype, optionally one or more stacked 802.1Q VLAN tags, then
// an IPv4 or IPv6 header) into rec. Non-IP ethertypes (ARP, etc.) report
// ok=false — there is nothing this package normalizes for them.
func parseEthernetHeader(b []byte, rec *Record) bool {
	r := newBreader(b)
	if !r.skip(6) { // destination MAC
		return false
	}
	if !r.skip(6) { // source MAC
		return false
	}
	ethertype, ok := r.u16()
	if !ok {
		return false
	}
	const vlanTPID = 0x8100
	for ethertype == vlanTPID {
		tci, ok := r.u16()
		if !ok {
			return false
		}
		rec.VLAN = int(tci & 0x0FFF)
		ethertype, ok = r.u16()
		if !ok {
			return false
		}
	}
	switch ethertype {
	case 0x0800: // IPv4
		return parseIPv4Header(r, rec)
	case 0x86DD: // IPv6
		return parseIPv6Header(r, rec)
	default:
		return false
	}
}

func parseIPv4Header(r *breader, rec *Record) bool {
	verIHL, ok := r.u8()
	if !ok {
		return false
	}
	ihl := int(verIHL&0x0F) * 4
	if ihl < 20 {
		return false
	}
	if !r.skip(1) { // tos
		return false
	}
	if !r.skip(2) { // total length
		return false
	}
	if !r.skip(2) { // identification
		return false
	}
	if !r.skip(2) { // flags + fragment offset
		return false
	}
	if !r.skip(1) { // ttl
		return false
	}
	proto, ok := r.u8()
	if !ok {
		return false
	}
	if !r.skip(2) { // header checksum
		return false
	}
	srcBytes, ok := r.bytes(4)
	if !ok {
		return false
	}
	dstBytes, ok := r.bytes(4)
	if !ok {
		return false
	}
	if ihl > 20 && !r.skip(ihl-20) { // IP options
		return false
	}

	rec.SrcIP = net.IP(srcBytes).String()
	rec.DstIP = net.IP(dstBytes).String()
	rec.Proto = int(proto)
	parseL4Ports(r, proto, rec) // best effort: leaves ports at 0 on failure
	return true
}

// parseIPv6Header parses IPv6's fixed 40-byte header only — extension
// header chains are not walked (see DecodeSFlow's doc comment for the
// documented gap this leaves).
func parseIPv6Header(r *breader, rec *Record) bool {
	if !r.skip(4) { // version(4) + traffic class(8) + flow label(20 bits)
		return false
	}
	if !r.skip(2) { // payload length
		return false
	}
	nextHeader, ok := r.u8()
	if !ok {
		return false
	}
	if !r.skip(1) { // hop limit
		return false
	}
	srcBytes, ok := r.bytes(16)
	if !ok {
		return false
	}
	dstBytes, ok := r.bytes(16)
	if !ok {
		return false
	}

	rec.SrcIP = net.IP(srcBytes).String()
	rec.DstIP = net.IP(dstBytes).String()
	rec.Proto = int(nextHeader)
	parseL4Ports(r, nextHeader, rec)
	return true
}

// parseL4Ports reads TCP/UDP's shared leading {src port, dst port} layout
// (both protocols put a 16-bit big-endian port at the same two offsets)
// into rec. Any other protocol (ICMP, etc.) — or a truncated read — simply
// leaves rec's ports at their zero value; this is always best-effort, never
// a decode failure for the surrounding packet.
func parseL4Ports(r *breader, proto byte, rec *Record) {
	const (
		protoTCP = 6
		protoUDP = 17
	)
	if proto != protoTCP && proto != protoUDP {
		return
	}
	srcPort, ok := r.u16()
	if !ok {
		return
	}
	dstPort, ok := r.u16()
	if !ok {
		return
	}
	rec.SrcPort = int(srcPort)
	rec.DstPort = int(dstPort)
}
