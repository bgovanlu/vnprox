import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { EvpnStatus } from "../api/types";
import { EvpnView } from "./EvpnView";

// T-404 acceptance criterion 1's fixture: established/idle/active states
// across three nodes, matching evpn-lab.yaml's declared FRR sessions
// (pve1<->pve2 Established, pve1<->pve3 Idle (Admin), pve2<->pve3 Active).
const status: EvpnStatus = {
  generatedAt: 1_752_000_000,
  nodes: [
    {
      node: "pve1",
      frrInstalled: true,
      routerId: "10.20.0.11",
      asn: 65001,
      peers: [
        { peerAddr: "10.20.0.12", peerNode: "pve2", addressFamily: "l2VpnEvpn", state: "Established", pfxRcd: 6, pfxSnt: 6, uptimeSecs: 5025 },
        { peerAddr: "10.20.0.13", peerNode: "pve3", addressFamily: "l2VpnEvpn", state: "Idle", stateReason: "Admin" },
      ],
      vnis: [{ vni: 10001, type: "L2", vxlanIf: "vxlan10001", numMacs: 12, numArpNd: 4 }],
    },
    {
      node: "pve2",
      frrInstalled: true,
      peers: [
        { peerAddr: "10.20.0.11", peerNode: "pve1", state: "Established" },
        { peerAddr: "10.20.0.13", peerNode: "pve3", state: "Active" },
      ],
      vnis: [],
    },
    { node: "pve3", frrInstalled: false, peers: [], vnis: [] },
  ],
  exitNodes: [{ zone: "evpnz", node: "pve3", healthy: false, detail: "session to 10.20.0.11 is Idle, not Established" }],
  controllers: [],
  findings: [
    { id: "evpn_bgp_flapping:pve1:10.20.0.13", code: "evpn_bgp_flapping", severity: "warning", node: "pve1", peerAddr: "10.20.0.13", detail: "session pve1<->10.20.0.13 changed state 4 times in the last 10m0s" },
  ],
};

function renderView() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <EvpnView />
    </QueryClientProvider>,
  );
}

describe("EvpnView", () => {
  beforeEach(() => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() =>
        Promise.resolve(
          new Response(JSON.stringify(status), { status: 200, headers: { "Content-Type": "application/json" } }),
        ),
      ),
    );
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("renders the peering matrix with every node and observed peer address", async () => {
    renderView();
    await waitFor(() => {
      expect(screen.getByRole("table", { name: "EVPN peering matrix" })).toBeInTheDocument();
    });
    const table = screen.getByRole("table", { name: "EVPN peering matrix" });
    expect(within(table).getByText("pve1")).toBeInTheDocument();
    expect(within(table).getByText("pve2")).toBeInTheDocument();
    expect(within(table).getByText("pve3")).toBeInTheDocument();
    expect(within(table).getByText("no EVPN")).toBeInTheDocument(); // pve3
    expect(within(table).getAllByText("Established")).toHaveLength(2);
    expect(within(table).getByText("Idle")).toBeInTheDocument();
    expect(within(table).getByText("Active")).toBeInTheDocument();
  });

  it("clicking a matrix cell shows session detail matching the fixture JSON", async () => {
    renderView();
    await waitFor(() => {
      expect(screen.getByRole("table", { name: "EVPN peering matrix" })).toBeInTheDocument();
    });
    const table = screen.getByRole("table", { name: "EVPN peering matrix" });
    const [establishedCell] = within(table).getAllByText("Established");
    if (!establishedCell) throw new Error("expected at least one Established cell in the matrix");
    await userEvent.click(establishedCell);

    await waitFor(() => {
      expect(screen.getByRole("heading", { name: "pve1 ↔ 10.20.0.12" })).toBeInTheDocument();
    });
    expect(screen.getByText(/Peer node: pve2/)).toBeInTheDocument();
    const pfxRcdRow = screen.getByText("Prefixes received").closest("div");
    expect(pfxRcdRow).not.toBeNull();
    expect(within(pfxRcdRow as HTMLElement).getByText("6")).toBeInTheDocument();
    expect(screen.getByText("1h23m")).toBeInTheDocument(); // uptime 5025s
  });

  it("session detail shows the last-error state reason for a down session", async () => {
    renderView();
    await waitFor(() => {
      expect(screen.getByRole("table", { name: "EVPN peering matrix" })).toBeInTheDocument();
    });
    const table = screen.getByRole("table", { name: "EVPN peering matrix" });
    await userEvent.click(screen.getByText("Idle"));
    await waitFor(() => {
      expect(screen.getByRole("heading", { name: "pve1 ↔ 10.20.0.13" })).toBeInTheDocument();
    });
    // "Admin" appears both as the last-error field value and inline next to
    // the state — assert the dedicated "Last error" field row specifically.
    const lastErrorRow = screen.getByText("Last error").closest("div");
    expect(lastErrorRow).not.toBeNull();
    expect(within(lastErrorRow as HTMLElement).getByText("Admin")).toBeInTheDocument();
    void table;
  });

  it("renders the VNI list", async () => {
    renderView();
    await waitFor(() => {
      expect(screen.getByRole("table", { name: "EVPN VNI list" })).toBeInTheDocument();
    });
    const vniTable = screen.getByRole("table", { name: "EVPN VNI list" });
    expect(within(vniTable).getByText("10001")).toBeInTheDocument();
    expect(within(vniTable).getByText("vxlan10001")).toBeInTheDocument();
  });

  it("renders exit-node health, including an unhealthy exit node", async () => {
    renderView();
    await waitFor(() => {
      expect(screen.getByText("unhealthy")).toBeInTheDocument();
    });
    expect(screen.getByText(/session to 10.20.0.11 is Idle/)).toBeInTheDocument();
  });

  it("T-404 AC3: a flapping-session finding renders in the findings list", async () => {
    renderView();
    await waitFor(() => {
      expect(screen.getByText(/changed state 4 times/)).toBeInTheDocument();
    });
    expect(screen.getByText("evpn_bgp_flapping")).toBeInTheDocument();
  });
});

describe("EvpnView with no findings", () => {
  it("shows the empty state instead of a findings list for stable sessions", async () => {
    const stable: EvpnStatus = { ...status, findings: [] };
    vi.stubGlobal(
      "fetch",
      vi.fn(() =>
        Promise.resolve(
          new Response(JSON.stringify(stable), { status: 200, headers: { "Content-Type": "application/json" } }),
        ),
      ),
    );
    renderView();
    await waitFor(() => {
      expect(screen.getByText("No flapping sessions")).toBeInTheDocument();
    });
    vi.unstubAllGlobals();
  });
});
