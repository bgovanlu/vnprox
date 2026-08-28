// SPDX-License-Identifier: Apache-2.0

// T-3910 acceptance criteria (planning/tasks/phase-39.md):
// 1. The replay window's real bounds (24h metrics / configured-minutes
//    flows) are shown up front, not discovered by dragging into emptiness.
// 3. Honest empty/disabled handling: "ingestion is off" reads distinctly
//    from "no traffic in this window", never a single blank map.
// 4. Vitest coverage for the scrub/animation state.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fetchFlows, type FlowsFilter } from "../../api/flows";
import { fetchMetricsHistory, fetchMetricsLive } from "../../api/metrics";
import type { FlowRecord, FlowsPage, LiveMetric, Rates } from "../../api/types";
import { FlowReplayPanel, type ReplayFrameState } from "./FlowReplayPanel";

const ZERO_RATES: Rates = { rxBps: 0, txBps: 0, rxPps: 0, txPps: 0, rxErrsPerSec: 0, txErrsPerSec: 0, rxDropPerSec: 0, txDropPerSec: 0 };
const NOW = 2_000_000;
const REF = "bond:pve1:bond0";

vi.mock("../../api/metrics", () => ({
  fetchMetricsHistory: vi.fn(),
  fetchMetricsLive: vi.fn(),
}));
vi.mock("../../api/flows", () => ({
  fetchFlows: vi.fn(),
}));

const fetchMetricsHistoryMock = vi.mocked(fetchMetricsHistory);
const fetchMetricsLiveMock = vi.mocked(fetchMetricsLive);
const fetchFlowsMock = vi.mocked(fetchFlows);

const FLOW_RECORD: FlowRecord = {
  at: NOW - 100,
  node: "pve1",
  srcIp: "10.0.0.5",
  dstIp: "10.0.0.6",
  proto: 6,
  bytes: 12345,
  packets: 10,
  srcRef: "bridge:pve1:vmbr0",
  dstRef: "bridge:pve2:vmbr0",
  source: "netflow5",
};

function renderPanel(opts: { onReplayChange: (state: ReplayFrameState) => void; flowRetentionMinutes?: number }) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <FlowReplayPanel
        metricsRefs={[REF]}
        liveUtilizationByRef={new Map([[REF, 5]])}
        liveFlowRecords={[]}
        flowRetentionMinutes={opts.flowRetentionMinutes ?? 60}
        onReplayChange={opts.onReplayChange}
        now={() => NOW}
      />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  fetchMetricsHistoryMock.mockImplementation((_ref: string, _fromTs?: number, toTs?: number) =>
    Promise.resolve([{ at: toTs ?? 0, rates: ZERO_RATES }]),
  );
  fetchMetricsLiveMock.mockImplementation((refs: string[]) =>
    Promise.resolve(refs.map((ref): LiveMetric => ({ ref, at: NOW, rates: ZERO_RATES, speedMbps: 1000 }))),
  );
});

describe("FlowReplayPanel — real bounds shown up front", () => {
  it("states both rings' actual retention in the header before any scrub", () => {
    fetchFlowsMock.mockImplementation((): Promise<FlowsPage> => Promise.resolve({ items: [] }));
    renderPanel({ onReplayChange: vi.fn(), flowRetentionMinutes: 60 });
    expect(screen.getByText(/Heat: last 24h/)).toBeInTheDocument();
    expect(screen.getByText(/Flow paths: last 60 min/)).toBeInTheDocument();
  });

  it("reflects a non-default configured flow retention", () => {
    fetchFlowsMock.mockImplementation((): Promise<FlowsPage> => Promise.resolve({ items: [] }));
    renderPanel({ onReplayChange: vi.fn(), flowRetentionMinutes: 15 });
    expect(screen.getByText(/Flow paths: last 15 min/)).toBeInTheDocument();
  });
});

describe("FlowReplayPanel — the two honest empty cases", () => {
  it("says ingestion is not enabled when the cluster-wide probe finds nothing at all", async () => {
    fetchFlowsMock.mockImplementation((): Promise<FlowsPage> => Promise.resolve({ items: [] }));
    renderPanel({ onReplayChange: vi.fn() });

    fireEvent.click(screen.getByLabelText("Step back one frame"));

    await waitFor(() => {
      expect(screen.getByText(/Flow ingestion is not enabled on this node/)).toBeInTheDocument();
    });
    expect(screen.getByText(/Traffic heat still replays/)).toBeInTheDocument();
  });

  it("says traffic was quiet (not 'ingestion disabled') when ingestion is on but this instant is empty", async () => {
    fetchFlowsMock.mockImplementation((filter: FlowsFilter = {}): Promise<FlowsPage> => {
      // The cluster-wide probe (guestEgoQueries.useClusterHasAnyFlowsProbe)
      // asks for exactly one record; this instant's own windowed fetch
      // asks for up to 500 and gets none — ingestion is on, this frame is
      // simply quiet.
      if (filter.limit === 1) return Promise.resolve({ items: [FLOW_RECORD] });
      return Promise.resolve({ items: [] });
    });
    renderPanel({ onReplayChange: vi.fn() });

    fireEvent.click(screen.getByLabelText("Step back one frame"));

    await waitFor(() => {
      expect(screen.getByText(/No flow paths recorded at this instant/)).toBeInTheDocument();
    });
    expect(screen.queryByText(/not enabled/)).not.toBeInTheDocument();
  });
});

describe("FlowReplayPanel — stepping and play", () => {
  it("Step back/forward move the scrub position and emit a scrubbing frame", async () => {
    fetchFlowsMock.mockImplementation((filter: FlowsFilter = {}): Promise<FlowsPage> => {
      if (filter.limit === 1) return Promise.resolve({ items: [FLOW_RECORD] });
      return Promise.resolve({ items: [FLOW_RECORD] });
    });
    const onReplayChange = vi.fn();
    renderPanel({ onReplayChange });

    expect(screen.getByText("Live")).toBeInTheDocument();

    fireEvent.click(screen.getByLabelText("Step back one frame"));

    await waitFor(() => {
      const scrubbingCall = onReplayChange.mock.calls.find((call: unknown[]) => (call[0] as ReplayFrameState).scrubbing);
      expect(scrubbingCall).toBeDefined();
    });
    expect(screen.queryByText("Live")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Exit replay" }));
    expect(screen.getByText("Live")).toBeInTheDocument();
  });

  it("Play is disabled when the environment prefers reduced motion", () => {
    const matchMediaMock = vi.fn().mockImplementation((query: string) => ({
      matches: query.includes("prefers-reduced-motion"),
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }));
    vi.stubGlobal("matchMedia", matchMediaMock);
    fetchFlowsMock.mockImplementation((): Promise<FlowsPage> => Promise.resolve({ items: [] }));

    renderPanel({ onReplayChange: vi.fn() });

    expect(screen.getByRole("button", { name: "Play" })).toBeDisabled();
    expect(screen.getByText(/Autoplay is off/)).toBeInTheDocument();

    vi.unstubAllGlobals();
  });
});
