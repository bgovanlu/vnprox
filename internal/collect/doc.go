// Package collect implements vnprox's poll loops: the PVE poller and the
// local-host poller that keep internal/inventory's Graph current
// (docs/architecture.md §3 "Data flow — read path", docs/deployment.md's
// documented [collect] intervals).
//
// A Collector wraps a *pve.Client, a host.Reader, and an *inventory.Graph.
// It exposes three run-group-compatible loops (RunPVELoop, RunHostLoop,
// RunLLDPLoop — see cmd/vnproxd's runGroup) plus an on-demand RefreshNow for
// targeted post-apply refreshes, and a Status snapshot for staleness
// reporting (surfaced by /api/v1/health via a small adapter in
// cmd/vnproxd).
//
// Poll steps call the internal/pve client and host.Reader, hand results to
// internal/inventory/ingest.go's From* adapters, and reconcile them into the
// graph with Graph.ApplyPoll. A single poll cycle (whether a scheduled tick
// or a RefreshNow call) issues several ApplyPoll calls internally (one per
// PVE source: cluster status, per-node network, guests, SDN, firewall) but
// is always reported to callers as exactly one merged Delta batch — see
// diffSnapshots in delta.go, which diffs a before/after Graph.Snapshot()
// pair directly rather than composing the individual ApplyPoll return
// values, so downstream consumers (a future WS hub, T-106) see one
// "topology changed" event per cycle, not one per sub-poll-step.
package collect
