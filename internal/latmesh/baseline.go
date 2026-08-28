// SPDX-License-Identifier: Apache-2.0

package latmesh

import (
	"math"
	"sort"
)

// percentile returns the p-th percentile (0-100) of sorted (must already be
// ascending), using the nearest-rank method: idx = ceil(p/100 * n), 1-
// indexed, clamped to [1,n]. Chosen over a linear-interpolation method
// because it is trivially hand-verifiable in a golden test (every returned
// value is a real observed sample, never an interpolated one between two
// samples). len(sorted) == 0 returns 0.
func percentile(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	idx := int(math.Ceil(p / 100 * float64(n)))
	if idx < 1 {
		idx = 1
	}
	if idx > n {
		idx = n
	}
	return sorted[idx-1]
}

// Baseline computes a link's historical baseline from a slice of past
// Readings: p50/p95 round-trip time and the mean loss percentage across the
// whole slice — the pure, testable core of Service.Baseline (T-806's
// verify-live UX calls the Service method; this function is what makes it
// golden-testable without a database). An empty samples slice returns all
// zeros, ok=false.
func Baseline(samples []Reading) (p50Ms, p95Ms, lossPct float64, ok bool) {
	if len(samples) == 0 {
		return 0, 0, 0, false
	}
	rtts := make([]float64, len(samples))
	var lossSum float64
	for i, s := range samples {
		rtts[i] = s.RttMs
		lossSum += s.LossPct
	}
	sort.Float64s(rtts)
	p50Ms = percentile(rtts, 50)
	p95Ms = percentile(rtts, 95)
	lossPct = lossSum / float64(len(samples))
	return p50Ms, p95Ms, lossPct, true
}
