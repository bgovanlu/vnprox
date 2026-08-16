// T-1403 acceptance criterion 4's frontend half: the Edge layer flags a
// port-forward rule pointing at a currently powered-off guest distinctly.
// Network is mocked at the ./edgeQueries seam (mirrors
// conntrack/ConntrackExplorer.test.tsx's identical pattern) so these tests
// never touch fetch.
import { render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type {
  EdgeNATView,
  EdgeRoutesView,
  IngressStatusView,
  IngressTargetsListResponse,
  MeResponse,
  WanStatus,
  WanTargetsView,
} from "../api/types";
import { ToastProvider } from "../components/Toast";
import { EdgeCockpit } from "./EdgeCockpit";

function renderCockpit() {
  return render(
    <ToastProvider>
      <EdgeCockpit />
    </ToastProvider>,
  );
}

const fullSession: MeResponse = {
  user: { username: "root", realm: "pam" },
  caps: { "": { netRead: true, netWrite: true, sdnRead: true, sdnWrite: true, fwRead: true, fwWrite: true, guestNet: true, audit: true, capture: false } },
};

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
let ingressTargetsResult: { data: IngressTargetsListResponse | undefined; isLoading: boolean; error: Error | null } = {
  data: { items: [] },
  isLoading: false,
  error: null,
};
let ingressStatusResult: { data: IngressStatusView | undefined; isLoading: boolean; error: Error | null } = {
  data: { targets: [], chains: [], generatedAt: 0 },
  isLoading: false,
  error: null,
};

// T-3004 added the WAN health panel to this page; its three hooks live in
// the same module, and this mock is exhaustive, so they are stubbed here
// too. WanHealthPanel has its own test file — these stubs only need to keep
// the panel mountable so the Edge assertions below stay about Edge.
const wanStatusResult: { data: WanStatus | undefined; isLoading: boolean; error: Error | null } = {
  data: { verdict: "no_targets", summary: "No WAN reference targets are configured yet.", uplinks: [], generatedAt: 0 },
  isLoading: false,
  error: null,
};
const wanTargetsResult: { data: WanTargetsView | undefined } = { data: { node: "pve1", targets: [] } };

vi.mock("./edgeQueries", () => ({
  useEdgeRoutesQuery: () => routesResult,
  useEdgeNATQuery: () => natResult,
  useIngressTargetsQuery: () => ingressTargetsResult,
  useIngressStatusQuery: () => ingressStatusResult,
  useCreateIngressTargetMutation: () => ({ mutate: vi.fn(), isPending: false }),
  useDeleteIngressTargetMutation: () => ({ mutate: vi.fn(), isPending: false }),
  useWanStatusQuery: () => wanStatusResult,
  useWanTargetsQuery: () => wanTargetsResult,
  useReplaceWanTargetsMutation: () => ({ mutate: vi.fn(), isPending: false }),
}));

vi.mock("../api/useSession", () => ({
  useSession: () => ({ data: fullSession }),
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
    renderCockpit();
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
    renderCockpit();

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
    renderCockpit();
    expect(screen.getByText("unresolved")).toBeInTheDocument();
  });

  it("shows a loading state and an error state", () => {
    routesResult = { data: undefined, isLoading: true, error: null };
    natResult = { data: undefined, isLoading: true, error: null };
    const { rerender } = renderCockpit();
    expect(screen.getByText("Loading…")).toBeInTheDocument();

    routesResult = { data: undefined, isLoading: false, error: new Error("boom") };
    natResult = { data: undefined, isLoading: false, error: null };
    rerender(
      <ToastProvider>
        <EdgeCockpit />
      </ToastProvider>,
    );
    expect(screen.getByText("Could not load edge data")).toBeInTheDocument();
  });

  it("renders the WAN -> port-forward -> proxy guest -> backend guest chain when a port-forward and an ingress target line up (T-1406 AC3)", () => {
    routesResult = { data: { defaultRoutes: [], staticRoutes: [], generatedAt: 1 }, isLoading: false, error: null };
    natResult = { data: { masquerade: [], portForwards: [], sdnSimpleZoneNat: [], generatedAt: 1 }, isLoading: false, error: null };
    ingressTargetsResult = {
      data: { items: [{ id: "ing1", kind: "haproxy", address: "http://10.0.0.20:8404", addedBy: "alice", addedAt: 1, hasCredential: false }] },
      isLoading: false,
      error: null,
    };
    ingressStatusResult = {
      data: {
        targets: [{ id: "ing1", kind: "haproxy", address: "http://10.0.0.20:8404", reachable: true, backends: [{ address: "10.0.0.5:8080", guestRef: "guest:pve1:201", healthy: true }] }],
        chains: [
          {
            portForwardId: "pf-proxy", node: "pve1", proto: "tcp", extPort: 443,
            proxyGuestRef: "guest:pve1:200", targetId: "ing1", targetKind: "haproxy",
            backends: [{ address: "10.0.0.5:8080", guestRef: "guest:pve1:201", healthy: true }],
          },
        ],
        generatedAt: 1,
      },
      isLoading: false,
      error: null,
    };
    renderCockpit();

    const chain = screen.getByRole("group", { name: "Ingress chain for port forward pf-proxy" });
    expect(chain).toBeInTheDocument();
    expect(within(chain).getByText("WAN")).toBeInTheDocument();
    expect(within(chain).getByText("tcp/443")).toBeInTheDocument();
    expect(within(chain).getByText("guest:pve1:200")).toBeInTheDocument();
    expect(within(chain).getByText("haproxy (ing1)")).toBeInTheDocument();
    expect(within(chain).getByText("guest:pve1:201")).toBeInTheDocument();
  });

  it("shows no connected chains when no port-forward and ingress target line up", () => {
    routesResult = { data: { defaultRoutes: [], staticRoutes: [], generatedAt: 1 }, isLoading: false, error: null };
    natResult = { data: { masquerade: [], portForwards: [], sdnSimpleZoneNat: [], generatedAt: 1 }, isLoading: false, error: null };
    ingressTargetsResult = { data: { items: [] }, isLoading: false, error: null };
    ingressStatusResult = { data: { targets: [], chains: [], generatedAt: 1 }, isLoading: false, error: null };
    renderCockpit();
    expect(screen.getByText("No connected ingress chains")).toBeInTheDocument();
  });
});
