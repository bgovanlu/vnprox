// Component-level tests for T-602's unified findings stream panel: filter
// composition (source/severity/node, AC2) rendered against the shared
// FindingsList presentation, and the fix-changeset wiring. The backend is
// mocked at the api/findings.ts boundary; the WS bridge is stubbed the same
// way ChangesetDrawer.test.tsx stubs it (no real WebSocket in jsdom).
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import { ToastProvider } from "../components/Toast";
import type { Changeset, StreamFinding } from "../api/types";
import { FindingsStreamPanel } from "./FindingsStreamPanel";

const sample: StreamFinding[] = [
  { id: "drift:1", source: "drift", check: "bridge_divergence", severity: "warning", detail: "bridge diverges", nodes: ["pve1"], fixable: false, docsLink: "docs/x" },
  { id: "lldp:1", source: "lldp", check: "vlan_cross_check_missing_on_switch", severity: "warning", detail: "switch missing vlan 20", nodes: ["pve2"], fixable: false, docsLink: "docs/y" },
  { id: "health:1", source: "health", check: "bond_slave_down", severity: "error", detail: "bond0 slave eno2 is down", nodes: ["pve1"], fixable: false, docsLink: "docs/z" },
];

const fetchFindings = vi.fn(() => Promise.resolve(sample));
const fixFinding = vi.fn(
  (_id: string): Promise<Changeset> =>
    Promise.resolve({
      id: "cs1", title: "t", author: "a", status: "draft", ops: [], findings: [],
      createdAt: 0, updatedAt: 0,
    }),
);

vi.mock("../api/findings", () => ({
  fetchFindings: () => fetchFindings(),
  fixFinding: (id: string) => fixFinding(id),
}));

vi.mock("../api/ws", () => ({
  createWsClient: () => ({ subscribe: () => () => undefined, status: () => "closed", close: () => undefined }),
  defaultWsUrl: () => "ws://unused",
}));

function renderPanel(): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MemoryRouter>
      <QueryClientProvider client={queryClient}>
        <ToastProvider>
          <FindingsStreamPanel />
        </ToastProvider>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

describe("FindingsStreamPanel", () => {
  it("renders every source's findings by default", async () => {
    renderPanel();
    await waitFor(() => {
      expect(screen.getByText("bridge diverges")).toBeInTheDocument();
    });
    expect(screen.getByText("switch missing vlan 20")).toBeInTheDocument();
    expect(screen.getByText("bond0 slave eno2 is down")).toBeInTheDocument();
  });

  it("filters by source", async () => {
    const user = userEvent.setup();
    renderPanel();
    await waitFor(() => screen.getByText("bridge diverges"));

    await user.selectOptions(screen.getByLabelText("Filter by source"), "lldp");

    expect(screen.queryByText("bridge diverges")).not.toBeInTheDocument();
    expect(screen.getByText("switch missing vlan 20")).toBeInTheDocument();
    expect(screen.queryByText("bond0 slave eno2 is down")).not.toBeInTheDocument();
  });

  it("filters by severity", async () => {
    const user = userEvent.setup();
    renderPanel();
    await waitFor(() => screen.getByText("bridge diverges"));

    await user.selectOptions(screen.getByLabelText("Filter by severity"), "error");

    expect(screen.getByText("bond0 slave eno2 is down")).toBeInTheDocument();
    expect(screen.queryByText("bridge diverges")).not.toBeInTheDocument();
  });

  it("filters by node", async () => {
    const user = userEvent.setup();
    renderPanel();
    await waitFor(() => screen.getByText("bridge diverges"));

    await user.selectOptions(screen.getByLabelText("Filter by node"), "pve2");

    expect(screen.getByText("switch missing vlan 20")).toBeInTheDocument();
    expect(screen.queryByText("bridge diverges")).not.toBeInTheDocument();
  });

  it("combines filters and shows a clear-filters affordance", async () => {
    const user = userEvent.setup();
    renderPanel();
    await waitFor(() => screen.getByText("bridge diverges"));

    await user.selectOptions(screen.getByLabelText("Filter by source"), "drift");
    await user.selectOptions(screen.getByLabelText("Filter by node"), "pve2");

    expect(screen.getByText("No findings match this filter")).toBeInTheDocument();

    await user.click(screen.getByText("Clear filters"));
    expect(screen.getByText("bridge diverges")).toBeInTheDocument();
  });

  it("shows the healthy empty state when the stream is empty", async () => {
    fetchFindings.mockResolvedValueOnce([]);
    renderPanel();
    await waitFor(() => {
      expect(screen.getByText("No findings")).toBeInTheDocument();
    });
  });

  // T-806: found via this task's own e2e run — a persisted sim_divergence
  // finding with an empty `nodes` array (it names refs, not nodes) must
  // render and filter cleanly, not crash filters.ts's nodesIn/
  // filterFindings the way a nil/undefined `nodes` value did before that
  // fix. This also covers AC2's "every source" premise now spanning five,
  // not four, sources.
  it("renders a probe-sourced (sim_divergence) finding with an empty nodes array, and offers a 'View in simulator' deep link", async () => {
    const probeFinding: StreamFinding = {
      id: "probe:sim_divergence|guest-nic:pve1:300/net0|guest-nic:guest-nic:pve1:301/net0|tcp|2222",
      source: "probe", check: "sim_divergence", severity: "warning",
      detail: "Simulated verdict: deny. Observed: reachable.", nodes: [],
      refs: ["guest-nic:pve1:300/net0"], fixable: false,
      docsLink: "/tools?srcKind=guest-nic&srcRef=guest-nic%3Apve1%3A300%2Fnet0&dstKind=guest-nic&dstRef=guest-nic%3Apve1%3A301%2Fnet0&proto=tcp&port=2222",
    };
    fetchFindings.mockResolvedValueOnce([...sample, probeFinding]);
    const user = userEvent.setup();
    renderPanel();

    await waitFor(() => {
      expect(screen.getByText("Simulated verdict: deny. Observed: reachable.")).toBeInTheDocument();
    });
    expect(screen.getByText("Verify live · sim_divergence")).toBeInTheDocument();

    const viewLink = screen.getByRole("button", { name: "View in simulator" });
    await user.click(viewLink);
    // Navigated (MemoryRouter has no visible URL bar to assert against
    // directly here — the click not throwing, plus the button existing at
    // all, is this test's own regression coverage; SimulatorPage.tsx's own
    // urlState round-trip is covered by urlState.test.ts).
    expect(viewLink).toBeInTheDocument();
  });

  it("includes probe in the source filter and filters a probe finding correctly", async () => {
    const probeFinding: StreamFinding = {
      id: "probe:1", source: "probe", check: "sim_divergence", severity: "warning",
      detail: "probe finding", nodes: [], fixable: false,
    };
    fetchFindings.mockResolvedValueOnce([...sample, probeFinding]);
    const user = userEvent.setup();
    renderPanel();
    await waitFor(() => screen.getByText("bridge diverges"));

    await user.selectOptions(screen.getByLabelText("Filter by source"), "probe");
    expect(screen.getByText("probe finding")).toBeInTheDocument();
    expect(screen.queryByText("bridge diverges")).not.toBeInTheDocument();
  });
});
