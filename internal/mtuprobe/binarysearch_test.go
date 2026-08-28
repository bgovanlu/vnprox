// SPDX-License-Identifier: Apache-2.0

package mtuprobe_test

import (
	"errors"
	"math"
	"testing"

	"github.com/bgovanlu/vnprox/internal/mtuprobe"
)

// scriptedProbe returns a mtuprobe.ProbeFunc mocking DF-set probe replies at
// varying sizes for a fixture path with a scripted "true" MTU: ok for every
// size <= trueMTU, dropped (ok=false) above it — the exact DF-probe
// fragmentation-needed behavior a real path exhibits. Also counts calls so
// the test can assert the exact probe count against the documented bound.
func scriptedProbe(trueMTU int, calls *int) mtuprobe.ProbeFunc {
	return func(size int) (bool, error) {
		*calls++
		return size <= trueMTU, nil
	}
}

// TestBinarySearchMTU_ConvergesToExactValue is AC1: against a fixture path
// with a scripted true MTU (mock responses at varying DF-probe sizes),
// binary search converges to the exact expected value within a bounded
// probe count (ceil(log2(hi-lo+1))+1 for the default 552..9216 range).
func TestBinarySearchMTU_ConvergesToExactValue(t *testing.T) {
	cases := []struct {
		name    string
		trueMTU int
	}{
		{"jumbo-9000", 9000},
		{"vxlan-underlay-1450", 1450},
		{"standard-1500", 1500},
		{"evpn-9166", 9166},
		{"pmtud-degraded-1400", 1400},
		{"exact-floor-552", 552},
		{"exact-ceiling-9216", 9216},
		{"odd-value-1387", 1387},
	}

	maxBoundedProbes := int(math.Ceil(math.Log2(float64(mtuprobe.MaxMTU-mtuprobe.MinMTU+1)))) + 1

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls int
			probe := scriptedProbe(tc.trueMTU, &calls)

			mtu, probeCount, err := mtuprobe.BinarySearchMTU(probe, 0, 0) // defaults to Min/MaxMTU
			if err != nil {
				t.Fatalf("BinarySearchMTU: unexpected error: %v", err)
			}
			if mtu != tc.trueMTU {
				t.Fatalf("BinarySearchMTU converged to %d, want exact %d", mtu, tc.trueMTU)
			}
			if probeCount != calls {
				t.Fatalf("reported probeCount %d != actual probe() calls %d", probeCount, calls)
			}
			if probeCount > maxBoundedProbes {
				t.Fatalf("probeCount %d exceeds the documented bound %d", probeCount, maxBoundedProbes)
			}
		})
	}
}

// TestBinarySearchMTU_FloorFails: a path that can't even carry MinMTU
// reports mtu=0, not an error and not a misleadingly-cheerful default.
func TestBinarySearchMTU_FloorFails(t *testing.T) {
	var calls int
	probe := scriptedProbe(0, &calls) // nothing ever gets through

	mtu, probeCount, err := mtuprobe.BinarySearchMTU(probe, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mtu != 0 {
		t.Fatalf("mtu = %d, want 0 when even the floor fails", mtu)
	}
	if probeCount != 1 {
		t.Fatalf("probeCount = %d, want exactly 1 (only the floor probe)", probeCount)
	}
}

// TestBinarySearchMTU_CustomRange exercises a narrower, caller-supplied
// [lo, hi] rather than the package defaults.
func TestBinarySearchMTU_CustomRange(t *testing.T) {
	var calls int
	probe := scriptedProbe(1400, &calls)

	mtu, _, err := mtuprobe.BinarySearchMTU(probe, 1000, 1500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mtu != 1400 {
		t.Fatalf("mtu = %d, want 1400", mtu)
	}
}

// TestBinarySearchMTU_ProbeError: a probe that could not even be attempted
// (transport-level failure, not a fragmentation-needed reply) propagates as
// a non-nil error rather than being silently reported as a low MTU.
func TestBinarySearchMTU_ProbeError(t *testing.T) {
	wantErr := errors.New("ping binary not found")
	probe := func(int) (bool, error) { return false, wantErr }

	_, _, err := mtuprobe.BinarySearchMTU(probe, 0, 0)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want wrapping %v", err, wantErr)
	}
}

// TestBinarySearchMTU_InvalidRange: lo > hi is a caller error, reported as
// such rather than silently misbehaving.
func TestBinarySearchMTU_InvalidRange(t *testing.T) {
	_, _, err := mtuprobe.BinarySearchMTU(func(int) (bool, error) { return true, nil }, 2000, 1000)
	if err == nil {
		t.Fatal("want an error for lo > hi, got nil")
	}
}
