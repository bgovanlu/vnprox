// SPDX-License-Identifier: Apache-2.0

// T-3501 AC5: findings with no entity refs (health/service_down for dnsmasq
// and frr on the reference node, pvecube — see planning/tasks/phase-35.md)
// must not paint nothing anywhere. This is their home — mirrors
// StalenessBanner.test.tsx's exact pattern.
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { UnrefFinding } from "../api/types";
import type { RemediationContext } from "../findings/remediation";
import { UnrefFindingsBanner } from "./UnrefFindingsBanner";

describe("UnrefFindingsBanner", () => {
  it("renders nothing when there are no unref findings", () => {
    const { container } = render(<UnrefFindingsBanner findings={undefined} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing for an empty array", () => {
    const { container } = render(<UnrefFindingsBanner findings={[]} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("surfaces the reference node's two ref-less service_down findings (dnsmasq, frr)", () => {
    const findings: UnrefFinding[] = [
      { source: "health", severity: "error", check: "service_down", detail: "dnsmasq is not running", nodes: ["pvecube"] },
      { source: "health", severity: "error", check: "service_down", detail: "frr is not running", nodes: ["pvecube"] },
    ];
    render(<UnrefFindingsBanner findings={findings} />);
    const banner = screen.getByRole("status");
    expect(banner).toHaveTextContent("2 findings are not tied to any map entity");
    expect(banner).toHaveTextContent("dnsmasq is not running");
    expect(banner).toHaveTextContent("frr is not running");
    expect(banner).toHaveTextContent("pvecube");
    // Severity/source are legible via the same chip vocabulary the map uses.
    expect(screen.getAllByText("■ health")).toHaveLength(2);
  });

  it("uses singular wording for exactly one finding", () => {
    const findings: UnrefFinding[] = [
      { source: "health", severity: "warning", check: "service_down", detail: "dnsmasq is not running", nodes: ["pvecube"] },
    ];
    render(<UnrefFindingsBanner findings={findings} />);
    expect(screen.getByRole("status")).toHaveTextContent("1 finding is not tied to any map entity");
  });

  it("falls back to the check id when detail is empty", () => {
    const findings: UnrefFinding[] = [{ source: "health", severity: "error", check: "service_down", detail: "", nodes: [] }];
    render(<UnrefFindingsBanner findings={findings} />);
    expect(screen.getByRole("status")).toHaveTextContent("service_down");
  });
});

// T-3604: the banner offers to start the service it says is down.
describe("UnrefFindingsBanner — service.start remedy (T-3604)", () => {
  const dnsmasq: UnrefFinding = {
    source: "health", severity: "error", check: "service_down",
    detail: "dnsmasq is not running on node pvecube", nodes: ["pvecube"],
    remedy: {
      action: "service.start", kind: "operational", label: "Start dnsmasq",
      params: { node: "pvecube", service: "dnsmasq" },
    },
  };
  const frr: UnrefFinding = {
    ...dnsmasq,
    detail: "frr is not running on node pvecube",
    remedy: {
      action: "service.start", kind: "operational", label: "Start frr",
      params: { node: "pvecube", service: "frr" },
    },
  };

  function ctx(over: Partial<RemediationContext> = {}): RemediationContext {
    return { netWrite: true, navigate: vi.fn(), runOperational: vi.fn(), ...over };
  }

  it("offers no action when the surface supplies no remediation context", () => {
    render(<UnrefFindingsBanner findings={[dnsmasq]} />);
    expect(screen.queryByRole("button", { name: "Start dnsmasq" })).toBeNull();
  });

  it("offers no action without netWrite", () => {
    render(<UnrefFindingsBanner findings={[dnsmasq]} remediationCtx={ctx({ netWrite: false })} />);
    expect(screen.queryByRole("button", { name: "Start dnsmasq" })).toBeNull();
  });

  it("confirms before starting, and says what it will and will not do", async () => {
    const user = userEvent.setup();
    const c = ctx();
    render(<UnrefFindingsBanner findings={[dnsmasq]} remediationCtx={c} />);
    await user.click(screen.getByRole("button", { name: "Start dnsmasq" }));
    expect(c.runOperational).not.toHaveBeenCalled();
    // An operator who reads "start" and gets "start and enable" has been
    // misled, however convenient the extra step would have been.
    expect(screen.getByText(/does not enable it at boot/)).toBeInTheDocument();
    expect(screen.getByText(/systemctl start dnsmasq/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Start" }));
    expect(c.runOperational).toHaveBeenCalledWith(dnsmasq.remedy);
  });

  // The regression that motivated splitting the list key from the action
  // key: dnsmasq and frr down on ONE node produce two findings identical in
  // source, check and nodes. Keyed on those three, an error from one landed
  // against both.
  it("keeps two services on the same node apart", () => {
    render(
      <UnrefFindingsBanner
        findings={[dnsmasq, frr]}
        remediationCtx={ctx()}
        results={{ "pvecube/frr": "Unit frr.service is masked." }}
      />,
    );
    expect(screen.getByRole("button", { name: "Start dnsmasq" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Start frr" })).toBeInTheDocument();
    // Exactly one row shows the error, and it is frr's.
    expect(screen.getAllByText(/Unit frr.service is masked/)).toHaveLength(1);
  });

  it("shows only the pending service as pending", async () => {
    const user = userEvent.setup();
    render(<UnrefFindingsBanner findings={[dnsmasq, frr]} remediationCtx={ctx()} pendingId="pvecube/frr" />);
    await user.click(screen.getByRole("button", { name: "Start frr" }));
    expect(screen.getByRole("button", { name: "Working…" })).toBeInTheDocument();
  });
});
