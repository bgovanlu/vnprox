package perfbudget

import (
	"fmt"
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
// times and returns the median of those n.
func Calibrate(n int) (time.Duration, error) {
	if n < 1 {
		return 0, fmt.Errorf("calibrating: samples must be at least 1, got %d", n)
	}
	for start := time.Now(); time.Since(start) < calibrationWarmup; {
		if _, err := CalibrationSample(); err != nil {
			return 0, err
		}
	}
	samples := make([]float64, 0, n)
	for i := 0; i < n; i++ {
		d, err := CalibrationSample()
		if err != nil {
			return 0, err
		}
		samples = append(samples, float64(d))
	}
	return time.Duration(Median(samples)), nil
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
