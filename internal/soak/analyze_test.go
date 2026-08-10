package soak

import (
	"errors"
	"strings"
	"testing"
)

// linear builds a series of n samples one minute apart starting at base and
// changing by step per sample.
func linear(metric, unit string, n int, base, step float64) Series {
	s := Series{Metric: metric, Unit: unit}
	for i := range n {
		s.Elapsed = append(s.Elapsed, float64(i))
		s.Values = append(s.Values, base+step*float64(i))
	}
	return s
}

func testPolicy() Policy {
	return Policy{
		Default:          0.5,
		DefaultMinRise:   1,
		MinWindowSamples: 4,
		PerMetric: map[string]float64{
			MetricGoroutines: 0.5,
			TablePrefix:      1.0,
		},
		MinRise: map[string]float64{
			MetricGoroutines: 1,
			TablePrefix:      1,
		},
	}
}

func TestAnalyze(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		wantInReason string
		series       []Series
		wantFailed   []string
		wantPass     bool
	}{
		{
			// AC1 in analyzer form: one goroutine leaked per 10s collection
			// cycle is 6/min, an order of magnitude over tolerance.
			name:         "goroutine leaked per collection cycle fails and names goroutines",
			series:       []Series{linear(MetricGoroutines, "goroutines", 20, 48, 6)},
			wantPass:     false,
			wantFailed:   []string{MetricGoroutines},
			wantInReason: "goroutines is rising",
		},
		{
			// AC3: absolute value is not the question.
			name:     "flat but very high goroutine count passes",
			series:   []Series{linear(MetricGoroutines, "goroutines", 20, 5000, 0)},
			wantPass: true,
		},
		{
			name:     "falling goroutine count passes",
			series:   []Series{linear(MetricGoroutines, "goroutines", 20, 5000, -3)},
			wantPass: true,
		},
		{
			// AC2 in analyzer form: the failure has to name the table, not
			// just say "a table grew".
			name:         "unbounded table fails and names the table",
			series:       []Series{linear(TablePrefix+"soak_leak_unbounded", "rows", 20, 0, 40)},
			wantPass:     false,
			wantFailed:   []string{TablePrefix + "soak_leak_unbounded"},
			wantInReason: "table.soak_leak_unbounded is rising",
		},
		{
			name:     "table growing slower than its class tolerance passes",
			series:   []Series{linear(TablePrefix+"audit_log", "rows", 20, 100, 0.9)},
			wantPass: true,
		},
		{
			name:     "rising just under tolerance passes",
			series:   []Series{linear(MetricGoroutines, "goroutines", 20, 40, 0.49)},
			wantPass: true,
		},
		{
			name:       "rising just over tolerance fails",
			series:     []Series{linear(MetricGoroutines, "goroutines", 20, 40, 0.51)},
			wantPass:   false,
			wantFailed: []string{MetricGoroutines},
		},
		{
			name: "several metrics failing are all named",
			series: []Series{
				linear(MetricGoroutines, "goroutines", 20, 48, 6),
				linear(MetricOpenFDs, "fds", 20, 30, 2),
				linear(MetricHeapBytes, "bytes", 20, 1e7, 0),
			},
			wantPass:   false,
			wantFailed: []string{MetricGoroutines, MetricOpenFDs},
		},
		{
			// The whole point of windowing to the second half: warm-up must
			// not read as a leak.
			name: "warm-up climb that flattens passes",
			series: []Series{{
				Metric: MetricRSSBytes, Unit: "bytes",
				Elapsed: []float64{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
				Values:  []float64{10, 40, 70, 100, 120, 130, 130, 131, 130, 131},
			}},
			wantPass: true,
		},
		{
			// ...and the mirror image: a flat first half followed by a climb
			// must NOT be averaged away into a pass.
			name: "leak that only starts halfway through still fails",
			series: []Series{{
				Metric: MetricGoroutines, Unit: "goroutines",
				Elapsed: []float64{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
				Values:  []float64{50, 50, 50, 50, 50, 53, 56, 59, 62, 65},
			}},
			wantPass:   false,
			wantFailed: []string{MetricGoroutines},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rep, err := Analyze(tc.series, testPolicy())
			if err != nil {
				t.Fatalf("Analyze: %v", err)
			}
			if rep.Pass != tc.wantPass {
				t.Fatalf("Pass = %v, want %v (verdicts: %+v)", rep.Pass, tc.wantPass, rep.Verdicts)
			}
			if tc.wantFailed != nil {
				if len(rep.Failed) != len(tc.wantFailed) {
					t.Fatalf("Failed = %v, want %v", rep.Failed, tc.wantFailed)
				}
				for i := range tc.wantFailed {
					if rep.Failed[i] != tc.wantFailed[i] {
						t.Fatalf("Failed = %v, want %v", rep.Failed, tc.wantFailed)
					}
				}
			}
			err = rep.Err()
			if tc.wantPass {
				if err != nil {
					t.Fatalf("Err() = %v, want nil for a passing report", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Err() = nil for a failing report")
			}
			for _, name := range tc.wantFailed {
				if !strings.Contains(err.Error(), name) {
					t.Errorf("Err() = %q, does not name failing metric %q", err, name)
				}
			}
			if tc.wantInReason != "" && !strings.Contains(err.Error(), tc.wantInReason) {
				t.Errorf("Err() = %q, want it to contain %q", err, tc.wantInReason)
			}
		})
	}
}

// TestAnalyzeMinRiseFloor covers the second half of the fail condition: a
// slope over tolerance that projects almost no absolute growth across a
// short window is short-window noise, not a leak — and the same series
// observed over a window long enough for the growth to matter must fail.
func TestAnalyzeMinRiseFloor(t *testing.T) {
	t.Parallel()
	p := testPolicy()
	p.MinRise = map[string]float64{MetricGoroutines: 4}

	// Eight samples one second apart: a 3-second (0.05-min) second-half
	// window. The fitted slope is 60/min, far over tolerance, but it
	// projects a rise of 3 — under the floor of 4.
	short := Series{Metric: MetricGoroutines, Unit: "goroutines"}
	for i := range 8 {
		short.Elapsed = append(short.Elapsed, float64(i)/60)
		short.Values = append(short.Values, 40+float64(i))
	}
	rep, err := Analyze([]Series{short}, p)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !rep.Pass {
		t.Fatalf("a +3-unit wobble over 3 seconds failed the gate: %+v", rep.Verdicts)
	}
	if !strings.Contains(rep.Verdicts[0].Reason, "short-window noise") {
		t.Errorf("passing reason %q does not explain the rise floor", rep.Verdicts[0].Reason)
	}

	// The same 60/min rate observed over a window long enough to matter
	// must fail — the floor must not become a permanent exemption.
	long := Series{Metric: MetricGoroutines, Unit: "goroutines"}
	for i := range 8 {
		long.Elapsed = append(long.Elapsed, float64(i))
		long.Values = append(long.Values, 40+60*float64(i))
	}
	rep, err = Analyze([]Series{long}, p)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if rep.Pass {
		t.Fatal("60 goroutines/min sustained over a 3-minute window passed the gate")
	}
}

func TestAnalyzeTooFewSamples(t *testing.T) {
	t.Parallel()
	// 6 samples -> a 3-sample second-half window, below MinWindowSamples=4.
	_, err := Analyze([]Series{linear(MetricGoroutines, "goroutines", 6, 40, 0)}, testPolicy())
	if !errors.Is(err, ErrTooFewSamples) {
		t.Fatalf("Analyze error = %v, want ErrTooFewSamples", err)
	}
	if !strings.Contains(err.Error(), MetricGoroutines) {
		t.Errorf("error %q does not name the metric it could not judge", err)
	}
}

// TestAnalyzeUndefinedFitIsNotAPass guards the specific way a leak gate
// rots into a rubber stamp: an undefined regression (every sample at the
// same instant) quietly scoring as slope 0.
func TestAnalyzeUndefinedFitIsNotAPass(t *testing.T) {
	t.Parallel()
	s := Series{
		Metric: MetricGoroutines, Unit: "goroutines",
		Elapsed: []float64{1, 1, 1, 1, 1, 1, 1, 1},
		Values:  []float64{40, 60, 80, 100, 120, 140, 160, 180},
	}
	rep, err := Analyze([]Series{s}, testPolicy())
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if rep.Pass {
		t.Fatal("an undefined trend fit passed the gate; it must be reported as inconclusive")
	}
	if !strings.Contains(rep.Err().Error(), "inconclusive") {
		t.Errorf("failure reason %q does not say the run was inconclusive", rep.Err())
	}
}

func TestPolicyToleranceFor(t *testing.T) {
	t.Parallel()
	p := Policy{
		Default: 1,
		PerMetric: map[string]float64{
			MetricGoroutines:               0.25,
			TablePrefix:                    10,
			TablePrefix + "metric_samples": 500,
		},
	}
	tests := []struct {
		metric string
		want   float64
	}{
		{MetricGoroutines, 0.25},
		{MetricRSSBytes, 1},
		{TablePrefix + "audit_log", 10},
		{TablePrefix + "metric_samples", 500},
	}
	for _, tc := range tests {
		if got := p.ToleranceFor(tc.metric); got != tc.want {
			t.Errorf("ToleranceFor(%q) = %v, want %v", tc.metric, got, tc.want)
		}
	}
}
