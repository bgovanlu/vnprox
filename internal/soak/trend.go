// SPDX-License-Identifier: Apache-2.0

package soak

import "math"

// Slope returns the ordinary least-squares slope (dy/dx) of the line fitted
// through the points (xs[i], ys[i]).
//
// ok is false — and slope is 0 — when the fit is undefined: fewer than two
// points, mismatched input lengths, every x identical (a vertical fit), or
// any non-finite input. Callers must treat !ok as "no verdict available",
// never as "flat": silently reporting a zero slope for an undefined fit is
// exactly how a leak gate learns to pass everything.
//
// The two-pass mean-centred form is deliberate. The textbook
// (n*Σxy - Σx*Σy) / (n*Σx² - (Σx)²) form loses most of its precision when
// x is a large offset with small spread — which is precisely the shape of
// this package's input (elapsed minutes late in a multi-hour run, sampled
// seconds apart). Centring first keeps the products small.
func Slope(xs, ys []float64) (slope float64, ok bool) {
	if len(xs) != len(ys) || len(xs) < 2 {
		return 0, false
	}
	var sumX, sumY float64
	for i := range xs {
		if !isFinite(xs[i]) || !isFinite(ys[i]) {
			return 0, false
		}
		sumX += xs[i]
		sumY += ys[i]
	}
	n := float64(len(xs))
	meanX, meanY := sumX/n, sumY/n

	var num, den float64
	for i := range xs {
		dx := xs[i] - meanX
		num += dx * (ys[i] - meanY)
		den += dx * dx
	}
	if den == 0 || !isFinite(num) || !isFinite(den) {
		return 0, false // every x identical: no defined slope
	}
	slope = num / den
	if !isFinite(slope) {
		return 0, false
	}
	return slope, true
}

func isFinite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }
