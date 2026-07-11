// T-504's firewall deep-link consuming side: a simulator deny verdict's
// blocking-rule card (or a T-505 correlated log line) links to
// `/firewall?scope=guest&ref=...&pos=...&origin=...&group=...` — this
// page must land on the Guest tab, select that guest, and scroll/
// highlight the named rule (AC1: "one click lands in the rule editor
// with the rule focused").
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import type { GuestRulesetResponse, RulesetListResponse } from "../api/types";
import { FirewallPage } from "./FirewallPage";

const guestRulesetsResponse: RulesetListResponse = {
  items: [
    { ref: "guest:pve1:100", scope: "guest", enabled: true, rules: [] },
    { ref: "guest:pve1:102", scope: "guest", enabled: true, rules: [] },
  ],
};

const guestDetail: GuestRulesetResponse = {
  ruleset: {
    ref: "guest:pve1:102",
    scope: "guest",
    enabled: true,
    rules: [{ pos: 0, enabled: true, direction: "in", action: "DROP", proto: "tcp", dport: "80", comment: "override" }],
  },
  resolved: {
    guest: "guest:pve1:102",
    active: true,
    rules: [
      { pos: 0, origin: "cluster", rule: { pos: 0, enabled: true, direction: "in", action: "ACCEPT", proto: "tcp", dport: "22" } },
      { pos: 1, origin: "guest", rule: { pos: 0, enabled: true, direction: "in", action: "DROP", proto: "tcp", dport: "80" } },
    ],
    defaultIn: { direction: "in", policy: "DROP", origin: "cluster" },
    defaultOut: { direction: "out", policy: "ACCEPT", origin: "cluster" },
  },
};

vi.mock("../api/firewall", () => ({
  fetchClusterRuleset: vi.fn(() => Promise.reject(new Error("not used in this test"))),
  fetchNodeRuleset: vi.fn(() => Promise.reject(new Error("not used in this test"))),
  fetchNodeRulesets: vi.fn(() => Promise.resolve({ items: [] })),
  fetchGuestRuleset: vi.fn((ref: string) => (ref === "guest:pve1:102" ? Promise.resolve(guestDetail) : Promise.reject(new Error("unexpected ref")))),
  fetchGuestRulesets: vi.fn(() => Promise.resolve(guestRulesetsResponse)),
  fetchFirewallObjects: vi.fn(() => Promise.resolve({ aliases: [], ipsets: [], groups: [], macros: [] })),
}));

function renderAt(path: string) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[path]}>
        <FirewallPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("FirewallPage deep link (T-504 AC1)", () => {
  it("lands on the Guest tab, selects the named guest, and highlights the exact rule", async () => {
    renderAt("/firewall?scope=guest&ref=guest%3Apve1%3A102&pos=0&origin=guest");

    // Guest tab active.
    expect(screen.getByRole("button", { name: "Guests" })).toHaveAttribute("aria-pressed", "true");

    // The named guest is selected (not just the first in the list).
    const select = await screen.findByLabelText("Select guest");
    expect(select).toHaveValue("guest:pve1:102");

    // The guest's own DROP rule (origin: guest, pos 0) is rendered and
    // marked as the focus target.
    await screen.findByText("override");
    const focusedRows = document.querySelectorAll('[data-focused="true"]');
    expect(focusedRows.length).toBeGreaterThan(0);
  });

  it("defaults to the Datacenter tab when no deep-link params are present", () => {
    renderAt("/firewall");
    expect(screen.getByRole("button", { name: "Datacenter" })).toHaveAttribute("aria-pressed", "true");
  });
});
