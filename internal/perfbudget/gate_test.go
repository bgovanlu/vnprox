package perfbudget

import (
	"math"
	"strings"
	"testing"
)

func TestMedian(t *testing.T) {
	cases := []struct {
		name    string
		samples []float64
		want    float64
	}{
		{name: "one sample", samples: []float64{7}, want: 7},
		{name: "odd n is a real observation", samples: []float64{5, 1, 3}, want: 3},
		{name: "even n averages the middle pair", samples: []float64{1, 2, 3, 4}, want: 2.5},
		{name: "unsorted input", samples: []float64{9, 1, 8, 2, 7}, want: 7},
		{name: "one huge outlier does not move it", samples: []float64{70, 71, 9000, 72, 73}, want: 72},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Median(tc.samples); got != tc.want {
				t.Fatalf("Median(%v) = %v, want %v", tc.samples, got, tc.want)
			}
		})
	}
	if !math.IsNaN(Median(nil)) {
		t.Fatal("Median of nothing should be NaN, not 0 — 0 would silently pass every max budget")
	}
}

// TestOutlierDoesNotFailTheGate is AC4 at the comparator level; the same
// property is proven end to end against a really-slowed workload by
// `make perf PERF_SLOW=outlier` (see internal/collect/perfslow_on_test.go).
func TestOutlierDoesNotFailTheGate(t *testing.T) {
	b := validBudget() // limit 100 ms, max, 5 samples
	m := FixedMachine(1, 1)

	// Every position, not just the middle: an implementation that read
	// samples[0], or that trimmed the extremes, or that took the last value,
	// would survive a fixture that only ever put the slow sample where that
	// implementation was not looking. (It did survive one, which is how this
	// loop came to be here.)
	for pos := 0; pos < b.Samples; pos++ {
		oneSlow := []float64{70, 71, 72, 73, 74}
		oneSlow[pos] = 900
		r, err := Evaluate(b, oneSlow, m)
		if err != nil {
			t.Fatal(err)
		}
		if !r.Pass {
			t.Fatalf("one 900 ms sample at position %d among four ~70 ms ones must not fail a 100 ms budget; median was %v", pos, r.Median)
		}
		if err := Check([]Result{r}); err != nil {
			t.Fatalf("Check should agree (outlier at %d): %v", pos, err)
		}
	}

	// And the converse, so the test above is not vacuous: when the workload is
	// really slow, one FAST sample must not rescue it.
	allSlow := []float64{900, 901, 70, 902, 903}
	r2, err := Evaluate(b, allSlow, m)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Pass {
		t.Fatal("four slow samples and one fast one must still fail — a median is not a minimum")
	}
}

func TestEvaluate_RejectsTheWrongSampleCount(t *testing.T) {
	b := validBudget()
	_, err := Evaluate(b, []float64{1, 2, 3}, FixedMachine(1, 1))
	if err == nil || !strings.Contains(err.Error(), "samples=5") {
		t.Fatalf("a site measuring 3 samples for a 5-sample budget must be an error, got %v", err)
	}
}

// TestFourPercentPerStepFailsAtTheCrossing is AC3.
//
// The fixture degrades 4% per step across five steps against a budget it
// starts 12% under. No single step is a 5% regression, and a gate that
// compared each run against the previous one would call every step of this
// series fine forever. A threshold gate fails at exactly one step: the one
// where the running value crosses the line, and every step after it.
//
// The series is computed rather than measured on purpose, and that is a
// deliberate limit worth stating: on the reference host the real 10,000-
// simulation workload measures 70-84 ms run to run, a spread of about +-10%,
// so a genuinely-slowed 4% step is well inside this machine's noise and a
// "real fixture" version of this test would be measuring the scheduler. The
// end-to-end proof that a real slowdown moves a real measurement through this
// same comparator is AC1's (`make perf PERF_SLOW=always`); what THIS test
// proves is the property that proof cannot: that the verdict depends only on
// the threshold and never on the previous step.
func TestFourPercentPerStepFailsAtTheCrossing(t *testing.T) {
	b := validBudget() // limit 100 ms, max
	m := FixedMachine(1, 1)

	const steps = 5
	const perStep = 1.04
	base := 88.0 // 12% under budget

	// Where the arithmetic says the crossing is: the first k with
	// 88 * 1.04^k > 100. 88*1.04^3 = 98.99, 88*1.04^4 = 102.95 — step 4.
	wantFirstFailure := 4

	firstFailure := -1
	value := base
	for step := 0; step <= steps; step++ {
		// Five identical samples: this test is about the threshold, and AC4's
		// noise handling is tested separately above.
		samples := []float64{value, value, value, value, value}
		r, err := Evaluate(b, samples, m)
		if err != nil {
			t.Fatal(err)
		}
		stepOverStep := (value/base - 1) * 100
		if step > 0 {
			stepOverStep = (value/(value/perStep) - 1) * 100
		}
		t.Logf("step %d: %.2f ms (%.1f%% over the previous step, %.1f%% over the start), headroom %.1f%%, pass=%v",
			step, value, stepOverStep, (value/base-1)*100, r.Headroom*100, r.Pass)
		if !r.Pass && firstFailure < 0 {
			firstFailure = step
		}
		if firstFailure >= 0 && r.Pass {
			t.Fatalf("step %d passed after step %d had already failed: the gate is not monotonic in the measurement", step, firstFailure)
		}
		value *= perStep
	}

	if firstFailure != wantFirstFailure {
		t.Fatalf("first failing step = %d, want %d — the gate is not comparing against the budget", firstFailure, wantFirstFailure)
	}

	// The load-bearing negative: no step is a 5% regression on its own, so a
	// step-over-step gate with any tolerance at or above 4% never fires here.
	if perStep-1 >= 0.05 {
		t.Fatal("the fixture must degrade by less than 5% per step for this test to mean anything")
	}
}

func TestCheck_NamesTheBudgetAndBothLimits(t *testing.T) {
	b := validBudget()
	m := FixedMachine(2.5, 1) // a slow machine: the effective limit is 250 ms
	r, err := Evaluate(b, []float64{300, 300, 300, 300, 300}, m)
	if err != nil {
		t.Fatal(err)
	}
	err = Check([]Result{r})
	if err == nil {
		t.Fatal("300 ms against a 250 ms effective budget must fail")
	}
	for _, want := range []string{b.ID, "300.0 ms", "100.0 ms", "250.0 ms", "x2.50", "calibrated"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure message should contain %q; got:\n%s", want, err)
		}
	}
}

func TestCheck_IgnoresReportOnlyBudgets(t *testing.T) {
	b := validBudget()
	b.Enforcement = ReportOnly
	b.Scaling = Absolute
	r, err := Evaluate(b, []float64{9000, 9000, 9000, 9000, 9000}, FixedMachine(1, 1))
	if err != nil {
		t.Fatal(err)
	}
	if r.Pass {
		t.Fatal("the result should still record that it is over — report-only is about consequence, not about honesty")
	}
	if err := Check([]Result{r}); err != nil {
		t.Fatalf("a report-only budget must never fail the run: %v", err)
	}
}

// TestReport_ShowsEveryBudgetIncludingPassingOnes is AC5.
func TestReport_ShowsEveryBudgetIncludingPassingOnes(t *testing.T) {
	pass := validBudget()
	fail := validBudget()
	fail.ID = "example.other_ms"
	reportOnly := validBudget()
	reportOnly.ID = "example.hardware_ms"
	reportOnly.Enforcement = ReportOnly
	reportOnly.Scaling = Absolute

	m := FixedMachine(1, 1)
	var results []Result
	for _, in := range []struct {
		b Budget
		v float64
	}{{b: pass, v: 40}, {b: fail, v: 140}, {b: reportOnly, v: 140}} {
		r, err := Evaluate(in.b, []float64{in.v, in.v, in.v, in.v, in.v}, m)
		if err != nil {
			t.Fatal(err)
		}
		results = append(results, r)
	}

	out := Report(results, m)
	for _, want := range []string{
		"example.thing_ms", "example.other_ms", "example.hardware_ms",
		"60.0%",  // the passing budget's headroom is reported, not just the failure
		"-40.0%", // and an exceeded one reports negative headroom rather than vanishing
		"OVER (report-only)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report should contain %q; got:\n%s", want, out)
		}
	}
	lines := strings.Count(strings.TrimSpace(out), "\n") + 1
	if lines != 5 { // header, column titles, three budgets
		t.Errorf("want one line per budget plus two of header, got %d:\n%s", lines, out)
	}
}

func TestReport_SaysWhenTheCalibrationWasClamped(t *testing.T) {
	m := FixedMachine(8, 1)
	m.Clamped = true
	r, err := Evaluate(validBudget(), []float64{1, 1, 1, 1, 1}, m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(Report([]Result{r}, m), "max_factor") {
		t.Fatal("a clamped factor must be visible in the report; silently passing everything is the failure mode the clamp exists to make loud")
	}
}

func TestMissing_CatchesABudgetNothingMeasured(t *testing.T) {
	a := validBudget()
	b := validBudget()
	b.ID = "example.forgotten_ms"
	r, err := Evaluate(a, []float64{1, 1, 1, 1, 1}, FixedMachine(1, 1))
	if err != nil {
		t.Fatal(err)
	}
	err = Missing([]Budget{a, b}, []Result{r})
	if err == nil || !strings.Contains(err.Error(), "example.forgotten_ms") {
		t.Fatalf("want an error naming the unmeasured budget, got %v", err)
	}
	if err := Missing([]Budget{a}, []Result{r}); err != nil {
		t.Fatalf("want no error when everything was measured, got %v", err)
	}
}

func TestMinDirection(t *testing.T) {
	b := validBudget()
	b.Direction = Min
	b.Unit = "fps"
	b.Limit = 30
	m := FixedMachine(1, 1)

	r, err := Evaluate(b, []float64{45, 46, 44, 45, 46}, m)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Pass || r.Headroom <= 0 {
		t.Fatalf("45 fps against a 30 fps floor should pass with positive headroom, got pass=%v headroom=%v", r.Pass, r.Headroom)
	}
	r2, err := Evaluate(b, []float64{20, 21, 19, 20, 21}, m)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Pass {
		t.Fatal("20 fps against a 30 fps floor must fail")
	}
	if !strings.Contains(Check([]Result{r2}).Error(), "under min budget") {
		t.Fatal("a floor's failure message should say the measurement was under it, not over it")
	}
}
