package perfbudget

import (
	"fmt"
	"math"
	"runtime"
	"time"
)

// CalibrationWorkload names the kernel below.
//
// It is compared against calibration.workload in perf/budgets.json on every
// run of TestCalibrationMatchesFile. Change the kernel and you must re-measure
// calibration.reference_ns and bump this string in both places — a new kernel
// timed against an old reference silently rescales every calibrated budget,
// and nothing else in the system would notice.
const CalibrationWorkload = "fnv1a-map-mix-v1"

// calibrationChecksum is what calibrationKernel returns, always. Two jobs: it
// is the value the kernel's result feeds into so the optimiser cannot delete
// the loop, and it catches an accidental edit to the kernel that the workload
// name above was not bumped for. Pure uint64 arithmetic, so it is identical on
// every architecture Go compiles for.
const calibrationChecksum = uint64(0xfa5c60f604abe405)

// calibrationWarmup is how long the kernel is run and thrown away before the
// first sample is kept.
//
// Measured, not guessed: on the reference host the kernel takes ~41 ms for its
// first four runs and ~21.5 ms for every run after that — a factor of two,
// switching over at almost exactly 165 ms of sustained load, which is the CPU
// frequency governor ramping rather than anything in this program. Calibrating
// on the cold half would report the machine as 2x slower than it is and stretch
// every calibrated budget by 2x, which is a gate that cannot see a 100%
// regression. Warm-up is expressed as a duration rather than a run count
// because the run count that covers the ramp is itself a property of the
// machine being calibrated.
const calibrationWarmup = 400 * time.Millisecond

const (
	// Sized so one run lands in the tens of milliseconds on the reference
	// host: long enough that the timer's resolution and one scheduler tick do
	// not dominate, short enough that a five-sample median is not itself a
	// noticeable cost in `make check`.
	calibrationIterations = 400_000
	calibrationMapSize    = 4096
)

// calibrationKernel is the fixed unit of work every "calibrated" budget is
// normalised against.
//
// It is a hash-and-map mix rather than a pure arithmetic loop on purpose: the
// Go work these budgets cover (internal/sim's per-request evaluation) is
// dominated by map lookups over a built index, and a calibration that only
// measured ALU throughput would rescale the wrong dimension on a machine whose
// caches, not whose clock, are the difference.
//
// Deterministic by construction: no map iteration order dependence (the result
// is a sum), no time, no randomness, no allocation past the one map.
func calibrationKernel() uint64 {
	m := make(map[uint64]uint64, calibrationMapSize)
	h := uint64(14695981039346656037) // FNV-1a 64-bit offset basis
	for i := 0; i < calibrationIterations; i++ {
		h ^= uint64(i)
		h *= 1099511628211 // FNV-1a 64-bit prime
		k := h % calibrationMapSize
		m[k] += h >> 7
		h += m[(k+1)%calibrationMapSize]
	}
	var sum uint64
	for _, v := range m {
		sum += v
	}
	return sum
}

// CalibrationSample times one run of the kernel.
func CalibrationSample() (time.Duration, error) {
	// The GC deciding to run inside the timed window is the single largest
	// source of variance in a sample this short, and a calibration that is
	// noisier than the thing it is normalising is worse than none.
	runtime.GC()
	start := time.Now()
	got := calibrationKernel()
	elapsed := time.Since(start)
	if got != calibrationChecksum {
		return 0, fmt.Errorf("calibration kernel %q returned %#x, want %#x: the kernel changed but %s still carries the old reference_ns — re-measure it and bump the workload name",
			CalibrationWorkload, got, calibrationChecksum, RepoRelPath)
	}
	return elapsed, nil
}

// Calibrate warms the machine for calibrationWarmup, then runs the kernel n
// times and returns the FASTEST of those n.
//
// Fastest, not median, and the difference is the difference between a gate and
// a suggestion. Every source of error in this measurement is one-directional:
// contention, GC, scheduler preemption and CPU frequency ramp can only make
// the kernel take LONGER than the machine is actually capable of. Nothing can
// make it finish faster than the hardware allows. So the minimum is the best
// available estimate of the machine's true speed, and the median is an
// estimate of "the machine's speed plus whatever noise was typical during
// these five samples".
//
// Why that matters here specifically: Factor divides this by the reference
// host's time and the result only ever LOOSENS a budget (it is floored at 1).
// A calibration that reads high therefore hands out extra headroom — which is
// to say, noise can make the gate pass a real regression. Measured on the dev
// host at merge time (2026-08-12, idle): three consecutive median-of-5
// calibrations returned 41.1 ms, 41.6 ms and 48.8 ms. The third would have
// stretched every calibrated budget by 15% for that run, silently. Taking the
// minimum makes a noisy run produce a TIGHTER budget than a quiet one, which
// is the safe direction to be wrong in: the failure mode becomes a spurious
// red that a re-run clears, not a green that hides a regression.
//
// The measurement side keeps the median (see Median's use in the gate): there,
// the thing being measured IS the workload, a slow outlier is a real
// observation of the system under test, and AC4 requires one outlier not to
// fail the gate. The asymmetry is deliberate — the calibration is measuring
// the ruler, the gate is measuring the object.
func Calibrate(n int) (time.Duration, error) {
	return calibrateWith(n, calibrationWarmup, CalibrationSample)
}

// calibrateWith is Calibrate's body with the sampler injected.
//
// The seam exists so the estimator can be tested deterministically. It cannot
// be tested through Calibrate: CalibrationSample times a real kernel, so on a
// quiet machine the median and the minimum of five samples are within noise of
// each other and a statistical assertion about them passes whichever estimator
// is in use. (Confirmed the hard way — a first version of the test below was
// written against the real sampler, and reverting this function to a median
// did not redden it. A test that cannot fail for the reason it was written is
// worse than none, because it reports coverage that does not exist.)
func calibrateWith(n int, warmup time.Duration, sample func() (time.Duration, error)) (time.Duration, error) {
	if n < 1 {
		return 0, fmt.Errorf("calibrating: samples must be at least 1, got %d", n)
	}
	for start := time.Now(); time.Since(start) < warmup; {
		if _, err := sample(); err != nil {
			return 0, err
		}
	}
	fastest := time.Duration(math.MaxInt64)
	for i := 0; i < n; i++ {
		d, err := sample()
		if err != nil {
			return 0, err
		}
		if d < fastest {
			fastest = d
		}
	}
	return fastest, nil
}

// Factor is how much every "calibrated" limit stretches on this machine:
// measured kernel time over the reference host's, floored at 1 and clamped at
// MaxFactor.
//
// Floored at 1 because a faster machine must not get a TIGHTER budget than the
// one docs/performance.md states — the documented number would stop being the
// contract, and a gate whose limit nobody can read off the page is back to
// being a number in a program. The cost of the floor is stated in the package
// comment: a machine faster than the reference gains no extra sensitivity.
func (f File) Factor(measured time.Duration) (float64, bool) {
	raw := float64(measured) / f.Calibration.ReferenceNS
	return clampFactor(raw, f.Calibration.MaxFactor)
}

// CoresFactor is T-2505's deadline ladder, reused verbatim so the two places
// this repository normalises for machine size cannot drift apart. See
// web/playwright.config.ts's slowFactor, and web/perf/budgets.ts, which is
// where the browser side reads the same ladder.
//
// availableParallelism semantics rather than "how many CPUs does this box
// have": under Linux, Go's runtime.NumCPU already honours the process's CPU
// affinity mask, so a `taskset -c 0,1` run and a cpuset-restricted container
// both read the cores they can actually use.
func CoresFactor(cores int) float64 {
	switch {
	case cores >= 8:
		return 1
	case cores >= 4:
		return 1.5
	default:
		return 2.5
	}
}

func clampFactor(raw, maxFactor float64) (factor float64, clamped bool) {
	switch {
	case raw < 1:
		return 1, false
	case raw > maxFactor:
		return maxFactor, true
	default:
		return raw, false
	}
}
