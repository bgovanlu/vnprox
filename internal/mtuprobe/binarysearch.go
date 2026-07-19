package mtuprobe

import "fmt"

// MinMTU/MaxMTU bound the binary search this package performs for every
// path — MinMTU is the practical floor most IP stacks tolerate (below it a
// path is unusable regardless of what this prober reports); MaxMTU is the
// jumbo-frame ceiling this codebase's own change.VxlanOverhead math and
// PVE's own SDN zone MTU field both already assume as the practical upper
// bound (docs/features/monitoring.md §5, health_vxlanmtu.go).
const (
	MinMTU = 552
	MaxMTU = 9216
)

// ProbeFunc attempts one DF-set (Don't Fragment) probe of exactly size
// bytes (the full path MTU being tested, not just the payload) toward one
// target, reporting whether a probe of that size got through (ok=true) or
// was dropped/fragmentation-needed (ok=false). A non-nil error means the
// probe could not even be attempted (dial/exec failure) — distinguished
// from an honest "too big" negative result, mirroring internal/latmesh.
// Prober's honesty-first convention (RealProber's doc comment): a probe
// that never got a chance to run is never conflated with one that ran and
// got no reply.
type ProbeFunc func(size int) (ok bool, err error)

// BinarySearchMTU finds the largest size in [lo, hi] for which probe
// reports ok, using integer binary search — the largest-true-value variant
// (not the more common "find any true/false boundary" search): it always
// probes lo first (bisecting further is pointless if even the floor
// fails), then repeatedly probes the midpoint of the remaining candidate
// range, narrowing toward the true boundary.
//
// lo/hi default to MinMTU/MaxMTU when <= 0. Converges in at most
// ceil(log2(hi-lo+1))+1 probes (the +1 for the initial lo-floor probe) —
// for the full default range (552..9216) that is at most 15 probes, well
// within AC1's "bounded probe count".
//
// Returns mtu=0 (not an error) when even the floor probe fails — an
// honestly-reported "no usable MTU found in range" result, the same
// zero-is-not-an-error stance internal/latmesh.parsePingSummary's fallback
// takes for "can't confirm, report the pessimistic reading" rather than a
// misleadingly-cheerful default. probeCount is always the exact number of
// probe() calls made, for tests to assert against the documented bound.
func BinarySearchMTU(probe ProbeFunc, lo, hi int) (mtu int, probeCount int, err error) {
	if lo <= 0 {
		lo = MinMTU
	}
	if hi <= 0 {
		hi = MaxMTU
	}
	if lo > hi {
		return 0, 0, fmt.Errorf("mtuprobe: binary search range invalid: lo=%d > hi=%d", lo, hi)
	}

	ok, err := probe(lo)
	probeCount++
	if err != nil {
		return 0, probeCount, fmt.Errorf("mtuprobe: probing floor size %d: %w", lo, err)
	}
	if !ok {
		return 0, probeCount, nil // even the floor MTU failed on this path
	}

	best := lo
	for lo < hi {
		mid := lo + (hi-lo+1)/2 // bias toward hi so lo==mid never repeats the same probe
		ok, err := probe(mid)
		probeCount++
		if err != nil {
			return 0, probeCount, fmt.Errorf("mtuprobe: probing size %d: %w", mid, err)
		}
		if ok {
			best = mid
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return best, probeCount, nil
}
