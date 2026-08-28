// SPDX-License-Identifier: Apache-2.0

// T-3909: the stitched hub-and-spoke map — WireGuard interconnect edges
// between the local hub and every attached cluster capsule that has an
// effective tunnel linkage. The two extra data sources this view fetches
// (the cluster registry's tunnel linkage, and this node's own live
// WireGuard tunnel state) are mocked at the hook boundary so these tests
// exercise real composition/render logic without a QueryClientProvider or a
// network layer.
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ClusterSummary, FederationCluster } from "../../api/federation";
import type { WireGuardTunnel } from "../../api/types";
import { GlobalTopologyView } from "./GlobalTopologyView";

const useFederationClustersQueryMock = vi.fn<() => { data: FederationCluster[] | undefined }>();
const useWireGuardTunnelsQueryMock =
  vi.fn<() => { data: WireGuardTunnel[] | undefined; isLoading: boolean; isError: boolean }>();

vi.mock("./federationQueries", () => ({
  useFederationClustersQuery: () => useFederationClustersQueryMock(),
}));

vi.mock("../../wireguard/wgTunnelsQuery", () => ({
  useWireGuardTunnelsQuery: () => useWireGuardTunnelsQueryMock(),
}));

const NOW_SEC = Math.floor(Date.now() / 1000);

function summary(clusterId: string, clusterName: string): ClusterSummary {
  return { clusterId, clusterName, reachable: true, nodes: 1, nodesOnline: 1, guests: 0, findings: 0, drift: false };
}

function registryCluster(overrides: Partial<FederationCluster> = {}): FederationCluster {
  return {
    id: "cl-a",
    name: "east",
    apiUrl: "https://east:8006",
    status: "ok",
    addedBy: "admin@pam",
    addedAt: 0,
    ...overrides,
  };
}

describe("GlobalTopologyView — interconnect edges (T-3909)", () => {
  it("renders no edges/legend when no attached cluster has a tunnel linkage", () => {
    useFederationClustersQueryMock.mockReturnValue({ data: [registryCluster({ wgTunnelId: undefined })] });
    useWireGuardTunnelsQueryMock.mockReturnValue({ data: [], isLoading: false, isError: false });

    render(<GlobalTopologyView clusters={[summary("cl-a", "east")]} onDrill={vi.fn()} />);

    expect(screen.getByText("east")).toBeInTheDocument();
    expect(screen.queryByRole("list", { name: "WireGuard interconnects" })).not.toBeInTheDocument();
  });

  it("renders an 'up' edge, with text (not colour-only) conveying the state", () => {
    useFederationClustersQueryMock.mockReturnValue({
      data: [registryCluster({ id: "cl-a", wgTunnelId: "tun-1", wgTunnelSource: "explicit" })],
    });
    useWireGuardTunnelsQueryMock.mockReturnValue({
      data: [
        {
          id: "tun-1",
          node: "pve1",
          ifName: "wg0",
          publicKey: "PK",
          addresses: [],
          peers: [{ publicKey: "P", allowedIps: [], rxBytes: 0, txBytes: 0, external: false, endpointDrifted: false, lastHandshakeUnix: NOW_SEC - 10 }],
          status: { interfaceUp: true, peerCount: 1 },
          listenPort: 51820,
          mtu: 1420,
        },
      ],
      isLoading: false,
      isError: false,
    });

    render(<GlobalTopologyView clusters={[summary("cl-a", "east")]} onDrill={vi.fn()} />);

    const list = screen.getByRole("list", { name: "WireGuard interconnects" });
    expect(list).toBeInTheDocument();
    expect(screen.getByText("interconnect up")).toBeInTheDocument();
    // The capsule itself also carries a text badge, independent of the
    // legend entry — WCAG 1.4.1: colour is never the only signal.
    expect(screen.getByText("WG interconnect: up")).toBeInTheDocument();
  });

  it("renders a 'down' edge distinctly from 'up', by text", () => {
    useFederationClustersQueryMock.mockReturnValue({ data: [registryCluster({ id: "cl-a", wgTunnelId: "tun-1" })] });
    useWireGuardTunnelsQueryMock.mockReturnValue({
      data: [
        {
          id: "tun-1",
          node: "pve1",
          ifName: "wg0",
          publicKey: "PK",
          addresses: [],
          peers: [],
          status: { interfaceUp: false, peerCount: 0 },
          listenPort: 51820,
          mtu: 1420,
        },
      ],
      isLoading: false,
      isError: false,
    });

    render(<GlobalTopologyView clusters={[summary("cl-a", "east")]} onDrill={vi.fn()} />);

    expect(screen.getByText("interconnect down")).toBeInTheDocument();
    expect(screen.queryByText("interconnect up")).not.toBeInTheDocument();
  });

  it("degrades a linked cluster's edge to 'unknown' — without blanking its capsule or a sibling cluster's edge — when the local WireGuard read fails", () => {
    useFederationClustersQueryMock.mockReturnValue({
      data: [
        registryCluster({ id: "cl-a", name: "east", wgTunnelId: "tun-1" }),
        registryCluster({ id: "cl-b", name: "west", wgTunnelId: "tun-2" }),
      ],
    });
    useWireGuardTunnelsQueryMock.mockReturnValue({ data: undefined, isLoading: false, isError: true });

    render(
      <GlobalTopologyView
        clusters={[summary("cl-a", "east"), summary("cl-b", "west")]}
        onDrill={vi.fn()}
      />,
    );

    // Both capsules still render in full — a local WireGuard read failure
    // never blanks the capsule grid.
    expect(screen.getByRole("button", { name: "Open cluster east" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Open cluster west" })).toBeInTheDocument();
    // Both linked clusters' edges report unknown, not down and not silently
    // omitted.
    expect(screen.getAllByText("interconnect state unknown")).toHaveLength(2);
    expect(screen.queryByText("interconnect up")).not.toBeInTheDocument();
    expect(screen.queryByText("interconnect down")).not.toBeInTheDocument();
  });

  it("isolates one unreachable PVE-side capsule from a sibling's healthy interconnect — capsule reachability and WG state are independent signals", () => {
    useFederationClustersQueryMock.mockReturnValue({
      data: [
        registryCluster({ id: "cl-a", name: "east", wgTunnelId: "tun-1" }),
        registryCluster({ id: "cl-b", name: "west", wgTunnelId: undefined }),
      ],
    });
    useWireGuardTunnelsQueryMock.mockReturnValue({
      data: [
        {
          id: "tun-1",
          node: "pve1",
          ifName: "wg0",
          publicKey: "PK",
          addresses: [],
          peers: [{ publicKey: "P", allowedIps: [], rxBytes: 0, txBytes: 0, external: false, endpointDrifted: false, lastHandshakeUnix: NOW_SEC }],
          status: { interfaceUp: true, peerCount: 1 },
          listenPort: 51820,
          mtu: 1420,
        },
      ],
      isLoading: false,
      isError: false,
    });

    const unreachableWest: ClusterSummary = { ...summary("cl-b", "west"), reachable: false, nodesOnline: 0, nodes: 0 };
    render(<GlobalTopologyView clusters={[summary("cl-a", "east"), unreachableWest]} onDrill={vi.fn()} />);

    // west is unreachable at the PVE layer (its own existing capsule
    // indicator), but that never suppresses east's healthy interconnect.
    expect(screen.getByText("unreachable")).toBeInTheDocument();
    expect(screen.getByText("interconnect up")).toBeInTheDocument();
  });
});
