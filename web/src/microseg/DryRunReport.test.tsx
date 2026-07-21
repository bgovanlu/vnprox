// T-1603 AC2 (Vitest): DryRunReport renders every `wouldBlock` entry from a
// fixed Report fixture in the flow-table format, and zero entries render
// zero rows (not a missing/error state). The would-have-blocked and
// cannot-determine buckets must be prominent — asserted here by their
// dedicated sections being present regardless of count.
import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { MicrosegDryRunReport, MicrosegFlowRef } from "../api/types";
import { DryRunReport } from "./DryRunReport";

function flow(overrides: Partial<MicrosegFlowRef> = {}): MicrosegFlowRef {
  return {
    direction: "in",
    peerIp: "10.0.0.5",
    peerSubnet: "10.0.0.0/24",
    proto: 6,
    port: 445,
    at: 1_752_753_600,
    bytes: 4096,
    ...overrides,
  };
}

function report(overrides: Partial<MicrosegDryRunReport> = {}): MicrosegDryRunReport {
  return {
    guestRef: "guest:pve1:210",
    wouldAllow: [],
    wouldBlock: [],
    cannotDetermine: [],
    ungoverned: [],
    coveragePct: 99.5,
    ...overrides,
  };
}

describe("DryRunReport (T-1603 AC2)", () => {
  it("renders every wouldBlock entry in the flow-table format", () => {
    render(
      <DryRunReport
        report={report({
          wouldBlock: [
            flow({ peerIp: "10.0.0.5", port: 445 }),
            flow({ peerIp: "10.0.0.9", port: 2049, at: 1_752_753_700 }),
          ],
        })}
      />,
    );

    const section = screen.getByTestId("would-block-section");
    expect(within(section).getByText("Would-have-blocked flows (2)")).toBeInTheDocument();

    const table = within(section).getByRole("table", { name: "Would-have-blocked flows table" });
    // Header row + one row per entry.
    const rows = within(table).getAllByRole("row");
    expect(rows).toHaveLength(3);
    expect(within(section).getByText(/10\.0\.0\.5/)).toBeInTheDocument();
    expect(within(section).getByText(/10\.0\.0\.9/)).toBeInTheDocument();
  });

  it("renders zero rows for an empty wouldBlock bucket, not a missing/error state", () => {
    render(<DryRunReport report={report({ wouldBlock: [] })} />);

    const section = screen.getByTestId("would-block-section");
    // The section and its heading are still present (never hidden).
    expect(within(section).getByText("Would-have-blocked flows (0)")).toBeInTheDocument();
    expect(within(section).getByText(/No observed-good flow would be blocked/i)).toBeInTheDocument();

    const table = within(section).getByRole("table", { name: "Would-have-blocked flows table" });
    // Header row only — zero data rows.
    expect(within(table).getAllByRole("row")).toHaveLength(1);
  });

  it("surfaces the cannot-determine bucket prominently with its reason", () => {
    render(
      <DryRunReport
        report={report({
          cannotDetermine: [flow({ reason: "iface-scoped rule: iface not resolvable in dry-run" })],
        })}
      />,
    );

    const section = screen.getByTestId("cannot-determine-section");
    expect(within(section).getByText("Cannot-determine flows (1)")).toBeInTheDocument();
    expect(within(section).getByText(/not resolvable in dry-run/)).toBeInTheDocument();
    // Never assumed safe — an alert role marks it.
    expect(within(section).getByRole("alert")).toBeInTheDocument();
  });

  it("labels a held-out dry-run distinctly", () => {
    render(<DryRunReport report={report()} heldOut />);
    expect(screen.getByText(/held-out window/i)).toBeInTheDocument();
  });
});
