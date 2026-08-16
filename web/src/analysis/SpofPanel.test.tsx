// T-3004 AC1's frontend half: a failure simulation names the affected
// entities as map links, and states "indeterminate" where it cannot decide.
// Network is mocked at the ./analysisQueries seam (the same pattern
// edge/EdgeCockpit.test.tsx uses) so these tests never touch fetch.
import { render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import type { FailsimImpact, SpofScore } from "../api/types";
import { SpofPanel } from "./SpofPanel";

let spofResult: { data: SpofScore | undefined; isLoading: boolean; error: Error | null } = {
  data: undefined,
  isLoading: false,
  error: null,
};

vi.mock("./analysisQueries", () => ({
  useSpofScoreQuery: () => spofResult,
}));

function impact(overrides: Partial<FailsimImpact> = {}): FailsimImpact {
  return {
    target: "physnic:pve1:eno1",
    severity: "none",
    disconnectedGuests: [],
    strandedVlans: [],
    mgmtPathLoss: [],
    notEvaluated: [],
    quorumRisk: false,
    cephRisk: false,
    ...overrides,
  };
}

function renderPanel(data: SpofScore) {
  spofResult = { data, isLoading: false, error: null };
  return render(
    <MemoryRouter>
      <SpofPanel />
    </MemoryRouter>,
  );
}

describe("SpofPanel", () => {
  it("renders an undecidable impact as Indeterminate, never as no impact", () => {
    renderPanel({
      score: 80,
      generatedAt: "2026-08-16T00:00:00Z",
      entries: [
        {
          ref: "physnic:pve1:eno1",
          impact: impact({ severity: "info", notEvaluated: ["quorum", "ceph"] }),
        },
      ],
    });

    expect(screen.getByTestId("spof-verdict")).toHaveTextContent("Indeterminate");
    expect(screen.queryByText("No known impact")).not.toBeInTheDocument();
    expect(screen.getByTestId("spof-not-evaluated")).toHaveTextContent("corosync quorum");
    expect(screen.getByTestId("spof-not-evaluated")).toHaveTextContent("Ceph networks");
  });

  it("renders an unrecognised severity as Indeterminate rather than guessing", () => {
    renderPanel({
      score: 50,
      generatedAt: "2026-08-16T00:00:00Z",
      entries: [{ ref: "bond:pve1:bond0", impact: impact({ severity: "catastrophic" }) }],
    });

    expect(screen.getByTestId("spof-verdict")).toHaveTextContent("Indeterminate");
  });

  it("names the affected entities as map links", () => {
    renderPanel({
      score: 40,
      generatedAt: "2026-08-16T00:00:00Z",
      entries: [
        {
          ref: "physnic:pve1:eno1",
          impact: impact({
            severity: "warning",
            disconnectedGuests: ["guest:pve1:100"],
            strandedVlans: ["vlan:pve1:vmbr0.20"],
          }),
        },
      ],
    });

    const affected = screen.getByRole("list", { name: "" });
    expect(affected).toBeTruthy();
    // Each affected entity is a button that selects it on the map — the same
    // affordance the MAC/FDB browser's owner badge uses.
    expect(screen.getByRole("button", { name: "guest:pve1:100" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "vlan:pve1:vmbr0.20" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "physnic:pve1:eno1" })).toBeInTheDocument();
  });

  it("suppresses a quorum-risk flag when quorum was not evaluated", () => {
    // quorumRisk false alongside `quorum` in notEvaluated means "not
    // checked", not "safe" — the flag must not be rendered either way round.
    renderPanel({
      score: 60,
      generatedAt: "2026-08-16T00:00:00Z",
      entries: [
        {
          ref: "node:pve1:pve1",
          impact: impact({ severity: "info", quorumRisk: false, notEvaluated: ["quorum"] }),
        },
      ],
    });

    expect(screen.queryByText(/Puts corosync quorum at risk/)).not.toBeInTheDocument();
    expect(screen.getByTestId("spof-not-evaluated")).toHaveTextContent("corosync quorum");
  });

  it("sorts indeterminate immediately after critical, ahead of degraded", () => {
    renderPanel({
      score: 10,
      generatedAt: "2026-08-16T00:00:00Z",
      entries: [
        { ref: "a:degraded:x", impact: impact({ severity: "warning" }) },
        { ref: "b:unknown:x", impact: impact({ severity: "info", notEvaluated: ["ceph"] }) },
        { ref: "c:critical:x", impact: impact({ severity: "critical" }) },
      ],
    });

    const verdicts = screen.getAllByTestId("spof-verdict").map((el) => el.textContent);
    expect(verdicts).toEqual(["Critical", "Indeterminate", "Degrades connectivity"]);
  });

  it("distinguishes 'no single points of failure' from a failed read", () => {
    renderPanel({ score: 100, generatedAt: "2026-08-16T00:00:00Z", entries: [] });
    expect(screen.getByText("No single points of failure found")).toBeInTheDocument();

    spofResult = { data: undefined, isLoading: false, error: new Error("boom") };
    const { container } = render(
      <MemoryRouter>
        <SpofPanel />
      </MemoryRouter>,
    );
    expect(within(container).getByText("Could not compute the SPOF score")).toBeInTheDocument();
    expect(within(container).queryByText("No single points of failure found")).not.toBeInTheDocument();
  });
});
