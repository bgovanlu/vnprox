// SPDX-License-Identifier: Apache-2.0

// T-1202 AC1 (frontend half): a reachable cluster capsule shows its name,
// aggregate findings, and drilling fires onDrill; an unreachable cluster
// renders degraded/greyed with an explicit indicator rather than being
// dropped.
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { ClusterSummary } from "../../api/federation";
import { ClusterCapsule } from "./ClusterCapsule";

const reachable: ClusterSummary = {
  clusterId: "cl-a",
  clusterName: "east",
  reachable: true,
  nodes: 3,
  nodesOnline: 3,
  guests: 12,
  findings: 2,
  drift: true,
};

const unreachable: ClusterSummary = {
  clusterId: "cl-b",
  clusterName: "west",
  reachable: false,
  nodes: 0,
  nodesOnline: 0,
  guests: 0,
  findings: 0,
  drift: false,
};

describe("ClusterCapsule", () => {
  it("renders a reachable cluster's rollup and drills on click", async () => {
    const onDrill = vi.fn();
    render(<ClusterCapsule summary={reachable} onDrill={onDrill} />);

    expect(screen.getByText("east")).toBeInTheDocument();
    expect(screen.getByText("2 findings")).toBeInTheDocument();
    expect(screen.getByText("drift")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Open cluster east" }));
    expect(onDrill).toHaveBeenCalledWith("cl-a");
  });

  it("renders an unreachable cluster greyed with an unreachable indicator", () => {
    render(<ClusterCapsule summary={unreachable} onDrill={vi.fn()} />);

    const capsule = screen.getByRole("button", { name: "Open cluster west" });
    expect(capsule).toHaveAttribute("data-reachable", "false");
    expect(capsule.className).toContain("opacity-70");
    expect(screen.getByText("unreachable")).toBeInTheDocument();
    // A degraded capsule doesn't pretend to have counts.
    expect(screen.queryByText(/findings$/)).not.toBeInTheDocument();
  });

  it("uses a singular finding label for exactly one finding", () => {
    render(<ClusterCapsule summary={{ ...reachable, findings: 1, drift: true }} onDrill={vi.fn()} />);
    expect(screen.getByText("1 finding")).toBeInTheDocument();
  });

  it("renders no interconnect badge when the cluster has no WireGuard linkage", () => {
    render(<ClusterCapsule summary={reachable} onDrill={vi.fn()} />);
    expect(screen.queryByText(/WG interconnect:/)).not.toBeInTheDocument();
  });

  it("renders a text interconnect badge on a reachable cluster (T-3909)", () => {
    render(
      <ClusterCapsule
        summary={reachable}
        onDrill={vi.fn()}
        interconnect={{ clusterId: "cl-a", clusterName: "east", tunnelId: "tun-1", tunnelSource: "explicit", state: "down" }}
      />,
    );
    expect(screen.getByText("WG interconnect: down")).toBeInTheDocument();
  });

  it("still renders the interconnect badge on an unreachable (PVE-side) capsule — the two signals are independent", () => {
    render(
      <ClusterCapsule
        summary={unreachable}
        onDrill={vi.fn()}
        interconnect={{ clusterId: "cl-b", clusterName: "west", tunnelId: "tun-2", tunnelSource: "peer", state: "up" }}
      />,
    );
    expect(screen.getByText("unreachable")).toBeInTheDocument();
    expect(screen.getByText("WG interconnect: up")).toBeInTheDocument();
  });
});
