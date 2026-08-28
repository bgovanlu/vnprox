// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// TestSampler_AllCounters covers T-1001's exporter seam: every ref ingested
// at least once appears with its exact raw (not rated) most-recent
// counters, sorted by ref string.
func TestSampler_AllCounters(t *testing.T) {
	links := []host.LinkState{
		{Kind: "physical", Name: "eno1", SpeedMbps: 1000, LinkUp: true},
		{Kind: "physical", Name: "eno2", SpeedMbps: 1000, LinkUp: true},
	}

	sampler := New(Config{Logger: testLogger()})
	ctx := context.Background()
	base := time.Unix(1_700_000_000, 0)

	// A single Ingest (no second sample) is enough for AllCounters, unlike
	// Live which needs two samples to compute a rate.
	sampler.Ingest(ctx, "pve1", base, links, map[string]host.IfaceStats{
		"eno1": {RxBytes: 100, TxBytes: 200, RxPackets: 3, TxPackets: 4, RxErrors: 5, TxErrors: 6, RxDropped: 7, TxDropped: 8},
		"eno2": {RxBytes: 900, TxBytes: 800},
	})

	snaps := sampler.AllCounters()
	if len(snaps) != 2 {
		t.Fatalf("AllCounters() returned %d snapshots, want 2", len(snaps))
	}
	// Sorted by ref string: "physnic:pve1:eno1" < "physnic:pve1:eno2".
	if snaps[0].Ref != (inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno1"}) {
		t.Errorf("snaps[0].Ref = %+v, want eno1's ref first (sorted order)", snaps[0].Ref)
	}
	want := Counters{RxBytes: 100, TxBytes: 200, RxPkts: 3, TxPkts: 4, RxErrs: 5, TxErrs: 6, RxDrop: 7, TxDrop: 8}
	if snaps[0].Counters != want {
		t.Errorf("snaps[0].Counters = %+v, want %+v", snaps[0].Counters, want)
	}
	if snaps[0].At != base.Unix() {
		t.Errorf("snaps[0].At = %d, want %d", snaps[0].At, base.Unix())
	}
	if snaps[1].Ref.ID != "eno2" {
		t.Errorf("snaps[1].Ref.ID = %q, want eno2", snaps[1].Ref.ID)
	}

	// A second Ingest updates the snapshot to the latest counters (still
	// raw, not a computed rate).
	sampler.Ingest(ctx, "pve1", base.Add(5*time.Second), links, map[string]host.IfaceStats{
		"eno1": {RxBytes: 1100, TxBytes: 1200},
		"eno2": {RxBytes: 900, TxBytes: 800},
	})
	snaps = sampler.AllCounters()
	byID := map[string]CounterSnapshot{}
	for _, s := range snaps {
		byID[s.Ref.ID] = s
	}
	if byID["eno1"].Counters.RxBytes != 1100 {
		t.Errorf("eno1 RxBytes after second ingest = %d, want 1100 (raw, not a rate)", byID["eno1"].Counters.RxBytes)
	}
}

// TestSampler_AllCounters_EmptyWhenNeverIngested covers the zero-state: no
// Ingest call yet means an empty (not nil-panicking) slice.
func TestSampler_AllCounters_EmptyWhenNeverIngested(t *testing.T) {
	sampler := New(Config{Logger: testLogger()})
	snaps := sampler.AllCounters()
	if len(snaps) != 0 {
		t.Errorf("AllCounters() on a fresh sampler = %v, want empty", snaps)
	}
}
