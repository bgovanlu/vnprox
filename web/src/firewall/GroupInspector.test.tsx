// SPDX-License-Identifier: Apache-2.0

// T-2002 item 3: the security-group inspector, closing T-1603's flagged
// gap that the microsegmentation planner could only be launched per-guest.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ToastProvider } from "../components/Toast";
import type { FirewallEffectsResponse, GroupRulesetResponse } from "../api/types";
import { GroupInspector } from "./GroupInspector";

const groupDetail: GroupRulesetResponse = {
  name: "webservers",
  comment: "public-facing web tier",
  rules: [
    { pos: 0, enabled: true, direction: "in", action: "ACCEPT", proto: "tcp", dport: "443", comment: "https" },
    { pos: 1, enabled: false, direction: "in", action: "ACCEPT", proto: "tcp", dport: "80" },
  ],
};

const isolatedGroupDetail: GroupRulesetResponse = {
  name: "isolated-group",
  rules: [{ pos: 0, enabled: true, direction: "in", action: "DROP" }],
};

const effects: FirewallEffectsResponse = { group: "webservers", guests: ["guest:pve1:100", "guest:pve1:101"] };

vi.mock("../api/firewall", () => ({
  fetchGroupRuleset: vi.fn((name: string) => {
    if (name === "webservers") return Promise.resolve(groupDetail);
    if (name === "isolated-group") return Promise.resolve(isolatedGroupDetail);
    return Promise.reject(new Error("not found"));
  }),
  fetchFirewallEffects: vi.fn((group: string) =>
    group === "webservers" ? Promise.resolve(effects) : Promise.resolve({ group, guests: [] }),
  ),
}));

function renderInspector(name: string, onBack: () => void = vi.fn()) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <GroupInspector name={name} onBack={onBack} />
      </ToastProvider>
    </QueryClientProvider>,
  );
}

describe("GroupInspector", () => {
  it("renders the group's own rules and comment", async () => {
    renderInspector("webservers");
    expect(await screen.findByText("public-facing web tier")).toBeInTheDocument();
    expect(screen.getByText("https")).toBeInTheDocument();
    expect(screen.getAllByRole("row")).toHaveLength(3); // header + 2 rules
  });

  it("lists the guests the group's rules actually reach", async () => {
    renderInspector("webservers");
    const select = await screen.findByLabelText("Select a guest to plan for");
    expect(select).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "guest:pve1:100" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "guest:pve1:101" })).toBeInTheDocument();
  });

  it("launches the microseg planner for a chosen guest — same component the guest-scoped launch uses (AC3)", async () => {
    const user = userEvent.setup();
    renderInspector("webservers");
    const select = await screen.findByLabelText("Select a guest to plan for");
    await user.selectOptions(select, "guest:pve1:100");
    expect(await screen.findByTestId("microseg-planner")).toBeInTheDocument();
    expect(screen.getByText("Microsegmentation planner")).toBeInTheDocument();
  });

  it("degrades honestly when the group matches no guests", async () => {
    renderInspector("isolated-group");
    expect(await screen.findByText(/matches no guests yet/)).toBeInTheDocument();
  });

  it("shows a not-found message when the group itself no longer exists", async () => {
    renderInspector("does-not-exist");
    expect(await screen.findByText("Could not load this group")).toBeInTheDocument();
  });

  it("calls onBack when the back link is clicked", async () => {
    const user = userEvent.setup();
    const onBack = vi.fn();
    renderInspector("webservers", onBack);
    await user.click(screen.getByRole("button", { name: /back to objects/i }));
    expect(onBack).toHaveBeenCalled();
  });
});
