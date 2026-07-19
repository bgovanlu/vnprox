// T-1403 acceptance criterion 4's frontend half: the Edge layer flags a
// port-forward rule pointing at a currently powered-off guest distinctly.
// Network is mocked at the ./edgeQueries seam (mirrors
// conntrack/ConntrackExplorer.test.tsx's identical pattern) so these tests
// never touch fetch.
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { EdgeNATView, EdgeRoutesView } from "../api/types";
import { EdgeCockpit } from "./EdgeCockpit";

let routesResult: { data: EdgeRoutesView | undefined; isLoading: boolean; error: Error | null } = {
  data: { defaultRoutes: [], staticRoutes: [], generatedAt: 0 },
  isLoading: false,
  error: null,
};
let natResult: { data: EdgeNATView | undefined; isLoading: boolean; error: Error | null } = {
  data: { masquerade: [], portForwards: [], sdnSimpleZoneNat: [], generatedAt: 0 },
  isLoading: false,
  error: null,
};

vi.mock("./edgeQueries", () => ({
  useEdgeRoutesQuery: () => routesResult,
  useEdgeNATQuery: () => natResult,
}));

describe("EdgeCockpit", () => {
  it("renders default routes and static routes", () => {
    routesResult = {
      data: {
        defaultRoutes: [{ node: "pve1", iface: "vmbr0", gateway: "203.0.113.1" }],
        staticRoutes: [{ id: "lab-route", node: "pve1", iface: "vmbr0", destCidr: "10.10.0.0/24", gateway: "203.0.113.5", metric: 50 }],
        generatedAt: 1,
      },
      isLoading: false,
      error: null,
    };
    render(<EdgeCockpit />);
    expect(screen.getByText("203.0.113.1")).toBeInTheDocument();
    expect(screen.getByText("10.10.0.0/24")).toBeInTheDocument();
  });

  it("flags a port-forward targeting a powered-off guest distinctly", () => {
    routesResult = { data: { defaultRoutes: [], staticRoutes: [], generatedAt: 1 }, isLoading: false, error: null };
    natResult = {
      data: {
        masquerade: [{ id: "masq1", node: "pve1", iface: "vmbr0", sourceCidr: "192.168.1.0/24" }],
        portForwards: [
          {
            id: "pf-web", node: "pve1", iface: "vmbr0", proto: "tcp", extPort: 8080,
            intIp: "192.168.1.50", intPort: 80, targetGuestRef: "guest:pve1:100", targetGuestPoweredOff: false,
          },
          {
            id: "pf-ssh", node: "pve1", iface: "vmbr0", proto: "tcp", extPort: 2222,
            intIp: "192.168.1.99", intPort: 22, targetGuestRef: "guest:pve1:101", targetGuestPoweredOff: true,
          },
        ],
        sdnSimpleZoneNat: [{ zone: "zone1", vnet: "vnet1", subnet: "10.20.0.0/24", gateway: "10.20.0.1" }],
        generatedAt: 1,
      },
      isLoading: false,
      error: null,
    };
    render(<EdgeCockpit />);

    // The powered-off target is flagged distinctly (a labeled status pill),
    // not just listed the same way as the healthy one.
    const flagged = screen.getByText(/guest:pve1:101 — powered off/);
    expect(flagged).toBeInTheDocument();
    expect(flagged.getAttribute("role")).toBe("status");

    const healthy = screen.getByText("guest:pve1:100");
    expect(healthy.getAttribute("role")).not.toBe("status");

    // The inbound-exposure summary counts both port-forwards and calls out
    // the powered-off one.
    expect(screen.getByText("2 port-forwards total")).toBeInTheDocument();
    expect(screen.getByText(/1 to a powered-off guest/)).toBeInTheDocument();

    expect(screen.getByText("10.20.0.0/24")).toBeInTheDocument();
  });

  it("shows an unresolved target when a port-forward's IP correlates to no known guest", () => {
    routesResult = { data: { defaultRoutes: [], staticRoutes: [], generatedAt: 1 }, isLoading: false, error: null };
    natResult = {
      data: {
        masquerade: [],
        portForwards: [{ id: "pf-x", node: "pve1", iface: "vmbr0", proto: "tcp", extPort: 9999, intIp: "192.168.1.5", intPort: 22 }],
        sdnSimpleZoneNat: [],
        generatedAt: 1,
      },
      isLoading: false,
      error: null,
    };
    render(<EdgeCockpit />);
    expect(screen.getByText("unresolved")).toBeInTheDocument();
  });

  it("shows a loading state and an error state", () => {
    routesResult = { data: undefined, isLoading: true, error: null };
    natResult = { data: undefined, isLoading: true, error: null };
    const { rerender } = render(<EdgeCockpit />);
    expect(screen.getByText("Loading…")).toBeInTheDocument();

    routesResult = { data: undefined, isLoading: false, error: new Error("boom") };
    natResult = { data: undefined, isLoading: false, error: null };
    rerender(<EdgeCockpit />);
    expect(screen.getByText("Could not load edge data")).toBeInTheDocument();
  });
});
