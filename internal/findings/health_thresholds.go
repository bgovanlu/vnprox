// SPDX-License-Identifier: Apache-2.0

// health_thresholds.go implements docs/features/monitoring.md §5's
// "interface error/drop rate thresholds" check, reading straight from
// T-601's internal/metrics.Sampler.Live — per planning/reports/T-601.md's
// note to this task: "Sampler.Live() already exposes everything the
// error/drop-threshold ... checks need — no new sampler work required, just
// read internal/metrics types directly." No new collection: this is pure
// consumption of already-sampled rates.

package findings

import (
	"fmt"
	"sort"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/metrics"
)

const CheckErrorDropRate = "error_drop_rate"

const errorDropRateDocsLink = "docs/features/monitoring.md#5-health-checks"

// MetricsProvider is the subset of *metrics.Sampler Engine needs.
type MetricsProvider interface {
	Live(refs []string) []metrics.LiveMetric
}

// HealthThresholds configures the numeric error/drop-rate thresholds and
// the hysteresis (rise/fall consecutive-cycle counts) every stateful health
// check uses. docs/features/monitoring.md §5 names the check but not a
// specific number — these defaults are this task's own choice, documented
// here and overridable via Config.Thresholds so an operator (or a future
// task) can tune them without code changes.
type HealthThresholds struct {
	// ErrorRatePerSec/DropRatePerSec are combined (rx+tx) events/sec
	// thresholds. Defaults: 1 error/sec, 1 drop/sec — low enough to catch a
	// genuinely unhealthy link, high enough that ordinary bursty traffic
	// with the occasional single dropped frame doesn't trip it.
	ErrorRatePerSec float64
	DropRatePerSec  float64
	// RiseCycles/FallCycles: consecutive Engine cycles a ref must be over
	// (under) threshold before the finding fires (clears) — AC3's
	// hysteresis. Defaults: 3 rise / 2 fall.
	RiseCycles int
	FallCycles int
	// LatRttWarnMs/LatLossWarnPct (T-1303) are the latency-mesh thresholds
	// health_latmesh.go's path_latency_degraded/path_loss checks compare a
	// link's *rolling* (not single-sample) RTT/loss against — see that
	// file's doc comment for why the rolling figure, not the raw per-tick
	// reading, is what's compared. Defaults: 80ms, 2% — loose enough that
	// ordinary jitter on a healthy LAN/corosync ring never trips either
	// one, tight enough to catch a real degrading path before an operator
	// notices from application-level symptoms.
	LatRttWarnMs   float64
	LatLossWarnPct float64
	// WanLossWarnPct (T-1405) is the threshold health_wan.go's wan_degraded
	// check compares a WAN link's *rolling* loss% against — deliberately
	// looser than LatLossWarnPct's 2% LAN threshold, since an ordinary WAN
	// path's baseline jitter/loss to an external reference target is
	// inherently higher than a LAN/corosync link's. Default: 20%.
	WanLossWarnPct float64
	// StoreCapacityWarnBytes (T-1905) is the threshold
	// health_storecapacity.go's store_near_capacity check compares the app
	// store's on-disk size against — [retention] store_warn_bytes, mirrored
	// here the same way wanLossWarnPct is threaded into this struct by
	// cmd/vnproxd rather than read from config directly (internal/findings
	// never imports internal/config). See config.DefaultStoreWarnBytes for
	// the argued default (4 GiB).
	StoreCapacityWarnBytes int64
}

// DefaultThresholds is applied by Engine's constructor when Config.Thresholds
// is the zero value.
var DefaultThresholds = HealthThresholds{
	ErrorRatePerSec:        1,
	DropRatePerSec:         1,
	RiseCycles:             3,
	FallCycles:             2,
	LatRttWarnMs:           80,
	LatLossWarnPct:         2,
	WanLossWarnPct:         20,
	StoreCapacityWarnBytes: 4 << 30, // 4 GiB — mirrors config.DefaultStoreWarnBytes
}

// sampleableKinds is the same Kind set internal/metrics samples (see its
// refMetasFromLinks): physical NICs, bonds, bridges, VLAN sub-interfaces.
var sampleableKinds = map[inventory.Kind]bool{
	inventory.KindPhysNic:   true,
	inventory.KindBond:      true,
	inventory.KindOVSBond:   true,
	inventory.KindBridge:    true,
	inventory.KindOVSBridge: true,
	inventory.KindVlan:      true,
}

// sampleableRefs lists every ref in snap that internal/metrics could ever
// have live data for.
func sampleableRefs(snap inventory.Snapshot) []string {
	var out []string
	for _, e := range snap.All() {
		ref := e.GetRef()
		if sampleableKinds[ref.Kind] {
			out = append(out, ref.String())
		}
	}
	sort.Strings(out)
	return out
}

// checkErrorDropRate is the CheckErrorDropRate family.
func checkErrorDropRate(snap inventory.Snapshot, mp MetricsProvider, db *debouncer, th HealthThresholds) []Finding {
	if mp == nil {
		return nil
	}
	refs := sampleableRefs(snap)
	if len(refs) == 0 {
		return nil
	}
	live := mp.Live(refs)

	var out []Finding
	liveKeys := map[string]bool{}
	for _, lm := range live {
		liveKeys[lm.Ref] = true
		errRate := lm.Rates.RxErrsPerSec + lm.Rates.TxErrsPerSec
		dropRate := lm.Rates.RxDropPerSec + lm.Rates.TxDropPerSec
		breach := errRate > th.ErrorRatePerSec || dropRate > th.DropRatePerSec

		active := db.Evaluate(lm.Ref, breach, th.RiseCycles, th.FallCycles)
		if !active {
			continue
		}

		node := ""
		if parsed, err := inventory.ParseRef(lm.Ref); err == nil {
			node = parsed.Node
		}
		severity := SeverityWarning
		if errRate > th.ErrorRatePerSec {
			severity = SeverityError
		}
		detail := fmt.Sprintf("%s is sustaining %.2f errors/sec and %.2f drops/sec (thresholds: %.2f/%.2f) — check the physical link, cabling, or switch port",
			lm.Ref, errRate, dropRate, th.ErrorRatePerSec, th.DropRatePerSec)
		var nodes []string
		if node != "" {
			nodes = []string{node}
		}
		f := newHealthFinding(CheckErrorDropRate, severity, detail, nodes, []string{lm.Ref})
		f.DocsLink = errorDropRateDocsLink
		out = append(out, f)
	}

	db.Prune(liveKeys)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
