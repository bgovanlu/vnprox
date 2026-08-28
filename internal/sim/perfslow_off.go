//go:build !perfslow

// SPDX-License-Identifier: Apache-2.0

package sim

// perfSlowWork is the no-op half of T-2506's deliberate-slowdown fixture.
//
// This is the file every shipped build, every `go build`, every `go test` and
// every lint run compiles: an empty function the inliner erases, so
// Simulate's hot path costs exactly what it did before T-2506. Its
// counterpart, perfslow_on.go, is compiled only under the `perfslow` build tag
// that nothing this repository ships, tests, lints or packages ever sets —
// the same arrangement cmd/vnproxd/soakleak.go uses for T-2504's leak
// fixtures, and for the same reason: a gate nobody has watched fail is a gate
// nobody has evidence about.
//
// See internal/collect/sim_bench_test.go for the harness that drives it and
// `make perf PERF_SLOW=...` for how to run it.
func perfSlowWork() {}
