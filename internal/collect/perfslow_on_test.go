//go:build perfslow

// SPDX-License-Identifier: Apache-2.0

package collect_test

import (
	"os"
	"strconv"
	"testing"

	"github.com/bgovanlu/vnprox/internal/sim"
)

// T-2506's deliberate-slowdown harness. Compiled only under the `perfslow`
// build tag, which nothing this repository ships, tests, lints or packages
// ever sets; `make perf PERF_SLOW=<mode>` adds it. Same arrangement as
// T-2504's `make soak LEAK=<mode>`.
//
// It configures internal/sim's real, tag-gated slow path (internal/sim/
// perfslow_on.go) between samples of the SAME test the ordinary run executes —
// TestPerfBudgets_Sim. Nothing about the measurement, the median, the
// calibration or the comparison is stubbed: the workload genuinely takes
// longer, and what is being proven is that the whole chain from clock to
// failure message works.
//
// Modes:
//
//	always   every sample is slowed. The median moves, the gate must FAIL and
//	         must name sim.simulate_10k_wall_ms (AC1).
//	outlier  exactly one of the five samples is slowed, by more than enough to
//	         fail on its own. The median does not move, and the gate must PASS
//	         (AC4: one slow run is noise, not a regression).
//
// PERF_SLOW_PASSES overrides how much extra work a slowed sample does; the
// default is chosen to be unmistakably over the budget rather than marginally
// over it, because a fixture that only just crosses the line would make a
// green result ambiguous between "the gate works" and "the machine was busy".
func perfSlowConfigure(t *testing.T, sample int) {
	t.Helper()
	mode := os.Getenv("VNPROX_PERF_SLOW")
	passes := 12
	if raw := os.Getenv("VNPROX_PERF_SLOW_PASSES"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("VNPROX_PERF_SLOW_PASSES=%q: %v", raw, err)
		}
		passes = parsed
	}
	// The slowed sample for "outlier" is deliberately NOT the first or the
	// last: an implementation that discarded an extreme, or that only ever
	// looked at the first sample, would pass a fixture that slowed either end.
	const outlierSample = 2

	switch mode {
	case "always":
		sim.PerfSlowPasses = passes
	case "outlier":
		if sample == outlierSample {
			sim.PerfSlowPasses = passes
		} else {
			sim.PerfSlowPasses = 0
		}
	case "":
		t.Fatalf("this binary was built with -tags perfslow but VNPROX_PERF_SLOW is unset; run `make perf PERF_SLOW=always` or `PERF_SLOW=outlier`")
	default:
		t.Fatalf("VNPROX_PERF_SLOW=%q: want \"always\" or \"outlier\"", mode)
	}
	t.Logf("perfslow: mode=%s sample=%d sim.PerfSlowPasses=%d", mode, sample, sim.PerfSlowPasses)
}
