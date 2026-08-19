// T-3501 AC5: findings with no entity refs (health/service_down for dnsmasq
// and frr on the reference node, pvecube — see planning/tasks/phase-35.md)
// must not paint nothing anywhere. This is their home — mirrors
// StalenessBanner.test.tsx's exact pattern.
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { UnrefFinding } from "../api/types";
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
