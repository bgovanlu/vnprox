// SPDX-License-Identifier: Apache-2.0

// T-1202 AC2: the capsule (global) view renders only with >=2 clusters
// attached. With exactly one cluster (or none / federation unwired), the gate
// renders the ordinary <TopologyPage/> with no wrapper — federation is
// invisible. Drilling into a cluster (>=2 attached, `?cluster` set) shows the
// topology page plus a back-to-global affordance.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { FederationTopologyResponse } from "../../api/federation";
import { GlobalTopologyGate } from "./GlobalTopologyGate";

const fetchFederationTopologyMock = vi.fn<() => Promise<FederationTopologyResponse>>();

vi.mock("../../api/federation", () => ({
  fetchFederationTopology: () => fetchFederationTopologyMock(),
}));

// The real TopologyPage pulls in the whole canvas stack; stub it — the gate's
// job is only *which* surface to mount, which the stub identity proves.
vi.mock("../TopologyPage", () => ({
  TopologyPage: () => <div data-testid="topology-page">topology page</div>,
}));

vi.mock("./GlobalTopologyView", () => ({
  GlobalTopologyView: ({ clusters }: { clusters: unknown[] }) => (
    <div data-testid="global-view">capsules:{clusters.length}</div>
  ),
}));

function summary(clusterId: string, clusterName: string): FederationTopologyResponse["clusters"][number] {
  return { clusterId, clusterName, reachable: true, nodes: 1, nodesOnline: 1, guests: 0, findings: 0, drift: false };
}

function renderGate(initialEntries: string[] = ["/topology"]) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={initialEntries}>
        <GlobalTopologyGate />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

afterEach(() => {
  fetchFederationTopologyMock.mockReset();
});

describe("GlobalTopologyGate", () => {
  it("renders the ordinary topology page unchanged with exactly one cluster", async () => {
    fetchFederationTopologyMock.mockResolvedValue({ clusters: [summary("cl-a", "east")] });
    renderGate();
    await waitFor(() => {
      expect(screen.getByTestId("topology-page")).toBeInTheDocument();
    });
    expect(screen.queryByTestId("global-view")).not.toBeInTheDocument();
  });

  it("renders the topology page when federation is unwired (empty attachment set)", async () => {
    fetchFederationTopologyMock.mockResolvedValue({ clusters: [] });
    renderGate();
    await waitFor(() => {
      expect(screen.getByTestId("topology-page")).toBeInTheDocument();
    });
    expect(screen.queryByTestId("global-view")).not.toBeInTheDocument();
  });

  it("renders the global capsule view with two or more clusters attached", async () => {
    fetchFederationTopologyMock.mockResolvedValue({ clusters: [summary("cl-a", "east"), summary("cl-b", "west")] });
    renderGate();
    await waitFor(() => {
      expect(screen.getByTestId("global-view")).toBeInTheDocument();
    });
    expect(screen.getByText("capsules:2")).toBeInTheDocument();
    expect(screen.queryByTestId("topology-page")).not.toBeInTheDocument();
  });

  it("drills into a cluster's topology when ?cluster is set, with a back affordance", async () => {
    fetchFederationTopologyMock.mockResolvedValue({ clusters: [summary("cl-a", "east"), summary("cl-b", "west")] });
    renderGate(["/topology?cluster=cl-b"]);
    await waitFor(() => {
      expect(screen.getByTestId("topology-page")).toBeInTheDocument();
    });
    // The drilled cluster's name and a back link are shown; the capsule grid
    // is not.
    expect(screen.getByText("west")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Global map/ })).toBeInTheDocument();
    expect(screen.queryByTestId("global-view")).not.toBeInTheDocument();
    // Back returns to the capsule grid.
    await userEvent.click(screen.getByRole("button", { name: /Global map/ }));
    await waitFor(() => {
      expect(screen.getByTestId("global-view")).toBeInTheDocument();
    });
  });
});
