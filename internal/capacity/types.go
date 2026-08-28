// SPDX-License-Identifier: Apache-2.0

package capacity

import "time"

// Kind names which utilization series an Aggregate summarizes. It mirrors
// store.CapacityKind{Link,IPAMPool} but is declared here as this package's own
// domain type so callers address it without importing internal/store.
type Kind string

const (
	// KindLink is a link/interface's utilization, rolled up from that day's
	// metric_samples counter deltas against the link speed.
	KindLink Kind = "link"
	// KindIPAMPool is a subnet's address-pool consumption, rolled up from
	// internal/ipam's live allocation count over the subnet's total.
	KindIPAMPool Kind = "ipam_pool"
)

// Check names for the two findings this arc's capacity producer raises
// (source "capacity"). Kept here so both the producer adapter and its tests
// reference one definition.
const (
	CheckLinkForecast = "capacity_link_forecast"
	CheckIPAMForecast = "capacity_ipam_forecast"
)

// CapacityThresholdPct is what "full" means for a forecast: 100% utilization.
// A forecast projects when the fitted trend line reaches this value.
const CapacityThresholdPct = 100.0

// DefaultForecastHorizonDays is the default look-ahead window ([capacity]
// forecast_horizon_days): a forecast only fires when the projected crossing
// falls within this many days, so a very slow trend that won't cross for years
// never raises a finding.
const DefaultForecastHorizonDays = 90

// Aggregate is one daily utilization rollup — the domain view of one
// store.CapacityAggregate row. AvgUtil/MaxUtil are percentages (0-100).
// BucketAt is the start-of-day (UTC) instant the rollup covers.
type Aggregate struct {
	BucketAt time.Time
	Ref      string
	Kind     Kind
	AvgUtil  float64
	MaxUtil  float64
}

// Projection is Forecast's result: CrossesAt is the projected instant the
// trend reaches capacity, or nil when the trend is flat/decreasing or the
// crossing lies beyond the requested horizon (no false-positive forecast on
// stable utilization). Confidence is the linear fit's R² in [0,1].
type Projection struct {
	CrossesAt  *time.Time
	Confidence float64
}

// ForecastFinding is Analyze's per-ref result for a ref projected to cross
// capacity within the horizon. It is the pure, findings-package-free carrier
// the cmd/vnproxd adapter maps into a findings.Finding (source "capacity"),
// keeping this package independent of internal/findings.
type ForecastFinding struct {
	CrossesAt   time.Time
	Ref         string
	Kind        Kind
	Check       string
	Detail      string
	HorizonDays int
	Confidence  float64
}
