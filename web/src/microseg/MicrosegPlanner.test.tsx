// T-1603 AC1 + AC3 (Vitest):
//   AC1 — MicrosegPlanner renders the coverage percentage and uncovered-flow
//         count from a fixed Proposal fixture WITHOUT rounding either to
//         100%/0 (the honesty contract).
//   AC3 — "Stage as changeset" is disabled until a dry-run has been run at
//         least once for the current proposal; re-proposing invalidates the
//         prior dry-run's staged-enablement.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ToastProvider } from "../components/Toast";
import type { MicrosegDryRunReport, MicrosegProposal } from "../api/types";
import { MicrosegPlanner } from "./MicrosegPlanner";

const proposeMicroseg = vi.fn<(guestRef: string) => Promise<MicrosegProposal>>();
const dryRunMicroseg = vi.fn<(guestRef: string, heldOut?: boolean) => Promise<MicrosegDryRunReport>>();

vi.mock("../api/microseg", () => ({
  proposeMicroseg: (guestRef: string) => proposeMicroseg(guestRef),
  dryRunMicroseg: (guestRef: string, heldOut?: boolean) => dryRunMicroseg(guestRef, heldOut),
}));

const addOps = vi.fn(() => Promise.resolve({ id: "cs-1" }));
vi.mock("../changesets/useDrawerActions", () => ({
  useDrawerActions: () => ({ addOps, replaceOps: vi.fn(), amendLastOps: vi.fn() }),
}));

const PROPOSAL: MicrosegProposal = {
  guestRef: "guest:pve1:210",
  rulesetRef: "fw-ruleset:pve1:guest/qemu/210",
  directions: ["in", "out"],
  rules: [
    { pos: 0, enabled: true, direction: "in", action: "ACCEPT", proto: "tcp", dport: "445", source: "10.0.0.0/24", comment: "smb" },
    { pos: 1, enabled: true, direction: "in", action: "DROP", comment: "default-deny" },
  ],
  stagedOps: [
    {
      op: "fw.rule.create",
      target: "fw-ruleset:pve1:guest/qemu/210",
      params: { direction: "in", action: "ACCEPT", proto: "tcp", dport: "445", source: "10.0.0.0/24", pos: 0, enabled: true },
    },
  ],
  coveragePct: 99.53,
  observedGoodBytes: 100_000,
  coveredBytes: 99_530,
  observedGoodFlowCount: 120,
  uncoveredFlowCount: 7,
  excludedAnomalyFlows: 2,
  alreadyCoveredGroups: 1,
};

const REPORT: MicrosegDryRunReport = {
  guestRef: "guest:pve1:210",
  wouldAllow: [],
  wouldBlock: [],
  cannotDetermine: [],
  ungoverned: [],
  coveragePct: 99.53,
};

function renderPlanner(): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <MicrosegPlanner guestRef="guest:pve1:210" />
      </ToastProvider>
    </QueryClientProvider>,
  );
}

afterEach(() => {
  proposeMicroseg.mockReset();
  dryRunMicroseg.mockReset();
  addOps.mockClear();
});

describe("MicrosegPlanner (T-1603 AC1 + AC3)", () => {
  it("renders coverage % and uncovered-flow count without rounding to 100%/0 (AC1)", async () => {
    proposeMicroseg.mockResolvedValue(PROPOSAL);
    renderPlanner();

    await userEvent.click(screen.getByRole("button", { name: "Propose policy" }));

    const summary = await screen.findByTestId("coverage-summary");
    // 99.53% shown verbatim, never lifted to "100%".
    expect(summary).toHaveTextContent("99.53%");
    expect(summary).not.toHaveTextContent("100%");
    // 7 uncovered flows stated plainly, never rounded away to 0.
    expect(summary).toHaveTextContent("7");
    expect(summary).toHaveTextContent(/deliberately-uncovered long tail/i);
  });

  it("gates 'Stage as changeset' behind a dry-run and re-propose invalidates it (AC3)", async () => {
    proposeMicroseg.mockResolvedValue(PROPOSAL);
    dryRunMicroseg.mockResolvedValue(REPORT);
    renderPlanner();

    await userEvent.click(screen.getByRole("button", { name: "Propose policy" }));
    await screen.findByTestId("coverage-summary");

    // Before any dry-run: Stage is disabled.
    const stageButton = screen.getByRole("button", { name: "Stage as changeset" });
    expect(stageButton).toBeDisabled();

    // After a dry-run: Stage is enabled.
    await userEvent.click(screen.getByRole("button", { name: "Run dry-run" }));
    await screen.findByTestId("dry-run-report");
    await waitFor(() => { expect(stageButton).toBeEnabled(); });

    // Re-proposing invalidates the prior dry-run's staged-enablement.
    await userEvent.click(screen.getByRole("button", { name: "Re-propose policy" }));
    await waitFor(() => { expect(screen.queryByTestId("dry-run-report")).not.toBeInTheDocument(); });
    expect(screen.getByRole("button", { name: "Stage as changeset" })).toBeDisabled();
  });

  it("stages the server-computed ops into the drawer once dry-run has run", async () => {
    proposeMicroseg.mockResolvedValue(PROPOSAL);
    dryRunMicroseg.mockResolvedValue(REPORT);
    renderPlanner();

    await userEvent.click(screen.getByRole("button", { name: "Propose policy" }));
    await screen.findByTestId("coverage-summary");
    await userEvent.click(screen.getByRole("button", { name: "Run dry-run" }));
    await screen.findByTestId("dry-run-report");

    await userEvent.click(screen.getByRole("button", { name: "Stage as changeset" }));

    await waitFor(() => { expect(addOps).toHaveBeenCalledTimes(1); });
    expect(addOps).toHaveBeenCalledWith(PROPOSAL.stagedOps, "Microsegment guest:pve1:210");
  });
});
