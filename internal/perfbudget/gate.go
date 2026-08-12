package perfbudget

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"sort"
	"strings"
)

// Median is the aggregation every budget's verdict is taken over.
//
// Median and not mean: T-2506 AC4. One sample landing on a GC pause, a
// scheduler preemption or a sibling process's burst moves a mean of five by
// a fifth of its excess and moves a median of five not at all. That is the
// whole noise policy — there is no retry, no tolerance band, and no
// comparison against the previous run.
//
// An even N takes the mean of the two middle samples, which is the ordinary
// definition; MinGateSamples is odd so a gate's median is always a real
// observation, not an average of two.
func Median(samples []float64) float64 {
	if len(samples) == 0 {
		return math.NaN()
	}
	sorted := append([]float64(nil), samples...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

// Machine is the measuring host, resolved once per run and applied to every
// budget so a report can say what it normalised by.
type Machine struct {
	// CalibrationNS is the median wall time of calibrate.go's kernel here.
	CalibrationNS float64
	// CalibratedFactor is CalibrationNS over the reference host's, floored at
	// 1 and clamped at MaxFactor.
	CalibratedFactor float64
	// CoresFactor is T-2505's availableParallelism ladder for Cores.
	CoresFactor float64
	// Cores is what runtime.NumCPU reports — which honours this process's CPU
	// affinity mask, so a taskset-restricted run reads the cores it can use.
	Cores int
	// Clamped records that CalibratedFactor hit MaxFactor: the machine
	// measured so much slower than the reference that its budgets were NOT
	// stretched all the way, and the report says so rather than quietly
	// passing everything.
	Clamped bool
}

// Detect measures this machine against the file's reference host.
func Detect(f File) (Machine, error) {
	d, err := Calibrate(f.Calibration.Samples)
	if err != nil {
		return Machine{}, fmt.Errorf("detecting machine: %w", err)
	}
	factor, clamped := f.Factor(d)
	cores := runtime.NumCPU()
	return Machine{
		CalibrationNS:    float64(d),
		CalibratedFactor: factor,
		CoresFactor:      CoresFactor(cores),
		Cores:            cores,
		Clamped:          clamped,
	}, nil
}

// FixedMachine is a Machine with no measurement in it, for tests that need a
// deterministic normalisation rather than this host's.
func FixedMachine(calibrated, cores float64) Machine {
	return Machine{
		CalibrationNS:    0,
		CalibratedFactor: calibrated,
		CoresFactor:      cores,
		Cores:            runtime.NumCPU(),
		Clamped:          false,
	}
}

// FactorFor is the multiplier this machine applies to one budget's limit.
func (m Machine) FactorFor(b Budget) (float64, error) {
	switch b.Scaling {
	case Calibrated:
		return m.CalibratedFactor, nil
	case Cores:
		return m.CoresFactor, nil
	case Absolute:
		return 1, nil
	default:
		return 0, fmt.Errorf("budget %s: unknown scaling %q", b.ID, b.Scaling)
	}
}

// Result is one budget's verdict, with everything a reader needs to argue with
// it: the raw samples, the median that decided it, the limit as written, the
// limit as normalised for this machine, and the headroom.
type Result struct {
	// Samples leads and Budget follows: govet's fieldalignment wants the
	// shortest pointer-bearing prefix, and a slice's one pointer word in front
	// of Budget's nine strings is cheaper than the other way round.
	Samples []float64
	Budget  Budget
	// Median is the value compared against Effective.
	Median float64
	// Effective is Budget.Limit scaled by Factor: the number actually enforced
	// here.
	Effective float64
	// Factor is the normalisation this machine applied.
	Factor float64
	// Headroom is the fraction of the effective budget still unused, signed:
	// 0.42 means 42% of the budget is spare, -0.10 means it is 10% over. AC5
	// reports this for every budget on every run, passing ones included,
	// because a budget at 3% headroom is a fact worth knowing before it breaks.
	Headroom float64
	// Pass is whether the median is on the right side of Effective. A
	// report-only budget records its Pass honestly and still never fails the
	// run; Check is what decides that.
	Pass bool
}

// Evaluate compares samples against a budget on a machine.
//
// Note what is NOT a parameter: any previous run. T-2506's gate is
// threshold-based by construction, so a drift of 4% a step across five steps
// fails at the step that crosses the line and not before — there is nothing
// here for a step-over-step comparison to be made against.
func Evaluate(b Budget, samples []float64, m Machine) (Result, error) {
	if len(samples) == 0 {
		return Result{}, fmt.Errorf("budget %s: no samples", b.ID)
	}
	if len(samples) != b.Samples {
		return Result{}, fmt.Errorf("budget %s: %s declares samples=%d, got %d — the median N is part of the budget, not the caller's choice",
			b.ID, RepoRelPath, b.Samples, len(samples))
	}
	factor, err := m.FactorFor(b)
	if err != nil {
		return Result{}, err
	}
	med := Median(samples)
	eff := b.Limit * factor
	r := Result{
		Budget:    b,
		Samples:   append([]float64(nil), samples...),
		Median:    med,
		Effective: eff,
		Factor:    factor,
		Headroom:  0,
		Pass:      false,
	}
	switch b.Direction {
	case Max:
		r.Pass = med <= eff
		r.Headroom = (eff - med) / eff
	case Min:
		r.Pass = med >= eff
		r.Headroom = (med - eff) / eff
	default:
		return Result{}, fmt.Errorf("budget %s: unknown direction %q", b.ID, b.Direction)
	}
	return r, nil
}

// Measure runs one budget's measurement Samples times and evaluates it. The
// sample count comes from the budget, so a site cannot decide to gate on one
// run of something the file says takes five.
func Measure(b Budget, m Machine, measure func(sample int) (float64, error)) (Result, error) {
	samples := make([]float64, 0, b.Samples)
	for i := 0; i < b.Samples; i++ {
		v, err := measure(i)
		if err != nil {
			return Result{}, fmt.Errorf("budget %s: sample %d: %w", b.ID, i, err)
		}
		samples = append(samples, v)
	}
	return Evaluate(b, samples, m)
}

// Check is the gate. It fails on gating budgets only, and every message names
// the budget, both limits and the measurement.
func Check(results []Result) error {
	var over []string
	for _, r := range results {
		if r.Pass || r.Budget.Enforcement != Gate {
			continue
		}
		rel := "over"
		if r.Budget.Direction == Min {
			rel = "under"
		}
		over = append(over, fmt.Sprintf(
			"%s (%s): %s %s budget — measured %s (median of %d), budget %s, effective %s here (x%.2f %s normalisation). %s",
			r.Budget.ID, r.Budget.Title, rel, r.Budget.Direction,
			format(r.Median, r.Budget.Unit), len(r.Samples),
			format(r.Budget.Limit, r.Budget.Unit), format(r.Effective, r.Budget.Unit),
			r.Factor, r.Budget.Scaling, r.Budget.Why))
	}
	if len(over) == 0 {
		return nil
	}
	return fmt.Errorf("performance budget exceeded (%s):\n  %s", RepoRelPath, strings.Join(over, "\n  "))
}

// Report renders every result, passing ones included — T-2506 AC5. A gate that
// only speaks when it fails cannot show a budget being approached.
func Report(results []Result, m Machine) string {
	var b strings.Builder
	fmt.Fprintf(&b, "performance budgets (%s), %d cores, calibration %.1f ms, x%.2f calibrated / x%.2f cores\n",
		RepoRelPath, m.Cores, m.CalibrationNS/1e6, m.CalibratedFactor, m.CoresFactor)
	if m.Clamped {
		fmt.Fprintf(&b, "  NOTE: the calibrated factor hit max_factor — this machine measured slower than the clamp allows for, so budgets were NOT stretched to match it\n")
	}
	fmt.Fprintf(&b, "  %-40s %10s %10s %10s %9s %s\n", "budget", "median", "budget", "effective", "headroom", "verdict")
	for _, r := range results {
		verdict := "PASS"
		switch {
		case r.Pass:
		case r.Budget.Enforcement == ReportOnly:
			verdict = "OVER (report-only)"
		default:
			verdict = "FAIL"
		}
		fmt.Fprintf(&b, "  %-40s %10s %10s %10s %8.1f%% %s\n",
			r.Budget.ID,
			format(r.Median, r.Budget.Unit),
			format(r.Budget.Limit, r.Budget.Unit),
			format(r.Effective, r.Budget.Unit),
			r.Headroom*100,
			verdict)
	}
	return b.String()
}

// Missing names budgets a site was expected to measure and did not. A budget
// that silently stops being measured still reads as green, which is the same
// failure mode as a spec file in no shard (T-2108, and T-2505's manifest
// check).
func Missing(expected []Budget, measured []Result) error {
	got := make(map[string]bool, len(measured))
	for _, r := range measured {
		got[r.Budget.ID] = true
	}
	var missing []string
	for _, b := range expected {
		if !got[b.ID] {
			missing = append(missing, b.ID)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return errors.New("these budgets name this site in " + RepoRelPath + " but nothing measured them: " + strings.Join(missing, ", "))
}

func format(v float64, unit string) string {
	return fmt.Sprintf("%.1f %s", v, unit)
}
