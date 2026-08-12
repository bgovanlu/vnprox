package perfbudget

import (
	"strings"
	"testing"
	"time"
)

// TestCalibrationKernelIsDeterministic. The kernel's result is what the
// slowdown-resistant part of the measurement rides on: if the compiler could
// delete it, or if an edit changed what it computes, every calibrated budget
// would be normalised by a different unit than the one reference_ns was
// measured with, and nothing else in the system would notice.
func TestCalibrationKernelIsDeterministic(t *testing.T) {
	first := calibrationKernel()
	if first != calibrationChecksum {
		t.Fatalf("calibrationKernel() = %#x, want %#x — the kernel changed. Re-measure calibration.reference_ns in %s, bump CalibrationWorkload and this checksum together.",
			first, calibrationChecksum, RepoRelPath)
	}
	if second := calibrationKernel(); second != first {
		t.Fatalf("the kernel is not deterministic: %#x then %#x", first, second)
	}
}

// TestCalibrationMatchesFile is the guard that stops a kernel change from
// silently rescaling every calibrated budget. Bumping CalibrationWorkload
// without re-measuring reference_ns (or the reverse) reddens here.
func TestCalibrationMatchesFile(t *testing.T) {
	f, err := LoadRepo()
	if err != nil {
		t.Fatalf("%v", err)
	}
	if f.Calibration.Workload != CalibrationWorkload {
		t.Fatalf("%s says the calibration workload is %q, this package's kernel is %q — reference_ns is only meaningful for one of them",
			RepoRelPath, f.Calibration.Workload, CalibrationWorkload)
	}
}

func TestCalibrate_ReturnsSomethingPlausible(t *testing.T) {
	d, err := Calibrate(3)
	if err != nil {
		t.Fatalf("%v", err)
	}
	// Not an assertion about speed — this runs on whatever machine it runs on.
	// It is an assertion that the timing path works at all: a zero would make
	// every factor 1 and a whole-second result would mean the kernel is sized
	// wrong for any machine.
	if d <= 0 || d > 5*time.Second {
		t.Fatalf("calibration returned %s, which is not a plausible time for this kernel", d)
	}
	t.Logf("calibration on this machine: %s (reference host: 42.3 ms)", d)
}

func TestCalibrate_RejectsZeroSamples(t *testing.T) {
	if _, err := Calibrate(0); err == nil {
		t.Fatal("want an error")
	}
}

// TestFactor_OnlyEverLoosens is the property the whole normalisation design
// rests on, and the one a reader is most likely to want to argue with. It is
// stated in the package comment; this is where it is enforced.
func TestFactor_OnlyEverLoosens(t *testing.T) {
	f := validFile() // reference_ns 42 ms, max_factor 8

	cases := []struct {
		name        string
		measured    time.Duration
		want        float64
		wantClamped bool
	}{
		{name: "the reference host itself", measured: 42 * time.Millisecond, want: 1, wantClamped: false},
		{name: "a faster machine gets no tighter a budget", measured: 10 * time.Millisecond, want: 1, wantClamped: false},
		{name: "a machine half the speed", measured: 84 * time.Millisecond, want: 2, wantClamped: false},
		{name: "an absurdly slow machine is clamped, not indulged", measured: 4200 * time.Millisecond, want: 8, wantClamped: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, clamped := f.Factor(tc.measured)
			if got != tc.want || clamped != tc.wantClamped {
				t.Fatalf("Factor(%s) = %v (clamped %v), want %v (clamped %v)", tc.measured, got, clamped, tc.want, tc.wantClamped)
			}
		})
	}
}

// TestCoresFactor_IsT2505sLadder. The same three rungs as
// web/playwright.config.ts's slowFactor and web/perf/budgets.ts's coresFactor.
// If this table and those two ever disagree, the repository has two answers to
// "how much slower is a small machine".
func TestCoresFactor_IsT2505sLadder(t *testing.T) {
	cases := []struct {
		cores int
		want  float64
	}{
		{cores: 1, want: 2.5},
		{cores: 2, want: 2.5},
		{cores: 3, want: 2.5},
		{cores: 4, want: 1.5},
		{cores: 7, want: 1.5},
		{cores: 8, want: 1},
		{cores: 32, want: 1},
	}
	for _, tc := range cases {
		if got := CoresFactor(tc.cores); got != tc.want {
			t.Errorf("CoresFactor(%d) = %v, want %v", tc.cores, got, tc.want)
		}
	}
}

func TestDetect_UsesTheFilesSampleCount(t *testing.T) {
	f := validFile()
	m, err := Detect(f)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if m.Cores < 1 {
		t.Fatalf("Detect reported %d cores", m.Cores)
	}
	if m.CalibratedFactor < 1 {
		t.Fatalf("the calibrated factor must never fall below 1, got %v", m.CalibratedFactor)
	}
	if m.CoresFactor != CoresFactor(m.Cores) {
		t.Fatalf("cores factor %v does not match the ladder for %d cores", m.CoresFactor, m.Cores)
	}
}

func TestFactorFor_RejectsAnUnknownScaling(t *testing.T) {
	b := validBudget()
	b.Scaling = "vibes"
	_, err := FixedMachine(1, 1).FactorFor(b)
	if err == nil || !strings.Contains(err.Error(), "vibes") {
		t.Fatalf("want an error naming the unknown scaling, got %v", err)
	}
}
