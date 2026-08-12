//go:build perfslow

package sim

// T-2506's deliberate-slowdown fixture: a real, measurable cost inside the
// real code path the `sim.simulate_10k_wall_ms` budget times.
//
// Deliberately NOT a fabricated number handed to the comparator. AC1 asks for
// a slowed code path, and the point of the distinction is that the whole
// chain — the workload, the clock, the median of N, the calibration factor,
// the comparison, the failure message — has to work end to end for the gate to
// fire. Injecting a fake measurement would exercise the last two links of that
// chain and prove nothing about the first four.
//
// Compiled only under the `perfslow` build tag, which nothing this repository
// ships, tests, lints or packages ever sets; perfslow_off.go's empty
// perfSlowWork is what every other build gets. Same arrangement as
// cmd/vnproxd/soakleak.go (T-2504).

// PerfSlowPasses is how many units of extra work every Simulate call performs.
// Zero — the default — means the tagged build behaves exactly like the
// untagged one, so the harness can prove the fixture is what changed the
// measurement rather than the build tag.
//
// Written by the test harness between samples, read by every Simulate call.
// That is a data race if anything calls Simulate concurrently, which is
// tolerable only because this variable exists in no build anybody runs
// concurrently — and is the reason this cannot be an ordinary runtime flag.
var PerfSlowPasses int

// perfSlowSink keeps the burn loop from being optimised away. A slowdown the
// compiler deletes is a fixture that proves the gate cannot fire.
var perfSlowSink uint64

// perfSlowWork burns PerfSlowPasses units of deterministic CPU inside
// Simulate. One pass is ~1 microsecond on the reference host, so
// PerfSlowPasses=1 roughly doubles the 10,000-simulation workload (~75 ms
// becomes ~85 ms) and PerfSlowPasses=10 puts it far over any budget.
func perfSlowWork() {
	h := perfSlowSink | 1
	for p := 0; p < PerfSlowPasses; p++ {
		for i := 0; i < 512; i++ {
			h ^= uint64(i)
			h *= 1099511628211
			h += h >> 7
		}
	}
	perfSlowSink = h
}
