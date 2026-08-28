// SPDX-License-Identifier: Apache-2.0

// T-3903 acceptance-criteria coverage: node picker + lookup wiring, and
// the reachable/ambiguous/unreachable lookup-result renderings. Network is
// mocked at the ./routeQueries seam (mirrors conntrack/ConntrackExplorer.
// test.tsx's identical pattern) so these tests never touch fetch.
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { RouteLookupResult, RouteNodesResponse, RouteSnapshot } from "../api/types";
import { RouteExplorerPage } from "./RouteExplorerPage";

let nodesResult: { data: RouteNodesResponse | undefined } = { data: { nodes: ["pve1", "pve2"] } };
let snapshotResult: {
  data: RouteSnapshot | undefined;
  isLoading: boolean;
  error: Error | null;
} = { data: undefined, isLoading: false, error: null };
let lookupResult: {
  data: RouteLookupResult | undefined;
  isFetching: boolean;
  error: Error | null;
} = { data: undefined, isFetching: false, error: null };

vi.mock("./routeQueries", () => ({
  useRouteNodesQuery: () => nodesResult,
  useRouteSnapshotQuery: () => snapshotResult,
  useRouteLookupQuery: () => lookupResult,
}));

function snapshot(overrides: Partial<RouteSnapshot> = {}): RouteSnapshot {
  return {
    node: "pve1",
    fib: [
      { afi: "ipv4", table: "main", type: "unicast", dst: "0.0.0.0/0", gateway: "192.168.1.1", dev: "vmbr0" },
      { afi: "ipv4", table: "main", type: "unicast", dst: "192.168.1.0/24", dev: "vmbr0" },
    ],
    rules: [{ afi: "ipv4", priority: 32766, src: "all", table: "main" }],
    frrUnavailable: true,
    ...overrides,
  };
}

function renderPage(initialEntries: string[] = ["/route-explorer"]): void {
  render(
    <MemoryRouter initialEntries={initialEntries}>
      <RouteExplorerPage />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  nodesResult = { data: { nodes: ["pve1", "pve2"] } };
  snapshotResult = { data: snapshot(), isLoading: false, error: null };
  lookupResult = { data: undefined, isFetching: false, error: null };
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("RouteExplorerPage", () => {
  it("renders the page heading and node picker", () => {
    renderPage();
    expect(screen.getByRole("heading", { name: "Route explorer" })).toBeInTheDocument();
    expect(screen.getByLabelText("Node")).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "pve1" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "pve2" })).toBeInTheDocument();
  });

  it("renders the FIB table from the snapshot", () => {
    renderPage();
    expect(screen.getByText("Kernel FIB")).toBeInTheDocument();
    expect(screen.getByText("0.0.0.0/0")).toBeInTheDocument();
    expect(screen.getByText("192.168.1.0/24")).toBeInTheDocument();
  });

  it("shows an FRR-unavailable empty state when the node runs no FRR", () => {
    renderPage();
    expect(screen.getByText(/FRR is not running on this node/)).toBeInTheDocument();
  });

  it("renders RIB entries when FRR is available", () => {
    snapshotResult = {
      data: snapshot({
        frrUnavailable: false,
        rib: [
          {
            afi: "ipv4",
            vrf: "default",
            prefix: "0.0.0.0/0",
            protocol: "kernel",
            nexthops: [{ interface: "vmbr0", ip: "192.168.1.1", active: true, fib: true }],
            selected: true,
            installed: true,
          },
        ],
      }),
      isLoading: false,
      error: null,
    };
    renderPage();
    expect(screen.getByText("default")).toBeInTheDocument();
    expect(screen.getByText("kernel")).toBeInTheDocument();
  });

  it("submits a lookup and renders a reachable result", async () => {
    lookupResult = {
      data: {
        dst: "8.8.8.8",
        reachable: true,
        matchedRoute: { afi: "ipv4", table: "main", type: "unicast", dst: "0.0.0.0/0", gateway: "192.168.1.1", dev: "vmbr0" },
        matchedRule: { afi: "ipv4", priority: 32766, src: "all", table: "main" },
        trace: ["rule priority 32766: from all lookup main", "table main: matched 0.0.0.0/0 via vmbr0"],
      },
      isFetching: false,
      error: null,
    };
    renderPage();
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Destination address"), "8.8.8.8");
    await user.click(screen.getByRole("button", { name: "Which path?" }));

    const resultPanel = screen.getByRole("status");
    expect(resultPanel).toHaveTextContent("would go via");
    expect(resultPanel).toHaveTextContent("8.8.8.8");
    expect(resultPanel).toHaveTextContent("192.168.1.1");
  });

  it("renders an ambiguous lookup result distinctly from unreachable", () => {
    lookupResult = {
      data: { dst: "fe80::1", reachable: false, ambiguous: ["vmbr0", "vmbr2", "vmbr99"] },
      isFetching: false,
      error: null,
    };
    renderPage(["/route-explorer?dst=fe80::1"]);
    expect(screen.getByText(/more than one equally-specific interface/)).toBeInTheDocument();
    expect(screen.getByText(/vmbr0, vmbr2, vmbr99/)).toBeInTheDocument();
  });

  it("renders an unreachable lookup result", () => {
    lookupResult = {
      data: { dst: "10.5.5.5", reachable: false },
      isFetching: false,
      error: null,
    };
    renderPage(["/route-explorer?dst=10.5.5.5"]);
    expect(screen.getByText(/is not reachable from any evaluated routing table/)).toBeInTheDocument();
  });

  it("shows rulesSkipped as a caveat when present", () => {
    lookupResult = {
      data: {
        dst: "10.0.0.5",
        reachable: false,
        rulesSkipped: ["priority 100: from 10.0.0.0/24 lookup 200 (source-scoped rule, not evaluated)"],
      },
      isFetching: false,
      error: null,
    };
    renderPage(["/route-explorer?dst=10.0.0.5"]);
    expect(screen.getByText(/Not evaluated:/)).toBeInTheDocument();
  });

  it("disables the lookup submit button until a destination is entered", () => {
    renderPage();
    expect(screen.getByRole("button", { name: "Which path?" })).toBeDisabled();
  });
});
