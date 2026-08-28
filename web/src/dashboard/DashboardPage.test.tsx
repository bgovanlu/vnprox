// SPDX-License-Identifier: Apache-2.0

// T-904 acceptance criteria 2-4: per-tile rendering against a
// three-node-vlan-shaped fixture (AC2), deep-link navigation out of each
// tile (AC3), and explicit "all clear" empty states against a
// single-node-shaped fixture with nothing open/pending (AC4). Every
// backend call is mocked at the api/* module boundary — the same
// "TopBar.test.tsx pattern" ManagementPage.test.tsx and
// FindingsStreamPanel.test.tsx already use — so this never issues a real
// fetch or opens a real WebSocket.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../api/client";
import { ToastProvider } from "../components/Toast";
import type {
  Changeset,
  DashboardTile,
  FlowsPage,
  LiveMetric,
  ProtectedInterfacesStatusResponse,
  StreamFinding,
  TopologyResponse,
} from "../api/types";
import type { AuditListResponse } from "../api/audit";
import { useChangesetDrawerStore } from "../changesets/store";
import { DashboardPage } from "./DashboardPage";

// T-3911: the dashboard grid persists its tile layout through the
// pre-existing per-user `layouts` mechanism (reserved name "dashboard")
// and fetches plugin tiles from GET /dashboard/tiles — both go through
// apiFetch directly (dashboardLayoutQueries.ts, api/dashboardTiles.ts,
// api/layouts.ts's saveLayout), so this file mocks the shared client
// module itself (CertificatesPage.test.tsx's precedent) rather than a
// higher-level api/* module, keeping ApiError/API_BASE real via
// importOriginal. Every AC2-4 test below never customises the layout, so
// the default here always answers "no saved layout yet" (a 404, exactly
// like a fresh user) and an empty plugin-tile list — the grid falls back
// to its built-in default order, byte-identical to the pre-T-3911 static
// grid these tests were originally written against.
const apiFetch = vi.hoisted(() => vi.fn());
vi.mock("../api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/client")>();
  return { ...actual, apiFetch };
});

let mockDashboardTiles: DashboardTile[] = [];

function defaultApiFetchImpl(path: string, options?: { method?: string; json?: unknown }): Promise<unknown> {
  if (path === "/dashboard/tiles") {
    return Promise.resolve({ items: mockDashboardTiles });
  }
  if (path === "/layouts/dashboard") {
    if (options?.method === "PUT") {
      return Promise.resolve({ name: "dashboard", layout: options.json, updatedAt: Date.now() });
    }
    return Promise.reject(new ApiError(404, "not_found", "no saved layout with that name"));
  }
  return Promise.reject(new Error(`DashboardPage.test.tsx: unexpected apiFetch call: ${path}`));
}

let mockFindings: StreamFinding[] = [];
let mockChangesets: Changeset[] = [];
let mockMgmtStatus: ProtectedInterfacesStatusResponse = { source: "confirmed", nodes: {} };
let mockTopology: TopologyResponse = { nodes: [], edges: [], layers: [], generatedAt: 0 };
let mockLiveMetrics: LiveMetric[] = [];
let mockAudit: AuditListResponse = { items: [] };
let mockFlows: FlowsPage = { items: [] };

vi.mock("../api/findings", () => ({
  fetchFindings: () => Promise.resolve(mockFindings),
  fixFinding: vi.fn(),
}));

vi.mock("../api/changesets", () => ({
  listChangesets: () => Promise.resolve(mockChangesets),
  getChangeset: vi.fn(),
  createChangeset: vi.fn(),
  updateChangeset: vi.fn(),
  discardChangeset: vi.fn(),
  validateChangeset: vi.fn(),
  diffChangeset: vi.fn(),
  applyChangeset: vi.fn(),
  confirmChangeset: vi.fn(),
  rollbackChangeset: vi.fn(),
}));

vi.mock("../api/protectedInterfaces", () => ({
  fetchMgmtStatus: () => Promise.resolve(mockMgmtStatus),
  fetchProtectedInterfaces: vi.fn(),
  fetchProtectedInterfacesSuggest: vi.fn(),
  saveProtectedInterfaces: vi.fn(),
}));

vi.mock("../api/topology", () => ({
  fetchTopology: () => Promise.resolve(mockTopology),
  fetchInventoryDetail: vi.fn(),
  searchInventory: vi.fn(),
}));

vi.mock("../api/metrics", () => ({
  fetchMetricsLive: () => Promise.resolve(mockLiveMetrics),
  fetchMetricsHistory: vi.fn(),
}));

vi.mock("../api/audit", () => ({
  fetchAudit: () => Promise.resolve(mockAudit),
}));

vi.mock("../api/flows", () => ({
  fetchFlows: () => Promise.resolve(mockFlows),
}));

vi.mock("../api/ws", () => ({
  createWsClient: () => ({ subscribe: () => () => undefined, status: () => "closed", close: () => undefined }),
  defaultWsUrl: () => "ws://unused",
}));

function renderDashboard() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <ToastProvider>
        <MemoryRouter initialEntries={["/"]}>
          <Routes>
            <Route path="/" element={<DashboardPage />} />
            <Route path="/tools" element={<div>Tools page</div>} />
            <Route path="/topology" element={<div>Topology page</div>} />
            <Route path="/management" element={<div>Management page</div>} />
            <Route path="/audit" element={<div>Audit page</div>} />
            <Route path="/flows" element={<div>Flows page</div>} />
          </Routes>
        </MemoryRouter>
      </ToastProvider>
    </QueryClientProvider>,
  );
}

function tile(name: string) {
  return screen.getByRole("region", { name });
}

const THREE_NODE_FINDINGS: StreamFinding[] = [
  { id: "drift:1", source: "drift", check: "bridge_divergence", severity: "warning", detail: "vmbr0 vlan-aware differs", nodes: ["pve1"], fixable: false, docsLink: "docs/x" },
  { id: "drift:2", source: "drift", check: "mtu_consistency", severity: "error", detail: "MTU mismatch on bond0", nodes: ["pve2"], fixable: false, docsLink: "docs/y" },
  { id: "health:1", source: "health", check: "bond_slave_down", severity: "error", detail: "bond0 slave eno2 is down", nodes: ["pve1"], fixable: false, docsLink: "docs/z" },
  { id: "lldp:1", source: "lldp", check: "vlan_cross_check_missing_on_switch", severity: "info", detail: "vlan 20 not advertised", nodes: ["pve3"], fixable: false, docsLink: "docs/w" },
];

const THREE_NODE_CHANGESETS: Changeset[] = [
  { id: "cs1", title: "add vlan 30", author: "root", status: "draft", ops: [], findings: [], createdAt: 1, updatedAt: 5 },
  { id: "cs2", title: "bond eno2", author: "root", status: "awaiting_confirm", ops: [], findings: [], createdAt: 1, updatedAt: 10, confirmDeadline: 999 },
  { id: "cs3", title: "old committed change", author: "root", status: "committed", ops: [], findings: [], createdAt: 1, updatedAt: 1 },
];

const THREE_NODE_MGMT: ProtectedInterfacesStatusResponse = {
  source: "confirmed",
  nodes: {
    pve1: [{ ref: "bridge:pve1:vmbr0", roles: ["mgmt"], path: ["physnic:pve1:eno1"], redundant: false }],
    pve2: [{ ref: "bridge:pve2:vmbr0", roles: ["mgmt"], path: ["physnic:pve2:eno1", "physnic:pve2:eno2"], redundant: true }],
    pve3: [{ ref: "bridge:pve3:vmbr0", roles: ["mgmt"], path: ["physnic:pve3:eno1", "physnic:pve3:eno2"], redundant: true }],
  },
};

const RATES = (rxBps: number, txBps: number) => ({
  rxBps,
  txBps,
  rxPps: 0,
  txPps: 0,
  rxErrsPerSec: 0,
  txErrsPerSec: 0,
  rxDropPerSec: 0,
  txDropPerSec: 0,
});

const THREE_NODE_TOPOLOGY: TopologyResponse = {
  generatedAt: 1,
  layers: ["phys", "l2", "sdn", "guest"],
  nodes: [
    { id: "bridge:pve1:vmbr0", kind: "bridge", label: "vmbr0", layer: "l2", nodeGroup: "pve1", status: "ok", badges: [] },
    { id: "bridge:pve2:vmbr0", kind: "bridge", label: "vmbr0", layer: "l2", nodeGroup: "pve2", status: "ok", badges: [] },
    { id: "guest-nic:pve1:100/net0", kind: "guest-nic", label: "vm100/net0", layer: "guest", nodeGroup: "pve1", status: "ok", badges: [] },
    { id: "guest-nic:pve1:101/net0", kind: "guest-nic", label: "vm101/net0", layer: "guest", nodeGroup: "pve1", status: "ok", badges: [] },
    { id: "guest-nic:pve2:200/net0", kind: "guest-nic", label: "vm200/net0", layer: "guest", nodeGroup: "pve2", status: "ok", badges: [] },
  ],
  edges: [
    { from: "guest-nic:pve1:100/net0", to: "bridge:pve1:vmbr0", kind: "attached-to", status: "ok", badges: [] },
    { from: "guest-nic:pve1:101/net0", to: "bridge:pve1:vmbr0", kind: "attached-to", status: "ok", badges: [] },
    { from: "guest-nic:pve2:200/net0", to: "bridge:pve2:vmbr0", kind: "attached-to", status: "ok", badges: [] },
  ],
};

const THREE_NODE_LIVE: LiveMetric[] = [
  { ref: "guest-nic:pve1:100/net0", at: 1, rates: RATES(5_000_000, 1_000_000) },
  { ref: "guest-nic:pve1:101/net0", at: 1, rates: RATES(500_000, 100_000) },
  { ref: "guest-nic:pve2:200/net0", at: 1, rates: RATES(1_000, 500) },
];

const THREE_NODE_FLOWS: FlowsPage = {
  items: [
    { at: 1000, node: "pve1", srcIp: "10.10.10.1", dstIp: "10.10.10.2", proto: 17, bytes: 8000, packets: 8, source: "netflow5", serviceClass: "corosync" },
    { at: 1010, node: "pve1", srcIp: "10.10.10.1", dstIp: "10.10.10.2", proto: 17, bytes: 2000, packets: 2, source: "netflow5", serviceClass: "corosync" },
    { at: 1005, node: "pve2", srcIp: "10.20.0.5", dstIp: "10.20.0.9", proto: 6, bytes: 1000, packets: 3, source: "netflow5", serviceClass: "migration" },
  ],
};

const THREE_NODE_AUDIT: AuditListResponse = {
  items: [
    { id: 1, at: 1_700_000_000, username: "root@pam", action: "changeset.apply", target: "cs2", result: "ok" },
    { id: 2, at: 1_700_000_100, username: "root@pam", action: "login", result: "ok" },
  ],
};

function seedThreeNodeFixture(): void {
  mockFindings = THREE_NODE_FINDINGS;
  mockChangesets = THREE_NODE_CHANGESETS;
  mockMgmtStatus = THREE_NODE_MGMT;
  mockTopology = THREE_NODE_TOPOLOGY;
  mockLiveMetrics = THREE_NODE_LIVE;
  mockAudit = THREE_NODE_AUDIT;
  mockFlows = THREE_NODE_FLOWS;
}

function seedSingleNodeCleanFixture(): void {
  mockFindings = [];
  mockChangesets = [];
  mockMgmtStatus = { source: "confirmed", nodes: { pve1: [{ ref: "bridge:pve1:vmbr0", roles: ["mgmt"], path: ["physnic:pve1:eno1", "physnic:pve1:eno2"], redundant: true }] } };
  mockTopology = { generatedAt: 1, layers: ["phys", "l2", "sdn", "guest"], nodes: [], edges: [] };
  mockLiveMetrics = [];
  mockAudit = { items: [] };
  mockFlows = { items: [] };
}

beforeEach(() => {
  useChangesetDrawerStore.setState({ activeId: undefined, drawerOpen: false, reviewRequested: false, warningsAcknowledged: false });
  mockDashboardTiles = [];
  apiFetch.mockReset();
  apiFetch.mockImplementation(defaultApiFetchImpl);
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("DashboardPage tiles render real data (AC2, three-node-vlan-shaped fixture)", () => {
  beforeEach(seedThreeNodeFixture);

  it("findings-by-severity tile groups by severity", async () => {
    renderDashboard();
    const region = tile("Findings by severity");
    await waitFor(() => {
      expect(within(region).getByText("2")).toBeInTheDocument(); // 2 errors
    });
    expect(within(region).getByText("Error")).toBeInTheDocument();
    expect(within(region).getByText("Warning")).toBeInTheDocument();
    expect(within(region).getByText("Info")).toBeInTheDocument();
  });

  it("drift-status tile counts only drift-sourced findings", async () => {
    renderDashboard();
    const region = tile("Drift status");
    await waitFor(() => {
      expect(region.textContent).toContain("2 drift findings across 2 nodes");
    });
    expect(within(region).getByText("pve1, pve2")).toBeInTheDocument();
  });

  it("pending-changesets tile lists non-terminal changesets only, with awaiting-confirm called out", async () => {
    renderDashboard();
    const region = tile("Pending changesets");
    await waitFor(() => {
      expect(within(region).getByText("add vlan 30")).toBeInTheDocument();
    });
    expect(within(region).getByText("bond eno2")).toBeInTheDocument();
    expect(within(region).queryByText("old committed change")).not.toBeInTheDocument();
    expect(within(region).getByText("1 awaiting confirm")).toBeInTheDocument();
  });

  it("mgmt-path redundancy tile counts non-redundant nodes", async () => {
    renderDashboard();
    const region = tile("Management-path redundancy");
    await waitFor(() => {
      expect(region.textContent).toContain("1 of 3 nodes have a single-path management uplink");
    });
    expect(within(region).getByText("pve1")).toBeInTheDocument();
  });

  it("top-talkers tile ranks guest NICs on the busiest bridge", async () => {
    renderDashboard();
    const region = tile("Top talkers");
    await waitFor(() => {
      expect(within(region).getByText("vm100/net0")).toBeInTheDocument();
    });
    // vm100 (5Mbps+1Mbps) outranks vm101 (500Kbps+100Kbps) on the same
    // (busiest) bridge vmbr0@pve1; vm200@pve2's bridge is far less busy.
    expect(within(region).getByText("vm101/net0")).toBeInTheDocument();
    expect(within(region).queryByText("vm200/net0")).not.toBeInTheDocument();
  });

  it("recent-audit tile renders the last entries", async () => {
    renderDashboard();
    const region = tile("Recent audit activity");
    await waitFor(() => {
      expect(within(region).getByText(/changeset.apply/)).toBeInTheDocument();
    });
    expect(within(region).getByText(/login/)).toBeInTheDocument();
  });

  it("service-network-traffic tile ranks serviceClass by bytes/sec (T-1504)", async () => {
    renderDashboard();
    const region = tile("Service-network traffic");
    await waitFor(() => {
      expect(within(region).getByText("Corosync")).toBeInTheDocument();
    });
    // Corosync (8000+2000 bytes over 10s = 1000 B/s = 8000bps) outranks
    // migration (1000 bytes, single-instant sample -> floored 1s window).
    expect(within(region).getByText("Migration")).toBeInTheDocument();
    const items = within(region).getAllByRole("listitem");
    expect(items[0]?.textContent).toContain("Corosync");
  });
});

describe("DashboardPage deep links (AC3)", () => {
  beforeEach(seedThreeNodeFixture);

  it("findings-by-severity tile deep-links to /tools", async () => {
    const user = userEvent.setup();
    renderDashboard();
    await user.click(within(tile("Findings by severity")).getByRole("button", { name: "Open findings" }));
    await waitFor(() => {
      expect(screen.getByText("Tools page")).toBeInTheDocument();
    });
  });

  it("drift-status tile deep-links to /tools", async () => {
    const user = userEvent.setup();
    renderDashboard();
    await user.click(within(tile("Drift status")).getByRole("button", { name: "Open findings" }));
    await waitFor(() => {
      expect(screen.getByText("Tools page")).toBeInTheDocument();
    });
  });

  it("pending-changesets tile opens the changeset drawer on its most-recently-updated pending changeset", async () => {
    const user = userEvent.setup();
    renderDashboard();
    await user.click(within(tile("Pending changesets")).getByRole("button", { name: "Open drawer" }));
    await waitFor(() => {
      expect(useChangesetDrawerStore.getState().activeId).toBe("cs2"); // updatedAt 10, most recent pending
      expect(useChangesetDrawerStore.getState().drawerOpen).toBe(true);
    });
  });

  it("mgmt-path redundancy tile deep-links to /management", async () => {
    const user = userEvent.setup();
    renderDashboard();
    await user.click(within(tile("Management-path redundancy")).getByRole("button", { name: "Open management" }));
    await waitFor(() => {
      expect(screen.getByText("Management page")).toBeInTheDocument();
    });
  });

  it("top-talkers tile deep-links to /topology", async () => {
    const user = userEvent.setup();
    renderDashboard();
    await user.click(within(tile("Top talkers")).getByRole("button", { name: "Open topology" }));
    await waitFor(() => {
      expect(screen.getByText("Topology page")).toBeInTheDocument();
    });
  });

  it("recent-audit tile deep-links to /audit", async () => {
    const user = userEvent.setup();
    renderDashboard();
    await user.click(within(tile("Recent audit activity")).getByRole("button", { name: "Open audit log" }));
    await waitFor(() => {
      expect(screen.getByText("Audit page")).toBeInTheDocument();
    });
  });

  it("service-network-traffic tile deep-links to /flows", async () => {
    const user = userEvent.setup();
    renderDashboard();
    await waitFor(() => {
      expect(within(tile("Service-network traffic")).getByText("Corosync")).toBeInTheDocument();
    });
    await user.click(within(tile("Service-network traffic")).getByRole("button", { name: "Open flow explorer" }));
    await waitFor(() => {
      expect(screen.getByText("Flows page")).toBeInTheDocument();
    });
  });
});

describe("DashboardPage empty states (AC4, single-node-shaped fixture with nothing open/pending)", () => {
  beforeEach(seedSingleNodeCleanFixture);

  it("every tile renders an explicit 'all clear' empty state, never blank", async () => {
    renderDashboard();

    await waitFor(() => {
      expect(within(tile("Findings by severity")).getByText("All clear")).toBeInTheDocument();
    });
    expect(within(tile("Drift status")).getByText("No drift detected")).toBeInTheDocument();
    expect(within(tile("Pending changesets")).getByText("Nothing pending")).toBeInTheDocument();
    expect(within(tile("Management-path redundancy")).getByText("All nodes redundant")).toBeInTheDocument();
    expect(within(tile("Top talkers")).getByText("No measurable traffic")).toBeInTheDocument();
    expect(within(tile("Service-network traffic")).getByText("No classified traffic")).toBeInTheDocument();
    expect(within(tile("Recent audit activity")).getByText("No audit activity yet")).toBeInTheDocument();

    // None of these should ever render an error string in the clean case.
    expect(screen.queryByText(/Could not load/)).not.toBeInTheDocument();
  });

  it("pending-changesets tile's empty-state open button still opens the (empty) drawer harmlessly", async () => {
    const user = userEvent.setup();
    renderDashboard();
    await waitFor(() => {
      expect(within(tile("Pending changesets")).getByText("Nothing pending")).toBeInTheDocument();
    });
    await user.click(within(tile("Pending changesets")).getByRole("button", { name: "Open drawer" }));
    expect(useChangesetDrawerStore.getState().drawerOpen).toBe(true);
    expect(useChangesetDrawerStore.getState().activeId).toBeUndefined();
  });
});
