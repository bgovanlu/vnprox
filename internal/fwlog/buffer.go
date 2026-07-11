package fwlog

import "sync"

// StreamEntry is one buffered, correlated log line — the unit both the
// REST tail read (internal/api's GET /firewall/log) and the WS follow
// push (Service.Tick's broadcast) hand to callers. Seq is a
// buffer-assigned, strictly increasing sequence number (stable render/dedup
// key for the frontend regardless of any two lines sharing a timestamp).
type StreamEntry struct {
	Correlation Correlation
	Entry       Entry
	Seq         int64
}

// RingBuffer is a fixed-capacity, append-only ring of StreamEntry plus a
// running count of how many were evicted to stay within capacity. Safe for
// concurrent use. This is the "bounded buffer" T-505's card requires for
// the per-node log reader's tail/follow state — it holds the merged,
// cluster-wide, already-correlated stream Service.Tick fills, not raw
// per-node lines.
type RingBuffer struct {
	items   []StreamEntry
	nextSeq int64
	evicted int64
	cap     int
	mu      sync.Mutex
}

// NewRingBuffer builds a RingBuffer holding at most capacity entries.
// capacity <= 0 is treated as 1 (a buffer that holds nothing would make
// "tail" meaningless).
func NewRingBuffer(capacity int) *RingBuffer {
	if capacity <= 0 {
		capacity = 1
	}
	return &RingBuffer{cap: capacity, items: make([]StreamEntry, 0, capacity)}
}

// Push appends one entry, assigning it the next sequence number, and
// evicts the oldest entry (incrementing the eviction counter) if the
// buffer was already at capacity. This eviction is ordinary history
// rotation (the buffer only ever keeps the most recent N lines) — it is
// deliberately not what Service surfaces as the storm "dropped" indicator
// (see Service.Tick's doc comment for that distinction).
func (b *RingBuffer) Push(e Entry, c Correlation) StreamEntry {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.nextSeq++
	se := StreamEntry{Entry: e, Correlation: c, Seq: b.nextSeq}

	if len(b.items) >= b.cap {
		copy(b.items, b.items[1:])
		b.items = b.items[:len(b.items)-1]
		b.evicted++
	}
	b.items = append(b.items, se)
	return se
}

// Snapshot returns a copy of every currently buffered entry (oldest
// first) plus the cumulative eviction count.
func (b *RingBuffer) Snapshot() ([]StreamEntry, int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]StreamEntry, len(b.items))
	copy(out, b.items)
	return out, b.evicted
}

// Len reports the number of entries currently buffered.
func (b *RingBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.items)
}
