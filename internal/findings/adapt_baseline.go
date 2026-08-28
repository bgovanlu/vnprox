// SPDX-License-Identifier: Apache-2.0

// adapt_baseline.go implements T-1601's flow-baseline anomaly findings
// (source "baseline", its own top-level source — see SourceBaseline's doc
// comment): a learned per-guest/per-segment traffic baseline
// (internal/baseline.Profile) deviated from by recent flows. Fed by a
// BaselineProvider seam (cmd/vnproxd's baselineAnomalyAdapter, which loads
// stored profiles + re-queries a bounded recent flow window and runs
// internal/baseline.Detect fresh each findings cycle) — the same "caller
// decides the recency window/fan-out" shape FlowProvider already establishes,
// never a second detector. Every finding is EXPLAINABLE: its detail names the
// baseline value, the observed value, and the deviation, never a bare
// "anomalous" string (the honesty contract T-1601's card and
// docs/features/monitoring.md §5 require).

package findings

import (
	"fmt"
	"sort"
	"time"

	"github.com/bgovanlu/vnprox/internal/baseline"
)

// Baseline anomaly check names — the `check` field on a source "baseline"
// finding, matching internal/baseline.AnomalyClass one-for-one.
const (
	CheckNewPort     = "new_port"
	CheckVolumeSpike = "volume_spike"
	CheckNewSubnet   = "new_subnet"
)

const baselineDocsLink = "docs/features/monitoring.md#5-health-checks"

// baselineFindingIDPrefix is the fixed prefix of every baseline finding's id
// ("baseline:<check>|<ref>|<subject>") — content-derived and stable, so
// re-detecting the same deviation reproduces a byte-identical id (the
// property Engine's transition/notification tracking depends on).
const baselineFindingIDPrefix = "baseline:"

// baselineRiseCycles/baselineFallCycles: the standard 2-cycle each-way
// hysteresis window every other continuously-recomputed producer uses (the
// same window corosync_link_degraded/service_traffic_on_wrong_network use) —
// an anomaly must persist two consecutive findings cycles before it fires, so
// a single transient recent-window blip never raises a finding on its own.
const (
	baselineRiseCycles = 2
	baselineFallCycles = 2
)

// BaselineProvider is the findings engine's seam onto internal/baseline: the
// anomalies detected this cycle across every Ref with a learned baseline. The
// caller (cmd/vnproxd) owns loading stored profiles and re-querying the
// recent flow window before running internal/baseline.Detect — internal/
// findings need not import internal/store. Nil skips the producer entirely,
// same nil-safe degradation as every other optional producer input here.
type BaselineProvider interface {
	RecentAnomalies() ([]baseline.Anomaly, error)
}

// checkBaselineAnomalies converts prov's current anomaly batch into unified
// findings, debounced per anomaly-id exactly like
// checkServiceTrafficOnWrongNetwork debounces per (serviceClass, vlan) pair.
// An anomaly whose triggering condition vanishes from prov's batch is pruned
// (its condition is gone); a persisting anomaly fires once it has been present
// baselineRiseCycles consecutive cycles.
func checkBaselineAnomalies(prov BaselineProvider, db *debouncer) []Finding {
	if prov == nil {
		return nil
	}
	anomalies, err := prov.RecentAnomalies()
	if err != nil {
		return nil
	}

	byID := make(map[string]baseline.Anomaly, len(anomalies))
	for _, a := range anomalies {
		byID[BaselineFindingID(a)] = a
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var out []Finding
	live := make(map[string]bool, len(ids))
	for _, id := range ids {
		a := byID[id]
		live[id] = true
		if !db.Evaluate(id, true, baselineRiseCycles, baselineFallCycles) {
			continue
		}
		out = append(out, Finding{
			ID:       id,
			Source:   SourceBaseline,
			Check:    string(a.Class),
			Severity: SeverityWarning,
			Detail:   baselineDetail(a),
			Refs:     sortedUnique([]string{a.Ref}),
			DocsLink: baselineDocsLink,
		})
	}

	db.Prune(live)
	sortFindings(out)
	return out
}

// BaselineFindingID is the stable, content-derived id for a's finding.
// Exported so cmd/vnproxd's T-4101 anomaly-triggered-capture wiring can
// correlate a newly-appeared source="baseline" Finding back to the
// internal/baseline.Anomaly that produced it, using the exact same id this
// package computes internally — never a second, drift-prone re-derivation.
func BaselineFindingID(a baseline.Anomaly) string {
	return baselineFindingIDPrefix + string(a.Class) + "|" + a.Ref + "|" + a.Subject
}

// baselineDetail renders a plain-English explanation of a — always naming the
// baseline it deviated from and the deviation's magnitude, never a bare
// "anomalous" string.
func baselineDetail(a baseline.Anomaly) string {
	win := formatBaselineWindow(a.BaselineWindow)
	switch a.Class {
	case baseline.ClassNewPort:
		return fmt.Sprintf(
			"%s used service port %s, never observed in the baseline window (%s) — seen %d time(s) now vs 0 in the baseline",
			a.Ref, a.Subject, win, int64(a.ObservedValue))
	case baseline.ClassNewSubnet:
		return fmt.Sprintf(
			"%s communicated with subnet %s, never observed in the baseline window (%s) — seen %d time(s) now vs 0 in the baseline",
			a.Ref, a.Subject, win, int64(a.ObservedValue))
	case baseline.ClassVolumeSpike:
		return fmt.Sprintf(
			"%s transferred %d bytes in hour %s — %.1f× its baseline of %d bytes (mean+stddev) for that hour-of-day over the baseline window (%s)",
			a.Ref, int64(a.ObservedValue), a.Subject, a.DeviationFactor, int64(a.BaselineValue), win)
	default:
		return fmt.Sprintf("%s: baseline anomaly (%s) on %s over the baseline window (%s)",
			a.Ref, a.Class, a.Subject, win)
	}
}

// formatBaselineWindow renders a learning window as "YYYY-MM-DD–YYYY-MM-DD"
// (UTC), or "unknown" when unset.
func formatBaselineWindow(w baseline.Window) string {
	if w.Start == 0 && w.End == 0 {
		return "unknown"
	}
	const day = "2006-01-02"
	return time.Unix(w.Start, 0).UTC().Format(day) + "–" + time.Unix(w.End, 0).UTC().Format(day)
}
