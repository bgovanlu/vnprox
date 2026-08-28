// SPDX-License-Identifier: Apache-2.0

package soak

import (
	"math"
	"testing"
)

func TestSlope(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		xs    []float64
		ys    []float64
		want  float64
		wantO bool
	}{
		{
			name: "perfectly flat at a high value passes through as zero slope",
			// The AC3 shape in miniature: a big constant is not a trend.
			xs: []float64{0, 1, 2, 3, 4}, ys: []float64{5000, 5000, 5000, 5000, 5000},
			want: 0, wantO: true,
		},
		{
			name: "exact positive slope",
			xs:   []float64{0, 1, 2, 3}, ys: []float64{10, 13, 16, 19},
			want: 3, wantO: true,
		},
		{
			name: "exact negative slope",
			xs:   []float64{0, 1, 2, 3}, ys: []float64{100, 90, 80, 70},
			want: -10, wantO: true,
		},
		{
			name: "noisy but rising: fit recovers the underlying rate",
			xs:   []float64{0, 1, 2, 3, 4, 5}, ys: []float64{10, 13, 15, 17, 21, 22},
			want: 2.4571428571, wantO: true,
		},
		{
			name: "sawtooth ending high fits a positive slope — why Heap forces a GC",
			// A pure GC sawtooth around a constant live set still produces a
			// non-zero fit if the window happens to end on a peak. This is
			// the documented reason Heap() collects before reading
			// HeapAlloc rather than gating on the raw allocator sawtooth.
			xs: []float64{0, 1, 2, 3, 4, 5}, ys: []float64{100, 130, 100, 130, 100, 130},
			want: 2.5714285714, wantO: true,
		},
		{
			name: "large x offset with small spread keeps precision",
			// 8 hours in, sampled 1/60 min (one second) apart: the naive
			// sum-of-squares form loses most of its significant digits here.
			xs: []float64{480, 480 + 1.0/60, 480 + 2.0/60, 480 + 3.0/60}, ys: []float64{1, 2, 3, 4},
			want: 60, wantO: true,
		},
		{
			name: "single point has no slope",
			xs:   []float64{1}, ys: []float64{1},
			want: 0, wantO: false,
		},
		{
			name: "empty has no slope",
			xs:   nil, ys: nil,
			want: 0, wantO: false,
		},
		{
			name: "mismatched lengths have no slope",
			xs:   []float64{0, 1, 2}, ys: []float64{1, 2},
			want: 0, wantO: false,
		},
		{
			name: "every x identical has no slope (never reported as flat)",
			xs:   []float64{2, 2, 2}, ys: []float64{1, 5, 9},
			want: 0, wantO: false,
		},
		{
			name: "NaN input has no slope",
			xs:   []float64{0, 1, 2}, ys: []float64{1, math.NaN(), 3},
			want: 0, wantO: false,
		},
		{
			name: "Inf input has no slope",
			xs:   []float64{0, 1, 2}, ys: []float64{1, math.Inf(1), 3},
			want: 0, wantO: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := Slope(tc.xs, tc.ys)
			if ok != tc.wantO {
				t.Fatalf("Slope ok = %v, want %v", ok, tc.wantO)
			}
			if !ok {
				return
			}
			if math.Abs(got-tc.want) > 1e-6 {
				t.Fatalf("Slope = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestSlopeMatchesClosedForm cross-checks the mean-centred implementation
// against the textbook closed form on well-conditioned input, so a future
// "simplification" of trend.go that quietly changes the arithmetic is
// caught rather than absorbed.
func TestSlopeMatchesClosedForm(t *testing.T) {
	t.Parallel()
	xs := []float64{0, 0.5, 1, 1.5, 2, 2.5, 3}
	ys := []float64{42, 44, 43, 47, 46, 50, 49}

	var sx, sy, sxy, sxx float64
	n := float64(len(xs))
	for i := range xs {
		sx += xs[i]
		sy += ys[i]
		sxy += xs[i] * ys[i]
		sxx += xs[i] * xs[i]
	}
	want := (n*sxy - sx*sy) / (n*sxx - sx*sx)

	got, ok := Slope(xs, ys)
	if !ok {
		t.Fatal("Slope reported no fit for well-conditioned input")
	}
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("Slope = %v, closed form = %v", got, want)
	}
}
