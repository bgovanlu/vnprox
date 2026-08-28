// SPDX-License-Identifier: Apache-2.0

// Package baseline learns per-guest/per-segment traffic baselines from the
// retained flow window (internal/flow.Record, sourced from flow_samples) and
// detects statistically significant deviations from a learned baseline
// (T-1601).
//
// This package is emphatically NOT a black-box/ML IDS, not packet inspection,
// and not a SIEM ingest path — the explicit non-goals T-1601's card names.
// Every deviation it reports is EXPLAINABLE: an Anomaly names the baseline it
// deviated from (BaselineWindow, BaselineValue) and the deviation's magnitude
// (ObservedValue, DeviationFactor), never a bare "this looks weird". The
// findings producer (internal/findings, source "baseline") renders each
// Anomaly into a plain-English detail string.
//
// Two operations, both pure functions over flow records:
//
//   - Learn(records, ref, window) Profile — computes a statistical summary
//     for one inventory Ref over a learning window: top talkers, the observed
//     service-port set, the observed peer-subnet set, and a per-hour-of-day
//     byte-volume histogram (mean/stddev of the per-wall-clock-hour byte
//     totals). A Profile is app-owned SUMMARY data that deliberately outlives
//     the raw flows it was learned from (persisted in baseline_profiles,
//     docs/data-model.md §2) — never a shadow copy of raw flow rows.
//
//   - Detect(profile, recent, cfg) []Anomaly — replays a recent slice of
//     flows against a learned Profile and raises three deviation classes:
//     new_port (a service port never seen while learning), volume_spike (a
//     wall-clock hour's byte volume >= a configurable multiple of that
//     hour-of-day's baseline mean+stddev), and new_subnet (a peer subnet
//     never seen while learning).
//
// A baseline never flags its own training data: feeding a Profile's own
// learning-window flows back into Detect raises nothing (the covering
// property T-1601's AC1 requires), and a Ref with no learned baseline
// produces no anomalies at all (cold-start is silent, AC5).
package baseline
