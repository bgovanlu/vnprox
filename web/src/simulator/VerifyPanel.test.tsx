import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { SimulateResult, VerifyResult } from "../api/types";
import { VerifyPanel } from "./VerifyPanel";

function baseSimulated(overrides: Partial<SimulateResult> = {}): SimulateResult {
  return {
    verdict: "allow",
    src: { kind: "guest-nic", guest: "guest:pve1:300", node: "pve1", description: "vm-a net0 on bridge vmbr0" },
    dst: { kind: "guest-nic", guest: "guest:pve1:301", node: "pve1", description: "vm-c net0 on bridge vmbr0" },
    hops: [],
    caveats: [{ code: "simulated", severity: "info", message: "Results reflect configured state, not live packets." }],
    ...overrides,
  };
}

function verify(overrides: Partial<VerifyResult> = {}): VerifyResult {
  return {
    simulated: baseSimulated(),
    observed: { outcome: "reachable", detail: "connected" },
    diverges: false,
    ...overrides,
  };
}

describe("VerifyPanel", () => {
  it("renders simulated and observed outcomes side by side", () => {
    render(<VerifyPanel verify={verify({ simulated: baseSimulated({ verdict: "allow" }), observed: { outcome: "reachable" } })} />);
    expect(screen.getByText("Simulated")).toBeInTheDocument();
    expect(screen.getByText("Observed")).toBeInTheDocument();
    expect(screen.getByText("Allowed")).toBeInTheDocument();
    expect(screen.getByText("Reachable")).toBeInTheDocument();
  });

  it("renders a distinct divergence callout when diverges is true", () => {
    render(
      <VerifyPanel
        verify={verify({ simulated: baseSimulated({ verdict: "deny" }), observed: { outcome: "reachable" }, diverges: true })}
      />,
    );
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent(/disagrees with the simulated verdict/i);
    // Honesty contract: never implies one result "corrects" the other, or
    // that either one is simply "actually" the truth — only that they
    // disagree. The panel is allowed to say "not a correction" (the
    // honest disclosure); it must never say "actually"/"is correct".
    expect(alert.textContent.toLowerCase()).not.toMatch(/\bactually\b|\bis correct\b|\bcorrects\b/);
  });

  it("renders no divergence callout when the results agree", () => {
    render(
      <VerifyPanel
        verify={verify({ simulated: baseSimulated({ verdict: "allow" }), observed: { outcome: "reachable" }, diverges: false })}
      />,
    );
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("shows the honest execError text for an error outcome rather than a generic message", () => {
    render(
      <VerifyPanel
        verify={verify({ observed: { outcome: "error", execError: "guest agent is not running" }, diverges: false })}
      />,
    );
    expect(screen.getByText("Could not be attempted")).toBeInTheDocument();
    expect(screen.getByText("guest agent is not running")).toBeInTheDocument();
  });

  it("always labels this section as a live probe, distinct from the static simulated result", () => {
    render(<VerifyPanel verify={verify()} />);
    expect(screen.getByText("Live probe")).toBeInTheDocument();
  });
});
