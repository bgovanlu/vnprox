// SPDX-License-Identifier: Apache-2.0

// T-908 acceptance criteria 2 & 3: compare mode for two same-kind entities
// (matching field rows aligned column-wise, each pane's own Metrics
// sparkline), and the mismatched-kind fallback (states why, never a broken
// layout).
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import type { EntityDetail, LiveMetric } from "../api/types";
import { ToastProvider } from "../components/Toast";
import { InspectorCompareView } from "./InspectorCompareView";

function bondDetail(ref: string, node: string, label: string, extraFields: Record<string, unknown> = {}): EntityDetail {
  return {
    ref,
    kind: "bond",
    node,
    label,
    fields: { Mode: "802.3ad", MTU: 1500, ...extraFields },
    provenance: {},
    related: [],
    generatedAt: 1,
  };
}

const bond1 = bondDetail("bond:pve1:bond0", "pve1", "bond0", { Mode: "802.3ad" });
const bond2 = bondDetail("bond:pve2:bond0", "pve2", "bond0", { Mode: "active-backup" });
const bridge1: EntityDetail = {
  ref: "bridge:pve1:vmbr0",
  kind: "bridge",
  node: "pve1",
  label: "vmbr0",
  fields: {},
  provenance: {},
  related: [],
  generatedAt: 1,
};

const detailByRef: Record<string, EntityDetail> = {
  [bond1.ref]: bond1,
  [bond2.ref]: bond2,
  [bridge1.ref]: bridge1,
};

vi.mock("../api/topology", () => ({
  fetchInventoryDetail: vi.fn((ref: string) => Promise.resolve(detailByRef[ref])),
  fetchTopology: vi.fn(),
  searchInventory: vi.fn(),
}));

let liveMetricByRef: Record<string, LiveMetric | undefined> = {};

vi.mock("../api/metrics", () => ({
  fetchMetricsLive: vi.fn((refs: string[]) =>
    Promise.resolve(refs.map((r) => liveMetricByRef[r]).filter((m): m is LiveMetric => m !== undefined)),
  ),
  fetchMetricsHistory: vi.fn((ref: string) =>
    Promise.resolve([
      { at: 0, rates: { rxBps: ref === bond1.ref ? 1000 : 5000, txBps: 500, rxPps: 1, txPps: 1, rxErrsPerSec: 0, txErrsPerSec: 0, rxDropPerSec: 0, txDropPerSec: 0 } },
      { at: 30, rates: { rxBps: ref === bond1.ref ? 2000 : 6000, txBps: 800, rxPps: 2, txPps: 2, rxErrsPerSec: 0, txErrsPerSec: 0, rxDropPerSec: 0, txDropPerSec: 0 } },
    ]),
  ),
}));

function renderCompare(refA: string, refB: string) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <ToastProvider>
          <InspectorCompareView
            refA={refA}
            refB={refB}
            pinnedA
            pinnedB
            onCloseA={() => undefined}
            onCloseB={() => undefined}
            onTogglePinA={() => undefined}
            onTogglePinB={() => undefined}
            onSelectRelated={() => undefined}
          />
        </ToastProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("InspectorCompareView — same-kind pair", () => {
  it("renders an aligned compare grid with both entities' field values", async () => {
    liveMetricByRef = {};
    renderCompare(bond1.ref, bond2.ref);

    const compare = await screen.findByTestId("inspector-compare");
    // Both labels appear as column headers.
    expect(within(compare).getAllByText("bond0")).toHaveLength(2);
    // The Mode row shows both values, aligned in the same row.
    const modeLabel = await within(compare).findByText("Mode");
    const modeRow = modeLabel.parentElement;
    expect(modeRow).not.toBeNull();
    if (modeRow) {
      expect(within(modeRow).getByText("802.3ad")).toBeInTheDocument();
      expect(within(modeRow).getByText("active-backup")).toBeInTheDocument();
    }
  });

  it("renders each pane's own Metrics sparkline independently", async () => {
    liveMetricByRef = {};
    renderCompare(bond1.ref, bond2.ref);
    await screen.findByTestId("inspector-compare");

    const user = userEvent.setup();
    await user.click(screen.getByRole("tab", { name: "Metrics" }));

    const sparklines = await screen.findAllByTestId("metrics-sparkline");
    expect(sparklines).toHaveLength(2);
  });
});

describe("InspectorCompareView — mismatched-kind pair", () => {
  it("states why compare is unavailable and falls back to two independent panes, not a broken layout", async () => {
    renderCompare(bond1.ref, bridge1.ref);

    expect(await screen.findByRole("status")).toHaveTextContent(/only available for two entities of the same kind/);
    expect(screen.queryByTestId("inspector-compare")).not.toBeInTheDocument();
    // Both entities are still independently visible.
    expect(await screen.findByText("bond0")).toBeInTheDocument();
    expect(screen.getByText("vmbr0")).toBeInTheDocument();
  });
});
