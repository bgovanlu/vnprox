// SPDX-License-Identifier: Apache-2.0

// T-3904 acceptance-criteria coverage: node picker wiring, table/chain/
// rule rendering, attribution links vs. honest "not vnprox-authored"
// labels, the empty-ambiguous-engine state, and deep-link highlighting.
// Network is mocked at the query-hook seam (mirrors RouteExplorerPage.
// test.tsx's identical pattern) so these tests never touch fetch.
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { NftRulesetResponse, RouteNodesResponse } from "../api/types";
import { CompiledRulesetPage } from "./CompiledRulesetPage";

let nodesResult: { data: RouteNodesResponse | undefined } = { data: { nodes: ["pve1", "pve2"] } };
let rulesetResult: {
  data: NftRulesetResponse | undefined;
  isLoading: boolean;
  error: Error | null;
} = { data: undefined, isLoading: false, error: null };

vi.mock("../routeexplorer/routeQueries", () => ({
  useRouteNodesQuery: () => nodesResult,
}));

vi.mock("./nftablesQueries", () => ({
  useCompiledRulesetQuery: () => rulesetResult,
}));

function emptyRuleset(overrides: Partial<NftRulesetResponse> = {}): NftRulesetResponse {
  return { node: "pve1", tables: [], chains: [], rules: [], empty: true, ...overrides };
}

function populatedRuleset(): NftRulesetResponse {
  return {
    node: "pve1",
    empty: false,
    tables: [
      { family: "inet", name: "proxmox-firewall", pveAuthored: true },
      { family: "ip", name: "some-other-table", pveAuthored: false },
    ],
    chains: [
      {
        name: "input", table: { family: "inet", name: "proxmox-firewall", pveAuthored: true },
        builtin: true, type: "filter", hook: "input", priority: "filter", policy: "drop",
      },
      {
        name: "block-smurfs", table: { family: "inet", name: "proxmox-firewall", pveAuthored: true },
        builtin: true,
      },
    ],
    rules: [
      {
        table: { family: "inet", name: "proxmox-firewall", pveAuthored: true },
        chain: "input", handle: 10, verdict: "accept", proto: "tcp", dstPort: "22",
        attribution: { determined: true, scope: "cluster", ref: "fw-ruleset:cluster:cluster", origin: "cluster", pos: 0 },
      },
      {
        table: { family: "inet", name: "proxmox-firewall", pveAuthored: true },
        chain: "block-smurfs", handle: 11, verdict: "drop",
        attribution: { determined: false, reason: "chain \"block-smurfs\" is a PVE built-in plumbing/protection chain — not produced by any authored rule" },
      },
    ],
  };
}

function renderPage(initialEntries: string[] = ["/firewall/compiled"]): void {
  render(
    <MemoryRouter initialEntries={initialEntries}>
      <CompiledRulesetPage />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  nodesResult = { data: { nodes: ["pve1", "pve2"] } };
  rulesetResult = { data: emptyRuleset(), isLoading: false, error: null };
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("CompiledRulesetPage", () => {
  it("renders the page heading and node picker", () => {
    renderPage();
    expect(screen.getByRole("heading", { name: "Compiled ruleset" })).toBeInTheDocument();
    expect(screen.getByLabelText("Node")).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "pve1" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "pve2" })).toBeInTheDocument();
  });

  it("shows the honest ambiguous-engine empty state, not a bare 'no data'", () => {
    renderPage();
    expect(screen.getByText(/No compiled nftables output found on this node/)).toBeInTheDocument();
    expect(screen.getByText(/legacy iptables engine/)).toBeInTheDocument();
  });

  it("renders tables/chains/rules and marks a non-PVE table distinctly", () => {
    rulesetResult = { data: populatedRuleset(), isLoading: false, error: null };
    renderPage();
    expect(screen.getByText("inet proxmox-firewall")).toBeInTheDocument();
    expect(screen.getByText("ip some-other-table")).toBeInTheDocument();
    expect(screen.getByText("not vnprox/PVE firewall output")).toBeInTheDocument();
    expect(screen.getAllByText("PVE built-in").length).toBeGreaterThan(0);
  });

  it("links a determined-attribution rule back to its rule editor position", () => {
    rulesetResult = { data: populatedRuleset(), isLoading: false, error: null };
    renderPage();
    const link = screen.getByRole("link", { name: "View cluster rule #0" });
    expect(link).toHaveAttribute("href", expect.stringContaining("scope=cluster"));
    expect(link).toHaveAttribute("href", expect.stringContaining("pos=0"));
  });

  it("never renders a link for a rule with no determined attribution, and shows the reason instead", () => {
    rulesetResult = { data: populatedRuleset(), isLoading: false, error: null };
    renderPage();
    expect(screen.queryByRole("link", { name: /block-smurfs/ })).not.toBeInTheDocument();
    expect(screen.getByText(/PVE built-in plumbing\/protection chain/)).toBeInTheDocument();
    expect(screen.getByText(/Not vnprox-authored/)).toBeInTheDocument();
  });

  it("has no edit affordance anywhere on the page", () => {
    rulesetResult = { data: populatedRuleset(), isLoading: false, error: null };
    renderPage();
    expect(screen.queryByRole("button", { name: /add|edit|delete/i })).not.toBeInTheDocument();
  });
});
