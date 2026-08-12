//go:build !perfslow

package collect_test

import "testing"

// perfSlowConfigure is the no-op half of T-2506's deliberate-slowdown harness:
// what every ordinary `go test`, `make check` and CI run compiles.
//
// Its counterpart, perfslow_on_test.go, is built only under the `perfslow`
// tag — `make perf PERF_SLOW=always|outlier` sets it — and is what proves the
// gate can actually fire. See internal/sim/perfslow_off.go for the slowed code
// path itself, and docs/development.md's "The performance budget gate".
func perfSlowConfigure(*testing.T, int) {}
