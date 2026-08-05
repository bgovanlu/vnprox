// T-1903: the daemon self-observability Grafana panel renders against a
// fixture Prometheus scrape shaped exactly like the exporter's real output
// (internal/api/metrics_exporter.go + internal/metrics/self.go) — no live
// Grafana or Prometheus. The fixture below is trimmed but format-faithful,
// captured from a real GET /metrics scrape of a router wired with a
// populated internal/metrics.Registry (see this task's report), mirroring
// MetricsPanel.test.tsx's own convention.
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { DaemonMetricsPanel } from "./DaemonMetricsPanel";

const scrape = `# HELP vnprox_http_requests_total Total HTTP requests handled, by route pattern, method, and status class.
# TYPE vnprox_http_requests_total counter
vnprox_http_requests_total{route="/api/v1/changesets/{id}",method="GET",status_class="2xx"} 1
vnprox_http_requests_total{route="/api/v1/changesets/{id}",method="GET",status_class="5xx"} 1
# HELP vnprox_http_request_duration_seconds HTTP request duration in seconds, by route pattern and method.
# TYPE vnprox_http_request_duration_seconds histogram
vnprox_http_request_duration_seconds_bucket{route="/api/v1/changesets/{id}",method="GET",le="0.025"} 1
vnprox_http_request_duration_seconds_bucket{route="/api/v1/changesets/{id}",method="GET",le="0.05"} 2
vnprox_http_request_duration_seconds_bucket{route="/api/v1/changesets/{id}",method="GET",le="+Inf"} 2
vnprox_http_request_duration_seconds_sum{route="/api/v1/changesets/{id}",method="GET"} 0.052
vnprox_http_request_duration_seconds_count{route="/api/v1/changesets/{id}",method="GET"} 2
# HELP vnprox_collector_polls_total Total collector poll attempts, by source, node, and outcome.
# TYPE vnprox_collector_polls_total counter
vnprox_collector_polls_total{source="host",node="pve1",outcome="success"} 1
vnprox_collector_polls_total{source="pve",node="",outcome="success"} 1
# HELP vnprox_collector_consecutive_failures Current consecutive poll failure count, by source and node.
# TYPE vnprox_collector_consecutive_failures gauge
vnprox_collector_consecutive_failures{source="pve",node=""} 0
vnprox_collector_consecutive_failures{source="host",node="pve1"} 3
# HELP vnprox_change_outcomes_total Total change-engine operations, by op and outcome.
# TYPE vnprox_change_outcomes_total counter
vnprox_change_outcomes_total{op="apply",outcome="success"} 1
vnprox_change_outcomes_total{op="rollback",outcome="success"} 1
# HELP vnprox_change_awaiting_confirm_seconds Time a changeset spent in awaiting_confirm before leaving it, by the status it left to.
# TYPE vnprox_change_awaiting_confirm_seconds histogram
vnprox_change_awaiting_confirm_seconds_bucket{outcome="committed",le="30"} 0
vnprox_change_awaiting_confirm_seconds_bucket{outcome="committed",le="60"} 1
vnprox_change_awaiting_confirm_seconds_bucket{outcome="committed",le="+Inf"} 1
vnprox_change_awaiting_confirm_seconds_sum{outcome="committed"} 45
vnprox_change_awaiting_confirm_seconds_count{outcome="committed"} 1
# HELP vnprox_store_query_duration_seconds SQLite store query/exec duration in seconds, by statement kind.
# TYPE vnprox_store_query_duration_seconds histogram
vnprox_store_query_duration_seconds_bucket{op="select",le="0.001"} 1
vnprox_store_query_duration_seconds_bucket{op="select",le="+Inf"} 1
vnprox_store_query_duration_seconds_sum{op="select"} 0.0008
vnprox_store_query_duration_seconds_count{op="select"} 1
# HELP vnprox_peer_calls_total Total peer-API RPCs issued, by node, endpoint, and outcome.
# TYPE vnprox_peer_calls_total counter
vnprox_peer_calls_total{node="pve2",endpoint="/api/peer/host/stats",outcome="ok"} 1
# HELP vnprox_peer_call_duration_seconds Peer-API RPC duration in seconds, by node and endpoint.
# TYPE vnprox_peer_call_duration_seconds histogram
vnprox_peer_call_duration_seconds_bucket{node="pve2",endpoint="/api/peer/host/stats",le="0.01"} 1
vnprox_peer_call_duration_seconds_bucket{node="pve2",endpoint="/api/peer/host/stats",le="+Inf"} 1
vnprox_peer_call_duration_seconds_sum{node="pve2",endpoint="/api/peer/host/stats"} 0.006
vnprox_peer_call_duration_seconds_count{node="pve2",endpoint="/api/peer/host/stats"} 1
# HELP vnprox_store_size_bytes Current SQLite store size on disk, in bytes (main file plus WAL/SHM sidecars).
# TYPE vnprox_store_size_bytes gauge
vnprox_store_size_bytes 5242880
# HELP vnprox_store_schema_version Currently-applied SQLite schema version.
# TYPE vnprox_store_schema_version gauge
vnprox_store_schema_version 34
# HELP vnprox_store_schema_migration_pending Whether this binary's embedded schema is newer than the store's applied version (1) or not (0); always 0 in steady state, since Open() migrates to latest before serving.
# TYPE vnprox_store_schema_migration_pending gauge
vnprox_store_schema_migration_pending 0
# HELP vnprox_ws_connections Current live WebSocket client connections on /api/ws.
# TYPE vnprox_ws_connections gauge
vnprox_ws_connections 2
`;

describe("DaemonMetricsPanel", () => {
  it("renders the daemon self-observability families from a fixture scrape", () => {
    render(<DaemonMetricsPanel scrape={scrape} />);
    expect(screen.getByTestId("daemon-metrics-panel")).toBeInTheDocument();

    // Flat counter/gauge families.
    expect(screen.getByText("vnprox_http_requests_total")).toBeInTheDocument();
    expect(screen.getByText("vnprox_collector_consecutive_failures")).toBeInTheDocument();
    expect(screen.getByText("source=host, node=pve1")).toBeInTheDocument();
    expect(screen.getByText("3")).toBeInTheDocument();
    expect(screen.getByText("vnprox_change_outcomes_total")).toBeInTheDocument();
    expect(screen.getByText("op=rollback, outcome=success")).toBeInTheDocument();
    expect(screen.getByText("vnprox_ws_connections")).toBeInTheDocument();
    expect(screen.getByText("2")).toBeInTheDocument();
    expect(screen.getByText("vnprox_store_size_bytes")).toBeInTheDocument();
    expect(screen.getByText("5242880")).toBeInTheDocument();

    // Histogram families reduced to count/avg, not one row per bucket.
    expect(screen.getByText("vnprox_http_request_duration_seconds")).toBeInTheDocument();
    expect(screen.getByText("2 calls, avg 0.026s")).toBeInTheDocument();
    expect(screen.getByText("vnprox_change_awaiting_confirm_seconds")).toBeInTheDocument();
    expect(screen.getByText("1 calls, avg 45.000s")).toBeInTheDocument();
    // No raw bucket rows leaked into the table (this panel is a summary,
    // not a bucket dump).
    expect(screen.queryByText(/le=/)).not.toBeInTheDocument();
  });

  it("shows an empty state when the scrape has no vnprox daemon metrics", () => {
    render(<DaemonMetricsPanel scrape={"# HELP vnprox_findings_open x\n# TYPE vnprox_findings_open gauge\nvnprox_findings_open{severity=\"error\"} 0\n"} />);
    expect(screen.getByTestId("daemon-metrics-panel-empty")).toBeInTheDocument();
  });
});
