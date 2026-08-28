// SPDX-License-Identifier: Apache-2.0

// T-1706 AC4 (metrics half): the Grafana metrics panel renders against a
// fixture Prometheus scrape shaped exactly like T-1001's exporter output
// (internal/api/metrics_exporter.go), with no live Grafana or Prometheus.
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { MetricsPanel } from "./MetricsPanel";

// A trimmed but format-faithful vnprox exporter scrape body.
const scrape = `# HELP vnprox_iface_rx_bytes_total Cumulative bytes received, per interface.
# TYPE vnprox_iface_rx_bytes_total counter
vnprox_iface_rx_bytes_total{ref="bridge:pve1:vmbr0",node="pve1",kind="bridge"} 42
# HELP vnprox_findings_open Current open finding count from the unified findings stream, by severity.
# TYPE vnprox_findings_open gauge
vnprox_findings_open{severity="error"} 2
vnprox_findings_open{severity="warning"} 5
vnprox_findings_open{severity="info"} 0
# HELP vnprox_drift_open Current open cross-node drift finding count.
# TYPE vnprox_drift_open gauge
vnprox_drift_open 1
# HELP vnprox_changesets Current changeset count by status.
# TYPE vnprox_changesets gauge
vnprox_changesets{status="draft"} 3
vnprox_changesets{status="committed"} 7
# HELP vnprox_build_info vnproxd build info; the sample value is always 1.
# TYPE vnprox_build_info gauge
vnprox_build_info{version="3.0.0"} 1
`;

describe("MetricsPanel", () => {
  it("renders the vnprox metric families from a fixture scrape (AC4)", () => {
    render(<MetricsPanel scrape={scrape} />);
    expect(screen.getByTestId("metrics-panel")).toBeInTheDocument();
    expect(screen.getByText("vnprox_findings_open")).toBeInTheDocument();
    expect(screen.getByText("vnprox_drift_open")).toBeInTheDocument();
    expect(screen.getByText("vnprox_changesets")).toBeInTheDocument();
    // A specific sample value from the changesets family is rendered.
    expect(screen.getByText("severity=error")).toBeInTheDocument();
    expect(screen.getByText("status=committed")).toBeInTheDocument();
    expect(screen.getByText("7")).toBeInTheDocument();
  });

  it("shows an empty state when the scrape has no vnprox metrics", () => {
    render(<MetricsPanel scrape={"# HELP other_metric x\n# TYPE other_metric gauge\nother_metric 1\n"} />);
    expect(screen.getByTestId("metrics-panel-empty")).toBeInTheDocument();
  });
});
