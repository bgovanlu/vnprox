import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";
import type { SimulateResult } from "../api/types";
import { ResultPanel } from "./ResultPanel";

function baseResult(overrides: Partial<SimulateResult>): SimulateResult {
  return {
    verdict: "allow",
    src: { kind: "guest-nic", guest: "app01", node: "pve1" },
    dst: { kind: "guest-nic", guest: "cache01", node: "pve2" },
    hops: [],
    caveats: [{ code: "simulated", severity: "info", message: "Results reflect configured state, not live packets." }],
    ...overrides,
  };
}

function renderPanel(result: SimulateResult) {
  return render(
    <MemoryRouter>
      <ResultPanel result={result} />
    </MemoryRouter>,
  );
}

describe("ResultPanel verdict banner", () => {
  it.each([
    ["allow", "Allowed"],
    ["deny", "Blocked"],
    ["unreachable", "Unreachable"],
    ["indeterminate", "Could not determine"],
  ] as const)("renders the %s verdict distinctly as '%s'", (verdict, label) => {
    renderPanel(baseResult({ verdict }));
    expect(screen.getByText(label)).toBeInTheDocument();
  });

  it("always shows the 'Simulated' badge, regardless of verdict", () => {
    for (const verdict of ["allow", "deny", "unreachable", "indeterminate"] as const) {
      const { unmount } = renderPanel(baseResult({ verdict }));
      expect(screen.getByText("Simulated")).toBeInTheDocument();
      unmount();
    }
  });

  it("renders indeterminate as neither pass nor fail, with a pointer to the caveats", () => {
    renderPanel(baseResult({ verdict: "indeterminate" }));
    expect(screen.queryByText("Allowed")).not.toBeInTheDocument();
    expect(screen.queryByText("Blocked")).not.toBeInTheDocument();
    expect(screen.getByText(/See the blocker caveats below/)).toBeInTheDocument();
  });

  it("uses an alert role for non-allow verdicts and status for allow", () => {
    const { unmount } = renderPanel(baseResult({ verdict: "allow" }));
    expect(screen.getByRole("status")).toBeInTheDocument();
    unmount();
    renderPanel(baseResult({ verdict: "deny" }));
    expect(screen.getByRole("alert")).toBeInTheDocument();
  });
});

describe("ResultPanel caveats", () => {
  it("always renders every caveat, with no collapse/show-more control", () => {
    const result = baseResult({
      verdict: "indeterminate",
      caveats: [
        { code: "simulated", severity: "info", message: "Results reflect configured state, not live packets." },
        { code: "guest-ip-unknown", severity: "blocker", message: "Guest IP address is not known to the inventory." },
        { code: "conntrack", severity: "warning", message: "Established-flow return traffic is not modeled." },
      ],
    });
    renderPanel(result);
    expect(screen.getByText("Results reflect configured state, not live packets.")).toBeInTheDocument();
    expect(screen.getByText("Guest IP address is not known to the inventory.")).toBeInTheDocument();
    expect(screen.getByText("Established-flow return traffic is not modeled.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /show/i })).not.toBeInTheDocument();
  });

  it("puts blocker-severity caveats first", () => {
    const result = baseResult({
      caveats: [
        { code: "simulated", severity: "info", message: "info one" },
        { code: "x", severity: "blocker", message: "blocker one" },
      ],
    });
    renderPanel(result);
    const items = screen.getAllByText(/one$/);
    expect(items[0]).toHaveTextContent("blocker one");
  });
});

describe("ResultPanel blocking rule card", () => {
  it("renders the blocking rule with a deep link to the firewall editor (AC1)", () => {
    const result = baseResult({
      verdict: "deny",
      blockingRule: {
        enforcementPoint: "dest-guest-in",
        rulesetRef: "guest:pve1:102",
        origin: "guest",
        direction: "in",
        action: "DROP",
        pos: 0,
        rule: { pos: 0, enabled: true, direction: "in", action: "DROP", proto: "tcp", dport: "80", comment: "override" },
      },
    });
    renderPanel(result);
    expect(screen.getByText("Blocking rule")).toBeInTheDocument();
    expect(screen.getByText("tcp/80")).toBeInTheDocument();
    const link = screen.getByRole("link", { name: "Open in firewall editor" });
    expect(link).toHaveAttribute("href", "/firewall?scope=guest&ref=guest%3Apve1%3A102&pos=0&origin=guest");
  });

  it("renders no blocking-rule card for a non-deny verdict", () => {
    renderPanel(baseResult({ verdict: "allow" }));
    expect(screen.queryByText("Blocking rule")).not.toBeInTheDocument();
  });
});

describe("ResultPanel missing-link card", () => {
  it("renders the missing-link explanation for an unreachable verdict", () => {
    const result = baseResult({
      verdict: "unreachable",
      missing: {
        code: "vlan_not_trunked",
        message: "VLAN 100 is not trunked on bond0 of node pve2",
        atRef: "bridge:pve2:vmbr0",
        atNode: "pve2",
      },
    });
    renderPanel(result);
    expect(screen.getByText("VLAN 100 is not trunked on bond0 of node pve2")).toBeInTheDocument();
    expect(screen.getByText(/Ref:/).textContent).toContain("bridge:pve2:vmbr0");
  });
});
