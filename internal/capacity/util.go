// SPDX-License-Identifier: Apache-2.0

package capacity

import "time"

// CounterSample is one point-in-time byte-counter reading for a link, the
// minimal slice of a metric_samples row LinkDailyUtil needs. The cmd/vnproxd
// link source maps store.MetricSample into this shape.
type CounterSample struct {
	At      time.Time
	RxBytes uint64
	TxBytes uint64
}

// LinkDailyUtil computes the mean and peak one-directional utilization
// (percent) across ordered counter samples, given the link's speed in Mbps.
//
// It derives a per-interval rate from adjacent counter deltas (the same
// counter-rate approach metrics.ComputeRates uses) and expresses the busier
// direction as a percentage of the link speed. ok is false — so the rollup can
// skip a ref rather than record a misleading zero — when there are fewer than
// two usable samples or the speed is unknown (<= 0, e.g. an unnegotiated link
// or a bond with no active-slave speed).
//
// A counter that went backwards between two samples (a NIC reset or a wrapped
// 32-bit counter, indistinguishable from two readings alone) contributes a
// zero delta for that interval rather than a spurious spike — a conservative
// choice appropriate for a downsampled daily rollup.
func LinkDailyUtil(samples []CounterSample, speedMbps int) (avg, max float64, ok bool) {
	if len(samples) < 2 || speedMbps <= 0 {
		return 0, 0, false
	}
	capacityBps := float64(speedMbps) * 1_000_000

	var sum float64
	var count int
	for i := 1; i < len(samples); i++ {
		prev, cur := samples[i-1], samples[i]
		dt := cur.At.Sub(prev.At).Seconds()
		if dt <= 0 {
			continue
		}
		rxBps := float64(delta(prev.RxBytes, cur.RxBytes)) * 8 / dt
		txBps := float64(delta(prev.TxBytes, cur.TxBytes)) * 8 / dt
		bps := rxBps
		if txBps > bps {
			bps = txBps
		}
		util := bps / capacityBps * 100
		sum += util
		count++
		if util > max {
			max = util
		}
	}
	if count == 0 {
		return 0, 0, false
	}
	return sum / float64(count), max, true
}

// delta returns the monotonic increase between two counter readings, treating
// a backwards step (reset/rollover) as zero — see LinkDailyUtil's doc comment.
func delta(prev, cur uint64) uint64 {
	if cur < prev {
		return 0
	}
	return cur - prev
}

// PoolUtil expresses allocated addresses as a percentage of a subnet's total.
// total <= 0 (unknown/degenerate subnet) reports 0 rather than dividing by
// zero.
func PoolUtil(allocated, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(allocated) / float64(total) * 100
}
