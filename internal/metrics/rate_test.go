// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"math"
	"testing"
	"time"
)

// TestComputeRates covers AC1: "Fixture counter progression -> correct
// rates (incl. counter-wrap handling, table-tested)".
func TestComputeRates(t *testing.T) {
	tests := []struct {
		name string
		prev Counters
		cur  Counters
		dt   time.Duration
		want Rates
	}{
		{
			name: "normal 5s progression",
			prev: Counters{RxBytes: 1000, TxBytes: 2000, RxPkts: 10, TxPkts: 20, RxErrs: 1, TxErrs: 0, RxDrop: 0, TxDrop: 2},
			cur:  Counters{RxBytes: 6000, TxBytes: 7000, RxPkts: 20, TxPkts: 40, RxErrs: 3, TxErrs: 0, RxDrop: 1, TxDrop: 2},
			dt:   5 * time.Second,
			want: Rates{
				RxBps: (6000 - 1000) * 8 / 5, TxBps: (7000 - 2000) * 8 / 5,
				RxPps: (20 - 10.0) / 5, TxPps: (40 - 20.0) / 5,
				RxErrsPerSec: (3 - 1.0) / 5, TxErrsPerSec: 0,
				RxDropPerSec: (1 - 0.0) / 5, TxDropPerSec: 0,
			},
		},
		{
			name: "zero delta (idle link)",
			prev: Counters{RxBytes: 500, TxBytes: 500},
			cur:  Counters{RxBytes: 500, TxBytes: 500},
			dt:   5 * time.Second,
			want: Rates{},
		},
		{
			name: "non-positive dt yields zero Rates",
			prev: Counters{RxBytes: 100},
			cur:  Counters{RxBytes: 200},
			dt:   0,
			want: Rates{},
		},
		{
			name: "32-bit counter wraparound",
			// A 32-bit counter near its max wraps to a small value after
			// crossing 2^32; the true delta is the distance from prev to
			// max plus cur+1.
			prev: Counters{RxBytes: uint64(math.MaxUint32) - 9}, // 4294967286
			cur:  Counters{RxBytes: 5},
			dt:   5 * time.Second,
			want: Rates{RxBps: float64(15) * 8 / 5}, // (9 to wrap) + 1 + 5 = 15
		},
		{
			name: "64-bit counter wraparound",
			prev: Counters{TxBytes: math.MaxUint64 - 2},
			cur:  Counters{TxBytes: 3},
			dt:   1 * time.Second,
			want: Rates{TxBps: float64(6) * 8 / 1}, // 2 to wrap + 1 + 3 = 6
		},
		{
			name: "packet counter wrap independent of byte counter",
			prev: Counters{RxPkts: uint64(math.MaxUint32) - 1},
			cur:  Counters{RxPkts: 2},
			dt:   2 * time.Second,
			want: Rates{RxPps: float64(4) / 2}, // 1 to wrap + 1 + 2 = 4
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputeRates(tc.prev, tc.cur, tc.dt)
			if got != tc.want {
				t.Errorf("ComputeRates(%+v, %+v, %v) = %+v, want %+v", tc.prev, tc.cur, tc.dt, got, tc.want)
			}
		})
	}
}

func TestDeltaCounter(t *testing.T) {
	tests := []struct {
		name      string
		prev, cur uint64
		wantDelta uint64
	}{
		{"increase", 100, 150, 50},
		{"equal", 100, 100, 0},
		{"32-bit wrap", 4294967290, 5, 11},
		{"64-bit wrap", math.MaxUint64 - 4, 5, 10},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := deltaCounter(tc.prev, tc.cur)
			if got != tc.wantDelta {
				t.Errorf("deltaCounter(%d, %d) = %d, want %d", tc.prev, tc.cur, got, tc.wantDelta)
			}
		})
	}
}

// TestUtilizationPct covers AC1's "correct utilization % against link
// speed".
func TestUtilizationPct(t *testing.T) {
	tests := []struct {
		name      string
		bps       float64
		speedMbps int
		want      float64
	}{
		{"half of 1Gbps link", 500_000_000, 1000, 50},
		{"full 1Gbps link", 1_000_000_000, 1000, 100},
		{"over 100% (bursty/measurement noise) reported as-is", 1_500_000_000, 1000, 150},
		{"10Mbps link at 1Mbps", 1_000_000, 10, 10},
		{"unknown speed (0) reports 0, not divide-by-zero/NaN", 1_000_000, 0, 0},
		{"negative speed reports 0", 1_000_000, -1, 0},
		{"zero bps reports 0", 0, 1000, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := UtilizationPct(tc.bps, tc.speedMbps)
			if got != tc.want {
				t.Errorf("UtilizationPct(%v, %d) = %v, want %v", tc.bps, tc.speedMbps, got, tc.want)
			}
		})
	}
}
