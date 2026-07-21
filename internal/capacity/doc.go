// Package capacity implements T-1606's capacity forecasting: it trends
// link/segment utilization and IPAM pool consumption against a downsampled
// long-term history and projects when a growth curve will cross capacity
// within a configured horizon ("vmbr1 uplink full in ~5 weeks").
//
// This package is the home of the network-intelligence arc's ONE deliberate
// retention extension. Its daily rollups (store.CapacityAggregate, migration
// 0026) outlive the raw metric_samples/flow_samples rings they are summarized
// from — but they are still explicitly bounded ([capacity]
// aggregate_retention_days, default 400) and pruned on the same tick-based
// cadence every other bounded table uses. It is a downsampled aggregate, not
// a raw-data warehouse; see docs/data-model.md's capacity_aggregates entry.
//
// The package is intentionally dependency-light: it holds the pure forecast
// math (Forecast/Analyze), the daily-rollup job orchestration (RollupJob over
// the BucketSource/Sink seams), and the small utilization helpers
// (LinkDailyUtil/PoolUtil). The concrete data sources — reading
// metric_samples for link utilization and internal/ipam for pool counts — and
// the store-backed Sink live at the cmd/vnproxd composition root, so this
// package imports neither internal/store nor internal/ipam and stays unit
// testable against fakes.
package capacity
