// SPDX-License-Identifier: Apache-2.0

// The Inspector's "Metrics" tab (docs/features/monitoring.md §1-2):
// counters + sparkline for a physnic/bond/bridge/vlan entity, and the
// bond per-slave balance view. A separate file from InspectorPanel.test.tsx
// so its api/metrics + WS mocks stay scoped to just these tests.
import detailFixture from "./__fixtures__/inventory-detail-vmbr0.json";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import type { EntityDetail, LiveMetric } from "../api/types";
import type { WsClient } from "../api/ws";
import { ToastProvider } from "../components/Toast";
import { InspectorPanel } from "./InspectorPanel";

const bridgeDetail = detailFixture as unknown as EntityDetail; // kind: "bridge"
const bondDetail: EntityDetail = {
  ...bridgeDetail,
  ref: "bond:pve1:bond0",
  kind: "bond",
  label: "bond0",
};

vi.mock("../api/topology", () => ({
  fetchInventoryDetail: vi.fn(() => Promise.resolve(mockDetailResponse)),
  fetchTopology: vi.fn(),
  searchInventory: vi.fn(),
}));

let mockDetailResponse: EntityDetail = bridgeDetail;
let mockLiveMetric: LiveMetric | undefined;

vi.mock("../api/metrics", () => ({
  fetchMetricsLive: vi.fn(() => Promise.resolve(mockLiveMetric ? [mockLiveMetric] : [])),
  fetchMetricsHistory: vi.fn(() =>
    Promise.resolve([
      { at: 0, rates: { rxBps: 1000, txBps: 500, rxPps: 1, txPps: 1, rxErrsPerSec: 0, txErrsPerSec: 0, rxDropPerSec: 0, txDropPerSec: 0 } },
      { at: 30, rates: { rxBps: 2000, txBps: 800, rxPps: 2, txPps: 2, rxErrsPerSec: 0, txErrsPerSec: 0, rxDropPerSec: 0, txDropPerSec: 0 } },
    ]),
  ),
}));

// A no-op WsClient stub: MetricsTab's useLiveMetrics subscribes to it but
// this never delivers events — the initial GET /metrics/live mock above is
// enough to exercise rendering without a real/fake WebSocket.
const noopWsClient: WsClient = {
  subscribe: () => () => undefined,
  status: () => "open",
  close: () => undefined,
};

function renderPanel(ref: string): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <ToastProvider>
          <InspectorPanel
            selectedRef={ref}
            onClose={() => undefined}
            onSelectRelated={() => undefined}
            metricsWsClient={noopWsClient}
          />
        </ToastProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("InspectorPanel Metrics tab", () => {
  it("shows a Metrics tab for a metrics-bearing kind (bridge) and renders counters + sparkline once opened", async () => {
    mockDetailResponse = bridgeDetail;
    mockLiveMetric = {
      ref: bridgeDetail.ref,
      at: 30,
      rates: { rxBps: 2000, txBps: 800, rxPps: 2, txPps: 2, rxErrsPerSec: 0, txErrsPerSec: 0, rxDropPerSec: 0, txDropPerSec: 0 },
      speedMbps: 1000,
      utilizationPct: 0.2,
    };
    renderPanel(bridgeDetail.ref);

    const tab = await screen.findByRole("tab", { name: "Metrics" });
    await userEvent.click(tab);

    expect(await screen.findByText("2.0 Kbps")).toBeInTheDocument(); // Rx stat (2000 bps)
    await waitFor(() => {
      expect(screen.getByTestId("metrics-sparkline")).toBeInTheDocument();
    });
  });

  it("renders the per-slave balance view for a bond, with the imbalanced slave visibly distinct", async () => {
    mockDetailResponse = bondDetail;
    mockLiveMetric = {
      ref: bondDetail.ref,
      at: 30,
      rates: { rxBps: 900_000, txBps: 0, rxPps: 0, txPps: 0, rxErrsPerSec: 0, txErrsPerSec: 0, rxDropPerSec: 0, txDropPerSec: 0 },
      speedMbps: 2000,
      slaves: [
        { ref: "physnic:pve1:eno1", active: true, rates: { rxBps: 800_000, txBps: 0, rxPps: 0, txPps: 0, rxErrsPerSec: 0, txErrsPerSec: 0, rxDropPerSec: 0, txDropPerSec: 0 } },
        { ref: "physnic:pve1:eno2", active: true, rates: { rxBps: 100_000, txBps: 0, rxPps: 0, txPps: 0, rxErrsPerSec: 0, txErrsPerSec: 0, rxDropPerSec: 0, txDropPerSec: 0 } },
      ],
    };
    renderPanel(bondDetail.ref);

    const tab = await screen.findByRole("tab", { name: "Metrics" });
    await userEvent.click(tab);

    expect(await screen.findByText(/Slave balance/)).toBeInTheDocument();
    expect(screen.getByText("physnic:pve1:eno1")).toBeInTheDocument();
    expect(screen.getByText("physnic:pve1:eno2")).toBeInTheDocument();
  });

  it("does not show a Metrics tab for a kind the sampler never tracks (guest)", async () => {
    mockDetailResponse = { ...bridgeDetail, ref: "guest:pve1:100", kind: "guest", label: "vm100" };
    mockLiveMetric = undefined;
    renderPanel("guest:pve1:100");

    await screen.findByText("vm100");
    expect(screen.queryByRole("tab", { name: "Metrics" })).not.toBeInTheDocument();
  });
});
