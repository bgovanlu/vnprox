package capacity

import (
	"math"
	"sort"
	"time"
)

// Forecast fits a simple linear trend over the daily rollups and projects when
// utilization will reach capacity (CapacityThresholdPct).
//
// It fits on MaxUtil — a link that peaks at 100% is full even if its daily
// average is modest, so peak is the honest capacity signal; for IPAM pools the
// daily rollup has a single reading so MaxUtil == AvgUtil and the choice is
// moot. y = MaxUtil, x = bucket instant (unix seconds).
//
// CrossesAt is nil (no finding) when:
//   - there are fewer than two distinct-day points (can't fit a line),
//   - the slope is flat or decreasing (utilization stable/shrinking — never a
//     false-positive forecast), or
//   - the projected crossing lies beyond horizonDays past the last rollup.
//
// When utilization already sits at/above capacity in the observed window the
// crossing is clamped to the last rollup instant ("full now"), which is
// trivially within the horizon.
func Forecast(aggregates []Aggregate, horizonDays int) Projection {
	if horizonDays <= 0 {
		horizonDays = DefaultForecastHorizonDays
	}

	pts := make([]Aggregate, len(aggregates))
	copy(pts, aggregates)
	sort.Slice(pts, func(i, j int) bool { return pts[i].BucketAt.Before(pts[j].BucketAt) })

	slope, intercept, r2, ok := linearFit(pts)
	if !ok {
		return Projection{}
	}
	// Flat or decreasing trend: nothing will be exhausted, no forecast.
	if slope <= 0 {
		return Projection{Confidence: r2}
	}

	lastX := float64(pts[len(pts)-1].BucketAt.Unix())
	crossX := (CapacityThresholdPct - intercept) / slope
	if crossX < lastX {
		// Already at/over capacity within the observed window.
		crossX = lastX
	}

	horizonSecs := float64(horizonDays) * 24 * 3600
	if crossX-lastX > horizonSecs {
		// Will cross, but not within the horizon we care about.
		return Projection{Confidence: r2}
	}

	crossAt := time.Unix(int64(math.Round(crossX)), 0).UTC()
	return Projection{CrossesAt: &crossAt, Confidence: r2}
}

// linearFit computes the ordinary-least-squares slope and intercept of y
// (MaxUtil) against x (bucket unix seconds), plus the fit's R². ok is false
// when there are fewer than two points or x has zero variance (every rollup on
// the same day — no trend to fit).
func linearFit(pts []Aggregate) (slope, intercept, r2 float64, ok bool) {
	n := float64(len(pts))
	if n < 2 {
		return 0, 0, 0, false
	}

	var sumX, sumY, sumXY, sumXX float64
	for _, p := range pts {
		x := float64(p.BucketAt.Unix())
		y := p.MaxUtil
		sumX += x
		sumY += y
		sumXY += x * y
		sumXX += x * x
	}

	denom := n*sumXX - sumX*sumX
	if denom == 0 {
		return 0, 0, 0, false
	}
	slope = (n*sumXY - sumX*sumY) / denom
	intercept = (sumY - slope*sumX) / n

	// R²: 1 - SSres/SStot. SStot == 0 (perfectly flat y) yields a defined,
	// non-forecasting result upstream (slope is 0 too), so report R² 1 for the
	// degenerate "the line fits the flat data exactly" case rather than NaN.
	meanY := sumY / n
	var ssRes, ssTot float64
	for _, p := range pts {
		x := float64(p.BucketAt.Unix())
		pred := slope*x + intercept
		ssRes += (p.MaxUtil - pred) * (p.MaxUtil - pred)
		ssTot += (p.MaxUtil - meanY) * (p.MaxUtil - meanY)
	}
	if ssTot == 0 {
		r2 = 1
	} else {
		r2 = 1 - ssRes/ssTot
	}
	if r2 < 0 {
		r2 = 0
	}
	if r2 > 1 {
		r2 = 1
	}
	return slope, intercept, r2, true
}
