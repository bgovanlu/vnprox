// SPDX-License-Identifier: Apache-2.0

package lacphash

import (
	"net"

	"github.com/bgovanlu/vnprox/internal/flow"
)

// FlowTupleFromRecord adapts one internal/flow.Record into a WeightedTuple
// for feeding into Predict. It never populates SrcMAC/DstMAC —
// flow.Record carries none, see doc.go — so a FlowTuple built this way can
// only ever be classified under PolicyLayer34/PolicyEncap34; Predict
// reports every PolicyLayer2/PolicyLayer23/PolicyEncap23 tuple built this
// way as Unclassified with ErrMACRequired, which is the honest, structural
// answer this adapter is documented to produce, not a bug in it.
//
// ok is false when r's SrcIP/DstIP don't parse as an IP address at all —
// internal/flow's own decoders should never produce that, but this
// adapter does not assume its caller only ever passes well-formed records.
func FlowTupleFromRecord(r flow.Record) (WeightedTuple, bool) {
	srcIP := net.ParseIP(r.SrcIP)
	dstIP := net.ParseIP(r.DstIP)
	if srcIP == nil || dstIP == nil {
		return WeightedTuple{}, false
	}
	return WeightedTuple{
		Tuple: FlowTuple{
			SrcIP:   srcIP,
			DstIP:   dstIP,
			SrcPort: clampPort(r.SrcPort),
			DstPort: clampPort(r.DstPort),
			Proto:   clampProto(r.Proto),
		},
		Bytes:   r.Bytes,
		Packets: r.Packets,
	}, true
}

// clampPort narrows a decoded port number to uint16, returning 0 (the
// FlowTuple "no port" value) for anything outside the valid 0-65535 port
// range rather than silently wrapping a malformed/out-of-range value into
// a plausible-looking but wrong port.
func clampPort(v int) uint16 {
	if v < 0 || v > 0xFFFF {
		return 0
	}
	return uint16(v)
}

// clampProto narrows an IP protocol number to uint8 (the valid range is
// 0-255 by definition — RFC 791 §3.1's one-octet Protocol field), returning
// 0 for anything outside it rather than wrapping.
func clampProto(v int) uint8 {
	if v < 0 || v > 0xFF {
		return 0
	}
	return uint8(v)
}
