// SPDX-License-Identifier: Apache-2.0

// T-3906: composition + degradation coverage for the guest ego view. Every
// data-source hook is mocked at the module boundary (mirrors guests/
// GuestsPage.test.tsx's own convention) so each test controls exactly one
// panel's degrade-vs-empty state without needing a real backend.
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { GuestEgoView } from "./GuestEgoView";
import type { GuestNicRow } from "../guests/guestNics";
import type {
  ConntrackPage,
  EntityDetail,
  GuestRulesetResponse,
  StreamFinding,
  SimulateResult,
} from "../api/types";

const nicRows: GuestNicRow[] = [
  { ref: "guest-nic:pve1:100/net0", label: "web01/net0", node: "pve1", bridgeOrVnet: "bridge:pve1:vmbr0", vid: 10, linkDown: false },
  { ref: "guest-nic:pve1:101/net0", label: "db01/net0", node: "pve1", bridgeOrVnet: "bridge:pve1:vmbr0", linkDown: false },
];

vi.mock("../guests/queries", () => ({
  useAllGuestNicsQuery: () => ({ rows: nicRows, isLoading: false }),
}));

const guestDetail: EntityDetail = {
  ref: "guest:pve1:100",
  kind: "guest",
  node: "pve1",
  label: "web01",
  fields: {},
  provenance: {},
  related: [],
  generatedAt: 0,
};

vi.mock("../topology/queries", () => ({
  useInventoryDetailQuery: () => ({ data: guestDetail, isLoading: false, isError: false }),
}));

const mockSelect = vi.fn();
vi.mock("../topology/store", () => ({
  useTopologyStore: (selector: (s: { select: typeof mockSelect }) => unknown) => selector({ select: mockSelect }),
}));

vi.mock("../topology/InteriorTab", () => ({
  InteriorTab: ({ entityRef }: { entityRef: string }) => <div data-testid="interior-tab">interior:{entityRef}</div>,
}));

vi.mock("../topology/EntityHistoryTab", () => ({
  EntityHistoryTab: ({ entityRef }: { entityRef: string }) => <div data-testid="history-tab">history:{entityRef}</div>,
}));

let simResult: SimulateResult | undefined;
vi.mock("../simulator/queries", () => ({
  useSimulateQuery: () => ({ data: simResult, isLoading: false, isError: false }),
}));

let rulesetResponse: GuestRulesetResponse | undefined;
vi.mock("../firewall/queries", () => ({
  useGuestRulesetQuery: () => ({ data: rulesetResponse, isLoading: false, isError: false }),
}));

let findings: StreamFinding[] = [];
vi.mock("../findings/queries", () => ({
  useFindingsQuery: () => ({ data: findings, isLoading: false, error: undefined }),
}));

let clusterHasAnyFlows: boolean | undefined = true;
let guestFlowItems: { at: number; srcIp: string; dstIp: string; proto: number; bytes: number; packets: number; source: string }[] = [];
vi.mock("./guestEgoQueries", () => ({
  useClusterHasAnyFlowsProbe: () => ({ hasAny: clusterHasAnyFlows, isLoading: false, isError: false }),
  useGuestFlows: () => ({ items: guestFlowItems, isLoading: false, isError: false }),
  useGuestConntrack: () => ({ data: conntrackPage, isLoading: false, isError: false }),
}));

let conntrackPage: ConntrackPage | undefined = { items: [] };

function renderAt(path: string) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[path]}>
        <GuestEgoView />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  simResult = undefined;
  rulesetResponse = undefined;
  findings = [];
  clusterHasAnyFlows = true;
  guestFlowItems = [];
  conntrackPage = { items: [] };
  mockSelect.mockClear();
});

describe("GuestEgoView routing", () => {
  it("shows a guest picker when no ref is given, listing guests derived from the NIC list", () => {
    renderAt("/guest");
    expect(screen.getByText("web01")).toBeInTheDocument();
    expect(screen.getByText("db01")).toBeInTheDocument();
  });

  it("shows an explicit error for a ref that isn't a guest ref", () => {
    renderAt("/guest?ref=bridge:pve1:vmbr0");
    expect(screen.getByText("Not a guest reference")).toBeInTheDocument();
  });

  it("renders the guest's identity once a valid ref is given", () => {
    renderAt("/guest?ref=guest:pve1:100");
    expect(screen.getByRole("heading", { name: /web01/ })).toBeInTheDocument();
  });
});

describe("GuestEgoView composition", () => {
  it("scopes the NICs panel to only this guest's own NICs", () => {
    renderAt("/guest?ref=guest:pve1:100");
    expect(screen.getByText("web01/net0")).toBeInTheDocument();
    expect(screen.queryByText("db01/net0")).not.toBeInTheDocument();
  });

  it("embeds the reused InteriorTab and EntityHistoryTab with this guest's ref", () => {
    renderAt("/guest?ref=guest:pve1:100");
    expect(screen.getByTestId("interior-tab")).toHaveTextContent("interior:guest:pve1:100");
    expect(screen.getByTestId("history-tab")).toHaveTextContent("history:guest:pve1:100");
  });

  it("shows the path evaluation verdict once a simulate result is available", () => {
    simResult = {
      verdict: "allow",
      src: { kind: "guest-nic", description: "web01 net0" },
      dst: { kind: "external", description: "external" },
      hops: [],
      caveats: [],
    };
    renderAt("/guest?ref=guest:pve1:100");
    expect(screen.getByText("Allowed")).toBeInTheDocument();
  });

  it("shows the firewall summary's active state and gate messages", () => {
    rulesetResponse = {
      ruleset: { ref: "guest:pve1:100", scope: "guest", enabled: true, rules: [] },
      resolved: {
        guest: "guest:pve1:100",
        active: false,
        gates: [{ scope: "cluster", message: "cluster firewall disabled" }],
        rules: [],
        defaultIn: { direction: "in", policy: "DROP", origin: "cluster" },
        defaultOut: { direction: "out", policy: "ACCEPT", origin: "cluster" },
      },
    };
    renderAt("/guest?ref=guest:pve1:100");
    expect(screen.getByText("Not active")).toBeInTheDocument();
    expect(screen.getByText("cluster firewall disabled")).toBeInTheDocument();
  });

  it("scopes open findings to this guest's own refs, excluding an unrelated one", () => {
    findings = [
      { id: "a", source: "health", check: "x", severity: "warning", detail: "affects this guest", nodes: ["pve1"], refs: ["guest:pve1:100"], fixable: false },
      { id: "b", source: "health", check: "y", severity: "warning", detail: "affects someone else", nodes: ["pve1"], refs: ["guest:pve1:999"], fixable: false },
    ];
    renderAt("/guest?ref=guest:pve1:100");
    expect(screen.getByText("affects this guest")).toBeInTheDocument();
    expect(screen.queryByText("affects someone else")).not.toBeInTheDocument();
  });

  it("shows an explicit 'no open findings' empty state when there are none", () => {
    renderAt("/guest?ref=guest:pve1:100");
    expect(screen.getByText("No open findings")).toBeInTheDocument();
  });
});

describe("GuestEgoView flows panel degradation", () => {
  it("distinguishes 'flow ingestion is not enabled' from an empty guest-scoped result", () => {
    clusterHasAnyFlows = false;
    renderAt("/guest?ref=guest:pve1:100");
    expect(screen.getByText("Flow ingestion is not enabled on this cluster")).toBeInTheDocument();
  });

  it("shows 'no recent flows for this guest' when the cluster has flows but this guest has none", () => {
    clusterHasAnyFlows = true;
    guestFlowItems = [];
    renderAt("/guest?ref=guest:pve1:100");
    expect(screen.getByText("No recent flows for this guest's network")).toBeInTheDocument();
    expect(screen.queryByText("Flow ingestion is not enabled on this cluster")).not.toBeInTheDocument();
  });

  it("renders actual flow rows when data is present", () => {
    clusterHasAnyFlows = true;
    guestFlowItems = [{ at: 1000, srcIp: "10.0.0.5", dstIp: "10.0.0.6", proto: 6, bytes: 2048, packets: 4, source: "sflow" }];
    renderAt("/guest?ref=guest:pve1:100");
    expect(screen.getByText(/10\.0\.0\.5 → 10\.0\.0\.6/)).toBeInTheDocument();
  });
});

describe("GuestEgoView conntrack panel degradation", () => {
  it("distinguishes 'conntrack not available on this node' from a genuinely empty table", () => {
    conntrackPage = { items: [], unavailableNodes: ["pve1"] };
    renderAt("/guest?ref=guest:pve1:100");
    expect(screen.getByText("Conntrack is not available on this guest's node")).toBeInTheDocument();
  });

  it("shows 'no active connections' when the node can read conntrack but has none", () => {
    conntrackPage = { items: [] };
    renderAt("/guest?ref=guest:pve1:100");
    expect(screen.getByText("No active connections right now")).toBeInTheDocument();
  });

  it("does not report unavailable for a different node's outage", () => {
    conntrackPage = { items: [], unavailableNodes: ["pve2"] };
    renderAt("/guest?ref=guest:pve1:100");
    expect(screen.getByText("No active connections right now")).toBeInTheDocument();
    expect(screen.queryByText("Conntrack is not available on this guest's node")).not.toBeInTheDocument();
  });

  it("renders live connection rows when data is present", () => {
    conntrackPage = { items: [{ node: "pve1", srcIp: "10.0.0.5", dstIp: "10.0.0.6", proto: 6, srcPort: 5000, dstPort: 443 }] };
    renderAt("/guest?ref=guest:pve1:100");
    expect(screen.getByText(/10\.0\.0\.5:5000 → 10\.0\.0\.6:443/)).toBeInTheDocument();
  });
});
