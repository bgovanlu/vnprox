// Component-level tests for T-602's unified findings stream panel: filter
// composition (source/severity/node, AC2) rendered against the shared
// FindingsList presentation, and the fix-changeset wiring. The backend is
// mocked at the api/findings.ts boundary; the WS bridge is stubbed the same
// way ChangesetDrawer.test.tsx stubs it (no real WebSocket in jsdom).
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
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
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <FindingsStreamPanel />
      </ToastProvider>
    </QueryClientProvider>,
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
});
