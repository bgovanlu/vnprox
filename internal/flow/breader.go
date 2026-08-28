// SPDX-License-Identifier: Apache-2.0

package flow

import "encoding/binary"

// breader is a small bounds-checked big-endian byte reader used by every
// decoder in this package (sFlow/NetFlow/IPFIX are all big-endian on the
// wire). Every method reports ok=false instead of panicking once the
// underlying slice is exhausted — the defensive-parsing contract this
// package's doc comment documents: a malformed or truncated datagram is
// skipped and counted, never allowed to panic or block the listener
// goroutine.
type breader struct {
	b   []byte
	off int
}

func newBreader(b []byte) *breader { return &breader{b: b} }

// remaining is how many unread bytes are left.
func (r *breader) remaining() int { return len(r.b) - r.off }

func (r *breader) u8() (byte, bool) {
	if r.remaining() < 1 {
		return 0, false
	}
	v := r.b[r.off]
	r.off++
	return v, true
}

func (r *breader) u16() (uint16, bool) {
	if r.remaining() < 2 {
		return 0, false
	}
	v := binary.BigEndian.Uint16(r.b[r.off:])
	r.off += 2
	return v, true
}

func (r *breader) u32() (uint32, bool) {
	if r.remaining() < 4 {
		return 0, false
	}
	v := binary.BigEndian.Uint32(r.b[r.off:])
	r.off += 4
	return v, true
}

func (r *breader) bytes(n int) ([]byte, bool) {
	if n < 0 || r.remaining() < n {
		return nil, false
	}
	v := r.b[r.off : r.off+n]
	r.off += n
	return v, true
}

func (r *breader) skip(n int) bool {
	if n < 0 || r.remaining() < n {
		return false
	}
	r.off += n
	return true
}

// sub returns a new breader scoped to exactly the next n bytes (consuming
// them from r), for decoders that need to bound-check a nested
// variable-length structure (an sFlow sample, a NetFlow/IPFIX set) without
// letting it read past its own declared length into the next structure.
func (r *breader) sub(n int) (*breader, bool) {
	b, ok := r.bytes(n)
	if !ok {
		return nil, false
	}
	return newBreader(b), true
}
