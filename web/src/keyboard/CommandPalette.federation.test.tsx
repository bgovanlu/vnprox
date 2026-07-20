// T-1202 AC3/AC5: with >=2 clusters attached, the palette fans search out
// across clusters (GET /federation/search), groups the namespaced hits by
// cluster, and offers a "Switch to <cluster>" action that changes the active
// cluster context.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { SearchResponse } from "../api/types";
import type { FederationSearchResponse, FederationTopologyResponse } from "../api/federation";
import { CommandPalette } from "./CommandPalette";
import { usePaletteActionsStore } from "./actions";

const searchInventoryMock = vi.fn<(q: string) => Promise<SearchResponse>>();
const fetchFederationTopologyMock = vi.fn<() => Promise<FederationTopologyResponse>>();
const fetchFederationSearchMock = vi.fn<(q: string) => Promise<FederationSearchResponse>>();

vi.mock("../api/topology", () => ({
  searchInventory: (q: string) => searchInventoryMock(q),
}));

vi.mock("../api/federation", () => ({
  fetchFederationTopology: () => fetchFederationTopologyMock(),
  fetchFederationSearch: (q: string) => fetchFederationSearchMock(q),
  // queries.ts imports this for the drilled-cluster fetch; unused here.
  fetchFederationClusterTopology: vi.fn(),
}));

afterEach(() => {
  usePaletteActionsStore.setState({ actionsByOwner: new Map(), allActions: [] });
  searchInventoryMock.mockReset();
  fetchFederationTopologyMock.mockReset();
  fetchFederationSearchMock.mockReset();
});

function LocationProbe() {
  const loc = useLocation();
  return <div data-testid="location">{loc.pathname + loc.search}</div>;
}

function renderPalette() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={["/dashboard"]}>
        <CommandPalette open onOpenChange={vi.fn()} />
        <Routes>
          <Route path="*" element={<LocationProbe />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

const clusterEast = {
  clusterId: "cl-a",
  clusterName: "east",
  reachable: true,
  nodes: 1,
  nodesOnline: 1,
  guests: 0,
  findings: 0,
  drift: false,
};
const clusterWest = { ...clusterEast, clusterId: "cl-b", clusterName: "west" };
const twoClusters: FederationTopologyResponse = { clusters: [clusterEast, clusterWest] };

describe("CommandPalette federation", () => {
  it("groups namespaced hits by cluster with a switch action per cluster", async () => {
    searchInventoryMock.mockResolvedValue({ results: [] });
    fetchFederationTopologyMock.mockResolvedValue(twoClusters);
    fetchFederationSearchMock.mockResolvedValue({
      results: [
        { clusterId: "cl-a", clusterName: "east", ref: "guest:pve1:100", kind: "guest", label: "db-east", node: "pve1", matchedField: "name" },
        { clusterId: "cl-b", clusterName: "west", ref: "guest:pve1:100", kind: "guest", label: "db-west", node: "pve1", matchedField: "name" },
      ],
    });

    renderPalette();
    await userEvent.type(screen.getByLabelText("Command palette input"), "db");

    await waitFor(() => {
      expect(screen.getByText("db-east")).toBeInTheDocument();
    });
    expect(screen.getByText("db-west")).toBeInTheDocument();
    // One switch action per distinct cluster.
    expect(screen.getByText("Switch to east")).toBeInTheDocument();
    expect(screen.getByText("Switch to west")).toBeInTheDocument();
  });

  it("switches active cluster context when a non-active cluster hit is chosen", async () => {
    searchInventoryMock.mockResolvedValue({ results: [] });
    fetchFederationTopologyMock.mockResolvedValue(twoClusters);
    fetchFederationSearchMock.mockResolvedValue({
      results: [
        { clusterId: "cl-b", clusterName: "west", ref: "guest:pve2:200", kind: "guest", label: "web-west", node: "pve2", matchedField: "name" },
      ],
    });

    renderPalette();
    await userEvent.type(screen.getByLabelText("Command palette input"), "web");
    await waitFor(() => {
      expect(screen.getByText("web-west")).toBeInTheDocument();
    });
    await userEvent.click(screen.getByText("web-west"));

    await waitFor(() => {
      expect(screen.getByTestId("location")).toHaveTextContent("/topology?cluster=cl-b");
    });
  });

  it("shows no federation section with fewer than two clusters attached", async () => {
    searchInventoryMock.mockResolvedValue({ results: [] });
    fetchFederationTopologyMock.mockResolvedValue({ clusters: [clusterEast] });
    fetchFederationSearchMock.mockResolvedValue({ results: [] });

    renderPalette();
    await userEvent.type(screen.getByLabelText("Command palette input"), "db");

    await waitFor(() => {
      expect(screen.getByText("No matches.")).toBeInTheDocument();
    });
    expect(fetchFederationSearchMock).not.toHaveBeenCalled();
  });
});
