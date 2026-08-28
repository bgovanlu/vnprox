// SPDX-License-Identifier: Apache-2.0

package metrics

import "time"

// Counters is one point-in-time interface counter snapshot, mirroring
// host.IfaceStats (docs/data-model.md §2's metric_samples columns) but
// domain-owned here so this package does not need to import internal/host
// for its pure rate math.
type Counters struct {
	RxBytes uint64
	TxBytes uint64
	RxPkts  uint64
	TxPkts  uint64
	RxErrs  uint64
	TxErrs  uint64
	RxDrop  uint64
	TxDrop  uint64
}

// Rates is the computed per-second rate set for one entity between two
// samples (docs/features/monitoring.md §1: "live counters (rx/tx bps, pps,
// errors, drops) on selection"). Bps fields are bits/sec (the conventional
// unit for link-utilization math); all others are events/sec.
type Rates struct {
	RxBps        float64 `json:"rxBps"`
	TxBps        float64 `json:"txBps"`
	RxPps        float64 `json:"rxPps"`
	TxPps        float64 `json:"txPps"`
	RxErrsPerSec float64 `json:"rxErrsPerSec"`
	TxErrsPerSec float64 `json:"txErrsPerSec"`
	RxDropPerSec float64 `json:"rxDropPerSec"`
	TxDropPerSec float64 `json:"txDropPerSec"`
}

// maxUint32 is the rollover point for a 32-bit counter truncated into a
// wider field — see deltaCounter's doc comment.
const maxUint32 = uint64(^uint32(0))

// deltaCounter computes the monotonic increase between two counter
// readings, handling rollover.
//
// When cur >= prev this is the obvious cur-prev. When cur < prev the
// counter rolled over (or was reset — the two are indistinguishable from
// two readings alone, a documented limitation shared by every counter-rate
// tool: Cacti, MRTG, Prometheus' rate()/increase() all make the same
// assumption). Two rollover widths are supported since real NIC drivers
// export both: classic 32-bit counters (still common on older/virtual
// NICs, and what /proc/net/dev historically exposed) wrap at 2^32, while
// genuinely 64-bit counters wrap at 2^64. The heuristic: if both prev and
// cur fit in 32 bits, assume a 32-bit rollover (the far more likely case in
// practice, since a real 64-bit-wide byte counter wrapping requires
// exabytes of traffic between two 5s samples); otherwise fall back to a
// 64-bit rollover, which Go's unsigned subtraction computes for free (it is
// already modular arithmetic mod 2^64).
func deltaCounter(prev, cur uint64) uint64 {
	if cur >= prev {
		return cur - prev
	}
	if prev <= maxUint32 && cur <= maxUint32 {
		return (maxUint32 - prev) + cur + 1
	}
	return cur - prev // uint64 subtraction wraps mod 2^64
}

// ComputeRates derives per-second Rates from two Counters samples dt apart.
// dt <= 0 (out-of-order or duplicate samples) yields the zero Rates rather
// than dividing by zero or reporting a negative rate.
func ComputeRates(prev, cur Counters, dt time.Duration) Rates {
	if dt <= 0 {
		return Rates{}
	}
	secs := dt.Seconds()
	return Rates{
		RxBps:        float64(deltaCounter(prev.RxBytes, cur.RxBytes)) * 8 / secs,
		TxBps:        float64(deltaCounter(prev.TxBytes, cur.TxBytes)) * 8 / secs,
		RxPps:        float64(deltaCounter(prev.RxPkts, cur.RxPkts)) / secs,
		TxPps:        float64(deltaCounter(prev.TxPkts, cur.TxPkts)) / secs,
		RxErrsPerSec: float64(deltaCounter(prev.RxErrs, cur.RxErrs)) / secs,
		TxErrsPerSec: float64(deltaCounter(prev.TxErrs, cur.TxErrs)) / secs,
		RxDropPerSec: float64(deltaCounter(prev.RxDrop, cur.RxDrop)) / secs,
		TxDropPerSec: float64(deltaCounter(prev.TxDrop, cur.TxDrop)) / secs,
	}
}

// UtilizationPct expresses bps (bits/sec, one direction) as a percentage of
// speedMbps (megabits/sec, e.g. inventory.PhysNic.SpeedMbps). speedMbps <= 0
// (unknown link speed — common for a bond master, or a link that hasn't
// negotiated) reports 0 rather than an undefined/infinite percentage; the
// map traffic paint mode (docs/features/monitoring.md §1) should treat a
// zero-speed entity as "no heat data" rather than "idle".
func UtilizationPct(bps float64, speedMbps int) float64 {
	if speedMbps <= 0 || bps <= 0 {
		return 0
	}
	capacity := float64(speedMbps) * 1_000_000
	return bps / capacity * 100
}
