package flow

import (
	"fmt"
	"net"
)

// DecodeNetFlow5 decodes a raw NetFlow v5 UDP payload (RFC-less but
// long-stable Cisco format: a fixed 24-byte header naming `count` fixed-size
// 48-byte flow records — no templates, unlike v9/IPFIX) into normalized
// Records tagged for node.
//
// Defensive: a malformed header returns (nil, error) immediately; a
// datagram whose declared count exceeds what its actual length can hold
// decodes as many complete 48-byte records as are present and returns the
// already-decoded records alongside a wrapped ErrMalformed for the
// remainder — callers (the listener) count the error, never panic or block
// on it.
func DecodeNetFlow5(data []byte, node string) ([]Record, error) {
	r := newBreader(data)

	version, ok := r.u16()
	if !ok {
		return nil, fmt.Errorf("netflow5: reading version: %w", ErrMalformed)
	}
	if version != 5 {
		return nil, fmt.Errorf("netflow5: version %d: %w", version, ErrUnsupportedVersion)
	}
	count, ok := r.u16()
	if !ok {
		return nil, fmt.Errorf("netflow5: reading record count: %w", ErrMalformed)
	}
	if !r.skip(4) { // sysUptime
		return nil, fmt.Errorf("netflow5: reading sysUptime: %w", ErrMalformed)
	}
	unixSecs, ok := r.u32()
	if !ok {
		return nil, fmt.Errorf("netflow5: reading unixSecs: %w", ErrMalformed)
	}
	// unixNsecs, flowSequence, engineType, engineID, samplingInterval.
	if !r.skip(4 + 4 + 1 + 1 + 2) {
		return nil, fmt.Errorf("netflow5: reading header tail: %w", ErrMalformed)
	}

	records := make([]Record, 0, count)
	for i := 0; i < int(count); i++ {
		rec, ok := decodeNetFlow5Record(r, node, int64(unixSecs))
		if !ok {
			return records, fmt.Errorf("netflow5: record %d/%d: %w", i, count, ErrMalformed)
		}
		records = append(records, rec)
	}
	return records, nil
}

func decodeNetFlow5Record(r *breader, node string, at int64) (Record, bool) {
	srcAddr, ok := r.u32()
	if !ok {
		return Record{}, false
	}
	dstAddr, ok := r.u32()
	if !ok {
		return Record{}, false
	}
	if !r.skip(4) { // nexthop
		return Record{}, false
	}
	input, ok := r.u16()
	if !ok {
		return Record{}, false
	}
	output, ok := r.u16()
	if !ok {
		return Record{}, false
	}
	dPkts, ok := r.u32()
	if !ok {
		return Record{}, false
	}
	dOctets, ok := r.u32()
	if !ok {
		return Record{}, false
	}
	if !r.skip(4 + 4) { // first, last (sysUptime ticks)
		return Record{}, false
	}
	srcPort, ok := r.u16()
	if !ok {
		return Record{}, false
	}
	dstPort, ok := r.u16()
	if !ok {
		return Record{}, false
	}
	if !r.skip(1) { // pad1
		return Record{}, false
	}
	if !r.skip(1) { // tcp_flags
		return Record{}, false
	}
	prot, ok := r.u8()
	if !ok {
		return Record{}, false
	}
	if !r.skip(1) { // tos
		return Record{}, false
	}
	if !r.skip(2 + 2 + 1 + 1 + 2) { // src_as, dst_as, src_mask, dst_mask, pad2
		return Record{}, false
	}

	return Record{
		At:             at,
		Node:           node,
		SrcIP:          ipv4String(srcAddr),
		DstIP:          ipv4String(dstAddr),
		SrcPort:        int(srcPort),
		DstPort:        int(dstPort),
		Proto:          int(prot),
		Bytes:          int64(dOctets),
		Packets:        int64(dPkts),
		IngressIfIndex: int(input),
		EgressIfIndex:  int(output),
		Source:         SourceNetFlow5,
	}, true
}

// ipv4String renders a big-endian-encoded uint32 as a dotted-quad string.
func ipv4String(v uint32) string {
	ip := net.IPv4(byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
	return ip.String()
}
