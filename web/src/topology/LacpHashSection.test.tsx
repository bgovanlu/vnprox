// SPDX-License-Identifier: Apache-2.0

// T-4110's own component test: lab-simulated flow/metric fixtures (T-4110
// is hardware-flagged — see planning/reports/needs-hardware-validation.md's
// T-4110 entry), never captured from real traffic, clearly labeled as
// such per the task card's requirement. Mocks "../api/flows"/"../api/metrics"
// the same way InspectorPanel.metrics.test.tsx does, so this file exercises
// LacpHashSection directly rather than through the whole InspectorPanel.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { FlowRecord, FlowsPage, LiveMetric } from "../api/types";
import type { WsClient } from "../api/ws";
import { LacpHashSection } from "./LacpHashSection";

let mockFlowsPage: FlowsPage = { items: [] };
// Keyed by ref, mirroring GET /metrics/live's real per-ref response shape —
// LacpHashSection queries each slave's own physnic ref directly (not the
// bond's ref), so the fixture must too.
let mockLiveMetrics: LiveMetric[] = [];

vi.mock("../api/flows", () => ({
  fetchFlows: vi.fn(() => Promise.resolve(mockFlowsPage)),
}));

vi.mock("../api/metrics", () => ({
  fetchMetricsLive: vi.fn(() => Promise.resolve(mockLiveMetrics)),
  fetchMetricsHistory: vi.fn(() => Promise.resolve([])),
}));

beforeEach(() => {
  vi.clearAllMocks();
  mockFlowsPage = { items: [] };
  mockLiveMetrics = [];
});

const noopWsClient: WsClient = {
  subscribe: () => () => undefined,
  status: () => "open",
  close: () => undefined,
};

function flow(over: Partial<FlowRecord>): FlowRecord {
  return {
    at: 1_700_000_000,
    node: "pve1",
    srcIp: "10.0.0.1",
    dstIp: "10.0.0.2",
    proto: 6,
    bytes: 100,
    packets: 1,
    source: "fixture",
    ...over,
  };
}

const twoSlaveFields = {
  XmitHashPolicy: "layer3+4",
  SlaveDetail: [
    { Name: "eno1", MIIStatus: "up" },
    { Name: "eno2", MIIStatus: "up" },
  ],
};

function renderSection(fields: Record<string, unknown>, node = "pve1"): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <LacpHashSection node={node} fields={fields} wsClient={noopWsClient} />
    </QueryClientProvider>,
  );
}

describe("LacpHashSection", () => {
  it("renders nothing when the bond has no slave detail at all (BondLacpSection already covers that empty state)", () => {
    const { container } = render(
      <QueryClientProvider client={new QueryClient()}>
        <LacpHashSection node="pve1" fields={{}} wsClient={noopWsClient} />
      </QueryClientProvider>,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("explains that no xmit_hash_policy is configured", async () => {
    mockFlowsPage = { items: [] };
    renderSection({ SlaveDetail: twoSlaveFields.SlaveDetail });
    expect(await screen.findByText(/No xmit_hash_policy configured/)).toBeInTheDocument();
  });

  it("explains the MAC-address gap for layer2", async () => {
    mockFlowsPage = { items: [] };
    renderSection({ ...twoSlaveFields, XmitHashPolicy: "layer2" });
    expect(await screen.findByText(/Cannot predict slave selection for/)).toBeInTheDocument();
    expect(screen.getByText(/never carry/)).toBeInTheDocument();
  });

  it("reports no flow data yet for this node distinctly from a genuine empty answer", async () => {
    mockFlowsPage = { items: [] };
    renderSection(twoSlaveFields);
    expect(await screen.findByText(/No flow records observed for this bond's node yet/)).toBeInTheDocument();
  });

  it("renders predicted vs actual once flows and a live sample are both available, flagging a lopsided divergence", async () => {
    mockFlowsPage = {
      items: [
        flow({ srcIp: "10.0.0.1", dstIp: "10.0.0.2", srcPort: 1000, dstPort: 443 }),
        flow({ srcIp: "10.0.0.1", dstIp: "10.0.0.2", srcPort: 1000, dstPort: 443 }),
        flow({ srcIp: "10.0.0.1", dstIp: "10.0.0.2", srcPort: 1000, dstPort: 443 }),
      ],
    };
    const rates = { rxBps: 900_000, txBps: 0, rxPps: 0, txPps: 0, rxErrsPerSec: 0, txErrsPerSec: 0, rxDropPerSec: 0, txDropPerSec: 0 };
    mockLiveMetrics = [
      { ref: "physnic:pve1:eno1", at: 30, rates },
      { ref: "physnic:pve1:eno2", at: 30, rates },
    ];
    renderSection(twoSlaveFields);

    await waitFor(() => {
      expect(screen.getByText("eno1")).toBeInTheDocument();
      expect(screen.getByText("eno2")).toBeInTheDocument();
    });
    // All three identical flows hash to one slave (predicted 100%/0% split);
    // the live sample is an even 900_000/900_000 split — that mismatch must
    // render as a flagged divergence on at least one row.
    expect(await screen.findAllByText("⚠", { exact: false })).not.toHaveLength(0);
  });

  it("does not fetch flows at all for a MAC-dependent policy (nothing predictable to fetch for)", async () => {
    const flows = await import("../api/flows");
    renderSection({ ...twoSlaveFields, XmitHashPolicy: "layer2+3" });
    await screen.findByText(/Cannot predict slave selection for/);
    expect(flows.fetchFlows).not.toHaveBeenCalled();
  });
});
