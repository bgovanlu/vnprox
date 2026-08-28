// SPDX-License-Identifier: Apache-2.0

package fwlog

import "testing"

func TestRingBuffer_PushAndSnapshot(t *testing.T) {
	b := NewRingBuffer(3)
	for i := 0; i < 3; i++ {
		b.Push(Entry{Raw: "line"}, Correlation{Status: StatusUnmatched})
	}
	items, evicted := b.Snapshot()
	if len(items) != 3 {
		t.Fatalf("len(items) = %d, want 3", len(items))
	}
	if evicted != 0 {
		t.Fatalf("evicted = %d, want 0 (buffer not yet over capacity)", evicted)
	}
	for i, it := range items {
		if it.Seq != int64(i+1) {
			t.Errorf("items[%d].Seq = %d, want %d (strictly increasing from 1)", i, it.Seq, i+1)
		}
	}
}

func TestRingBuffer_EvictsOldestBeyondCapacity(t *testing.T) {
	b := NewRingBuffer(2)
	b.Push(Entry{Raw: "a"}, Correlation{})
	b.Push(Entry{Raw: "b"}, Correlation{})
	b.Push(Entry{Raw: "c"}, Correlation{})

	items, evicted := b.Snapshot()
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2 (capacity)", len(items))
	}
	if evicted != 1 {
		t.Fatalf("evicted = %d, want 1", evicted)
	}
	if items[0].Entry.Raw != "b" || items[1].Entry.Raw != "c" {
		t.Fatalf("items = %+v, want [b, c] (oldest evicted)", items)
	}
}

func TestRingBuffer_ZeroCapacityTreatedAsOne(t *testing.T) {
	b := NewRingBuffer(0)
	b.Push(Entry{Raw: "a"}, Correlation{})
	b.Push(Entry{Raw: "b"}, Correlation{})
	items, evicted := b.Snapshot()
	if len(items) != 1 || items[0].Entry.Raw != "b" {
		t.Fatalf("items = %+v, want just [b]", items)
	}
	if evicted != 1 {
		t.Fatalf("evicted = %d, want 1", evicted)
	}
}

func TestRingBuffer_Len(t *testing.T) {
	b := NewRingBuffer(10)
	if b.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", b.Len())
	}
	b.Push(Entry{}, Correlation{})
	if b.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", b.Len())
	}
}
