// SPDX-License-Identifier: Apache-2.0

package soak

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// TablePrefix is the metric-name prefix every SQLite row-count series
// carries, so a verdict for "table.audit_log" is unambiguously a table and
// a Policy can address the whole class at once.
const TablePrefix = "table."

// ErrTooFewSamples is returned by Analyze when the second-half window holds
// fewer samples than Policy.MinWindowSamples. It is deliberately an error
// rather than a pass: a run too short to have an opinion must not be
// mistaken for a run that found nothing.
var ErrTooFewSamples = errors.New("soak: too few samples in the trend window to reach a verdict")

// Series is one metric's sample history from a single run. Elapsed is
// minutes since the run started (the slope's x axis, so every tolerance in
// this package is stated per minute regardless of run length); Values is
// the sampled value at that moment. The two slices are always the same
// length.
type Series struct {
	Metric  string
	Unit    string
	Elapsed []float64
	Values  []float64
}

// Policy is the per-metric trend tolerance, in units per minute. A metric
// whose second-half slope exceeds its tolerance fails; a metric that is
// flat, falling, or rising slower than its tolerance passes no matter how
// large its absolute value is.
//
// Tolerances are per minute rather than per run so that the same policy
// gates a 60-second smoke run and an 8-hour nightly run without rescaling.
// A longer run is not more permissive — it is more sensitive, because more
// samples make the slope estimate tighter.
type Policy struct {
	// PerMetric overrides Default for an exact metric name (e.g.
	// "goroutines") or for a whole class via a "table." prefix key.
	PerMetric map[string]float64
	// MinRise is the second condition a metric must meet to fail: the
	// fitted trend must predict at least this much *total* growth across
	// the observed window, in the metric's own units. Keys resolve exactly
	// like PerMetric's, falling back to DefaultMinRise.
	//
	// This exists because a slope estimated over a short window is noisy in
	// a way a slope estimated over a long one is not. The second half of a
	// two-minute run spans one minute, so a metric that merely jitters by
	// three between samples can fit a slope of several units per minute
	// without anything having leaked. Requiring the fit to also predict a
	// real, absolute rise across the window makes the same policy usable at
	// 60 seconds and at 8 hours: short runs are effectively gated on
	// absolute growth, long runs on rate (because MinRise divided by a
	// four-hour window is far below any sane rate tolerance). It is the one
	// knob that keeps this gate from crying wolf, which is the failure mode
	// that gets a gate switched off.
	MinRise map[string]float64
	// Default applies to any metric with no PerMetric entry.
	Default float64
	// DefaultMinRise applies to any metric with no MinRise entry.
	DefaultMinRise float64
	// MinWindowSamples is the number of samples the second-half window must
	// hold before Analyze will reach any verdict at all. Below it, Analyze
	// returns ErrTooFewSamples.
	MinWindowSamples int
}

// ToleranceFor resolves the tolerance for a metric: its exact PerMetric
// entry, else the "table." class entry for a table series, else Default.
func (p Policy) ToleranceFor(metric string) float64 {
	return resolve(p.PerMetric, metric, p.Default)
}

// MinRiseFor resolves the absolute-rise floor for a metric, by the same
// exact-then-class-then-default lookup ToleranceFor uses.
func (p Policy) MinRiseFor(metric string) float64 {
	return resolve(p.MinRise, metric, p.DefaultMinRise)
}

func resolve(m map[string]float64, metric string, fallback float64) float64 {
	if v, ok := m[metric]; ok {
		return v
	}
	if strings.HasPrefix(metric, TablePrefix) {
		if v, ok := m[TablePrefix]; ok {
			return v
		}
	}
	return fallback
}

// Verdict is one metric's outcome.
type Verdict struct {
	Metric        string  `json:"metric"`
	Unit          string  `json:"unit"`
	Reason        string  `json:"reason"`
	SlopePerMin   float64 `json:"slope_per_min"`
	Tolerance     float64 `json:"tolerance_per_min"`
	MinRise       float64 `json:"min_rise"`
	ProjectedRise float64 `json:"projected_rise_over_window"`
	WindowMinutes float64 `json:"window_minutes"`
	WindowFirst   float64 `json:"window_first"`
	WindowLast    float64 `json:"window_last"`
	WindowMin     float64 `json:"window_min"`
	WindowMax     float64 `json:"window_max"`
	WindowSamples int     `json:"window_samples"`
	Pass          bool    `json:"pass"`
}

// Report is the whole run's verdict.
type Report struct {
	Verdicts []Verdict `json:"verdicts"`
	Failed   []string  `json:"failed"`
	Pass     bool      `json:"pass"`
}

// Err reports the gate failure as an error naming every metric that rose,
// or nil if the run passed. This is what a caller turns into a test
// failure or a non-zero exit.
func (r Report) Err() error {
	if r.Pass {
		return nil
	}
	lines := make([]string, 0, len(r.Verdicts)+1)
	lines = append(lines, fmt.Sprintf("soak: resource-leak gate FAILED on %d metric(s): %s",
		len(r.Failed), strings.Join(r.Failed, ", ")))
	for _, v := range r.Verdicts {
		if !v.Pass {
			lines = append(lines, "  - "+v.Reason)
		}
	}
	return errors.New(strings.Join(lines, "\n"))
}

// window returns the second half of s — indices [len/2, len) — as the
// (x, y) pair the slope is fitted over. For an odd sample count the extra
// sample goes to the first (discarded, warm-up) half, which is the
// conservative choice: it can only shrink the window, never dilute it with
// warm-up.
func (s Series) window() (xs, ys []float64) {
	n := len(s.Values)
	if n != len(s.Elapsed) {
		n = min(n, len(s.Elapsed))
	}
	start := n / 2
	return s.Elapsed[start:n], s.Values[start:n]
}

// Analyze fits each series' second half and compares its slope against the
// policy's tolerance for that metric.
//
// It returns ErrTooFewSamples if any series' window is shorter than
// Policy.MinWindowSamples — the run is inconclusive, not clean. Verdicts
// come back sorted by metric name so an artifact diffs cleanly between
// runs.
func Analyze(series []Series, p Policy) (Report, error) {
	rep := Report{Pass: true, Verdicts: make([]Verdict, 0, len(series))}
	for _, s := range series {
		xs, ys := s.window()
		if len(ys) < p.MinWindowSamples {
			return Report{}, fmt.Errorf("%w: metric %q has %d sample(s) in its second-half window, need %d",
				ErrTooFewSamples, s.Metric, len(ys), p.MinWindowSamples)
		}
		tol := p.ToleranceFor(s.Metric)
		minRise := p.MinRiseFor(s.Metric)
		windowMinutes := 0.0
		if len(xs) > 1 {
			windowMinutes = xs[len(xs)-1] - xs[0]
		}
		v := Verdict{
			Metric:        s.Metric,
			Unit:          s.Unit,
			Tolerance:     tol,
			MinRise:       minRise,
			WindowMinutes: windowMinutes,
			WindowSamples: len(ys),
		}
		if len(ys) > 0 {
			v.WindowFirst, v.WindowLast = ys[0], ys[len(ys)-1]
			v.WindowMin, v.WindowMax = ys[0], ys[0]
			for _, y := range ys {
				v.WindowMin = min(v.WindowMin, y)
				v.WindowMax = max(v.WindowMax, y)
			}
		}

		slope, ok := Slope(xs, ys)
		v.SlopePerMin = slope
		v.ProjectedRise = slope * windowMinutes
		switch {
		case !ok:
			// Undefined fit (all samples at the same instant, non-finite
			// values). Never silently "flat" — see Slope's doc comment.
			v.SlopePerMin, v.ProjectedRise = 0, 0
			v.Pass = false
			v.Reason = fmt.Sprintf("%s: no trend could be fitted over %d sample(s) in the second-half window "+
				"(identical or non-finite timestamps) — the run is inconclusive, not clean", s.Metric, len(ys))
		case slope > tol && v.ProjectedRise > minRise:
			v.Pass = false
			v.Reason = fmt.Sprintf("%s is rising at %.4f %s/min over the second half of the run "+
				"(tolerance %.4f %s/min), a projected +%.0f %s across the %.2f-min window "+
				"(floor %.0f; window %d samples, %.0f -> %.0f, min %.0f, max %.0f)",
				s.Metric, slope, s.Unit, tol, s.Unit, v.ProjectedRise, s.Unit, windowMinutes, minRise,
				len(ys), v.WindowFirst, v.WindowLast, v.WindowMin, v.WindowMax)
		case slope > tol:
			v.Pass = true
			v.Reason = fmt.Sprintf("%s slope %.4f %s/min exceeds tolerance %.4f %s/min but projects only "+
				"+%.0f %s across the %.2f-min window, under the %.0f floor that separates a trend from "+
				"short-window noise (window %d samples, min %.0f, max %.0f)",
				s.Metric, slope, s.Unit, tol, s.Unit, v.ProjectedRise, s.Unit, windowMinutes, minRise,
				len(ys), v.WindowMin, v.WindowMax)
		default:
			v.Pass = true
			v.Reason = fmt.Sprintf("%s slope %.4f %s/min within tolerance %.4f %s/min (window %d samples, min %.0f, max %.0f)",
				s.Metric, slope, s.Unit, tol, s.Unit, len(ys), v.WindowMin, v.WindowMax)
		}
		if !v.Pass {
			rep.Pass = false
			rep.Failed = append(rep.Failed, s.Metric)
		}
		rep.Verdicts = append(rep.Verdicts, v)
	}
	sort.Slice(rep.Verdicts, func(i, j int) bool { return rep.Verdicts[i].Metric < rep.Verdicts[j].Metric })
	sort.Strings(rep.Failed)
	return rep, nil
}
