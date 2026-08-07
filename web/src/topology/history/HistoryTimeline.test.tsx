// T-1007 acceptance criteria 1/2/4 (planning/tasks/phase-10.md):
// 1. Scrubbing re-issues GET /metrics/history + GET /flows with the
//    correct fromTs/toTs and re-renders from that historical data, not the
//    live WS-fed cache.
// 2. Event markers render at correct positions for seeded audit rows +
//    finding transitions; clicking a changeset marker navigates to that
//    changeset's existing detail view (opens the drawer), never re-applies.
// 4. Range clamping: scrubbing before the earliest available flow data
//    disables the Flows layer with the disclosed message while
//    metrics-only playback keeps working.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fetchFlows } from "../../api/flows";
import { fetchHistoryEvents } from "../../api/history";
import { fetchMetricsHistory, fetchMetricsLive } from "../../api/metrics";
import type { FlowRecord, FlowsPage, HistoryEvent, LiveMetric, Rates } from "../../api/types";
import { useChangesetDrawerStore } from "../../changesets/store";
import { HistoryTimeline, type HistoryPlaybackState } from "./HistoryTimeline";

const ZERO_RATES: Rates = { rxBps: 0, txBps: 0, rxPps: 0, txPps: 0, rxErrsPerSec: 0, txErrsPerSec: 0, rxDropPerSec: 0, txDropPerSec: 0 };
const HISTORICAL_RATES: Rates = { ...ZERO_RATES, rxBps: 500_000_000 }; // -> 50% of a 1000Mbps link

const NOW = 2_000_000;
const REF = "bond:pve1:bond0";

vi.mock("../../api/metrics", () => ({
  fetchMetricsHistory: vi.fn(),
  fetchMetricsLive: vi.fn(),
}));
vi.mock("../../api/flows", () => ({
  fetchFlows: vi.fn(),
}));
vi.mock("../../api/history", () => ({
  fetchHistoryEvents: vi.fn(),
}));

const fetchMetricsHistoryMock = vi.mocked(fetchMetricsHistory);
const fetchMetricsLiveMock = vi.mocked(fetchMetricsLive);
const fetchFlowsMock = vi.mocked(fetchFlows);
const fetchHistoryEventsMock = vi.mocked(fetchHistoryEvents);

const HISTORICAL_FLOW_RECORD: FlowRecord = {
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

const LIVE_FLOW_RECORD: FlowRecord = { ...HISTORICAL_FLOW_RECORD, bytes: 1, dstIp: "10.0.0.7", dstRef: "bridge:pve3:vmbr0" };

function renderTimeline(opts: {
  onPlaybackChange: (state: HistoryPlaybackState) => void;
  liveUtilizationByRef?: ReadonlyMap<string, number>;
  liveFlowRecords?: readonly FlowRecord[];
  flowRetentionMinutes?: number;
}) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <HistoryTimeline
        metricsRefs={[REF]}
        liveUtilizationByRef={opts.liveUtilizationByRef ?? new Map([[REF, 5]])}
        liveFlowRecords={opts.liveFlowRecords ?? [LIVE_FLOW_RECORD]}
        flowRetentionMinutes={opts.flowRetentionMinutes ?? 60}
        onPlaybackChange={opts.onPlaybackChange}
        now={() => NOW}
      />
    </QueryClientProvider>,
  );
}

function scrubTo(at: number): void {
  const slider = screen.getByLabelText("Scrub map history");
  fireEvent.change(slider, { target: { value: String(at) } });
}

beforeEach(() => {
  vi.clearAllMocks();
  useChangesetDrawerStore.setState({ activeId: undefined, drawerOpen: false, reviewRequested: false, warningsAcknowledged: false });

  fetchMetricsHistoryMock.mockImplementation((_ref: string, _fromTs?: number, toTs?: number) =>
    Promise.resolve([{ at: toTs ?? 0, rates: HISTORICAL_RATES }]),
  );
  fetchMetricsLiveMock.mockImplementation((refs: string[]) =>
    Promise.resolve(refs.map((ref): LiveMetric => ({ ref, at: NOW, rates: ZERO_RATES, speedMbps: 1000 }))),
  );
  fetchFlowsMock.mockImplementation(
    (): Promise<FlowsPage> => Promise.resolve({ items: [HISTORICAL_FLOW_RECORD] }),
  );
  fetchHistoryEventsMock.mockImplementation((): Promise<HistoryEvent[]> => Promise.resolve([]));
});

describe("HistoryTimeline", () => {
  it("passes the live values through unchanged before any scrub", async () => {
    const onPlaybackChange = vi.fn();
    const liveUtil = new Map([[REF, 5]]);
    renderTimeline({ onPlaybackChange, liveUtilizationByRef: liveUtil, liveFlowRecords: [LIVE_FLOW_RECORD] });

    await waitFor(() => {
      expect(onPlaybackChange).toHaveBeenCalledWith(
        expect.objectContaining({ scrubbing: false, at: undefined, utilizationByRef: liveUtil, flowRecords: [LIVE_FLOW_RECORD] }),
      );
    });
    expect(fetchMetricsHistoryMock).not.toHaveBeenCalled();
    expect(fetchFlowsMock).not.toHaveBeenCalled();
  });

  it("AC1: scrubbing re-issues GET /metrics/history and GET /flows with the scrubbed fromTs/toTs, and renders historical data instead of the live cache", async () => {
    const onPlaybackChange = vi.fn();
    const liveUtil = new Map([[REF, 5]]); // distinct from the historical 50% below
    renderTimeline({ onPlaybackChange, liveUtilizationByRef: liveUtil, liveFlowRecords: [LIVE_FLOW_RECORD] });

    const scrubAt = NOW - 120; // 2 minutes ago: well within the 60-minute flow window
    scrubTo(scrubAt);

    await waitFor(() => {
      expect(fetchMetricsHistoryMock).toHaveBeenCalledWith(REF, scrubAt - 90, scrubAt);
    });
    await waitFor(() => {
      expect(fetchFlowsMock).toHaveBeenCalledWith({ fromTs: scrubAt - 60, toTs: scrubAt, limit: 500 });
    });

    await waitFor(() => {
      const lastCall = onPlaybackChange.mock.calls.at(-1)?.[0] as HistoryPlaybackState;
      expect(lastCall.scrubbing).toBe(true);
      expect(lastCall.at).toBe(scrubAt);
      // Historical utilization (50%, from HISTORICAL_RATES/1000Mbps), NOT
      // the live map's value (5) — proof the live/WS-fed cache was not read.
      expect(lastCall.utilizationByRef.get(REF)).toBeCloseTo(50, 5);
      expect(lastCall.flowRecords).toEqual([HISTORICAL_FLOW_RECORD]);
      expect(lastCall.flowsAvailable).toBe(true);
    });
  });

  it("AC4: scrubbing before the flow retention window disables the Flows layer with the disclosed message, while metrics playback continues", async () => {
    const onPlaybackChange = vi.fn();
    renderTimeline({ onPlaybackChange, flowRetentionMinutes: 60 });

    const scrubAt = NOW - 2 * 3600; // 2h ago: beyond the 60-minute flow window, within the 24h metrics window
    scrubTo(scrubAt);

    await waitFor(() => {
      const lastCall = onPlaybackChange.mock.calls.at(-1)?.[0] as HistoryPlaybackState;
      expect(lastCall.scrubbing).toBe(true);
      expect(lastCall.flowsAvailable).toBe(false);
      expect(lastCall.flowRecords).toEqual([]);
      // Metrics-only playback keeps working even though flows are unavailable.
      expect(lastCall.utilizationByRef.get(REF)).toBeCloseTo(50, 5);
    });
    expect(fetchFlowsMock).not.toHaveBeenCalled();
    expect(screen.getByText(/Flow history available for the last 60 minutes only/)).toBeInTheDocument();
  });

  it("AC2: event markers render for seeded changeset/finding rows, and clicking a changeset marker opens its existing detail view", async () => {
    fetchHistoryEventsMock.mockImplementation(
      (): Promise<HistoryEvent[]> =>
        Promise.resolve([
          { at: NOW - 1000, kind: "changeset", action: "changeset.confirm", result: "committed", changesetId: "cs-42" },
          { at: NOW - 500, kind: "finding", findingId: "f1", transition: "new" },
        ]),
    );
    const onPlaybackChange = vi.fn();
    renderTimeline({ onPlaybackChange });

    const changesetMarker = await screen.findByRole("button", { name: /Changeset event: changeset\.confirm/ });
    const findingMarker = await screen.findByRole("button", { name: /Finding new/ });
    expect(findingMarker).toBeDisabled();

    fireEvent.click(changesetMarker);
    await waitFor(() => {
      expect(useChangesetDrawerStore.getState().activeId).toBe("cs-42");
      expect(useChangesetDrawerStore.getState().drawerOpen).toBe(true);
    });
  });

  // T-2003-bug-01 regression. The defect: the caller handed this component a
  // freshly built `liveFlowRecords` array on every render, this effect saw a
  // changed dependency, called back into the caller's setState, and the
  // caller re-rendered — forever. That loop is what starved React Router
  // v7's navigation transition and left the app unable to leave the Topology
  // page. The source of the unstable identity is fixed in flowsQueries.ts;
  // this asserts the component itself no longer amplifies one into a loop.
  it("does not re-notify the caller when a re-render brings equal-but-freshly-built live values", async () => {
    const onPlaybackChange = vi.fn();
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const timeline = (util: ReadonlyMap<string, number>, records: readonly FlowRecord[]) => (
      <QueryClientProvider client={queryClient}>
        <HistoryTimeline
          metricsRefs={[REF]}
          liveUtilizationByRef={util}
          liveFlowRecords={records}
          flowRetentionMinutes={60}
          onPlaybackChange={onPlaybackChange}
          now={() => NOW}
        />
      </QueryClientProvider>
    );

    const { rerender } = render(timeline(new Map([[REF, 5]]), [LIVE_FLOW_RECORD]));
    await waitFor(() => {
      expect(onPlaybackChange).toHaveBeenCalledTimes(1);
    });

    // Every rerender passes brand-new Map/array objects holding exactly the
    // same contents — the identity churn the defect was made of.
    for (let i = 0; i < 5; i++) {
      rerender(timeline(new Map([[REF, 5]]), [LIVE_FLOW_RECORD]));
    }
    await waitFor(() => {
      expect(screen.getByLabelText("Scrub map history")).toBeInTheDocument();
    });
    expect(onPlaybackChange).toHaveBeenCalledTimes(1);

    // ...and a real change still gets through, so the guard is not simply
    // muting the component.
    rerender(timeline(new Map([[REF, 42]]), [LIVE_FLOW_RECORD]));
    await waitFor(() => {
      expect(onPlaybackChange).toHaveBeenCalledTimes(2);
    });
  });

  it("never renders any apply/confirm/rollback affordance", () => {
    renderTimeline({ onPlaybackChange: vi.fn() });
    for (const verb of ["Apply", "Confirm", "Rollback"]) {
      expect(screen.queryByRole("button", { name: new RegExp(verb, "i") })).not.toBeInTheDocument();
    }
  });
});
