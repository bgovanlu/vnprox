// T-2704: the point-in-time diff panel. The assertions here are about the one
// thing the feature exists for — that a change vnprox did NOT make is visibly
// marked as such, and that a range vnprox cannot answer for says so instead of
// rendering as "no differences".
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "../api/client";
import type { TopologyDiffResponse } from "../api/topologyDiff";
import { TopologyDiffPanel } from "./TopologyDiffPanel";

const fetchTopologyDiff = vi.fn<(from: string, to: string) => Promise<TopologyDiffResponse>>();

vi.mock("../api/topologyDiff", async () => {
  const actual = await vi.importActual<typeof import("../api/topologyDiff")>("../api/topologyDiff");
  return { ...actual, fetchTopologyDiff: (from: string, to: string) => fetchTopologyDiff(from, to) };
});

function baseDiff(partial: Partial<TopologyDiffResponse> = {}): TopologyDiffResponse {
  return {
    from: { requested: "snap-1", snapshotId: "snap-1", kind: "scheduled", at: 100 },
    to: { requested: "now", live: true, at: 200 },
    added: [],
    removed: [],
    modified: [],
    coverage: { nodes: ["pve1"], paths: ["/etc/network/interfaces"] },
    unattributedCount: 0,
    ...partial,
  };
}

function renderPanel(from = "snap-1", to = "now"): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <TopologyDiffPanel from={from} to={to} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  fetchTopologyDiff.mockReset();
});

describe("TopologyDiffPanel", () => {
  it("names the changeset behind an attributed change and marks an unattributed one", async () => {
    fetchTopologyDiff.mockResolvedValue(
      baseDiff({
        added: [
          {
            ref: "bridge:pve1:vmbr9",
            kind: "bridge",
            node: "pve1",
            name: "vmbr9",
            change: "added",
            fields: [{ field: "MTUDeclared", before: "", after: "9000" }],
            attribution: {
              attributed: true,
              changesetId: "cs-1",
              changesetTitle: "add vmbr9",
              actor: "alice@pve",
              at: 150,
            },
          },
        ],
        modified: [
          {
            ref: "bridge:pve1:vmbr0",
            kind: "bridge",
            node: "pve1",
            name: "vmbr0",
            change: "modified",
            fields: [{ field: "MTUDeclared", before: "1500", after: "9000" }],
            attribution: { attributed: false },
          },
        ],
        unattributedCount: 1,
      }),
    );
    renderPanel();

    // Control leg: the attributed row DOES name its changeset, so the
    // "Unattributed" assertion below is about attribution and not about the
    // panel failing to render attribution at all.
    expect(await screen.findByRole("link", { name: "add vmbr9" })).toBeInTheDocument();
    expect(screen.getByText(/by alice@pve in/)).toBeInTheDocument();

    // The product value: the out-of-band change is called out, in words.
    expect(screen.getByText("Unattributed")).toBeInTheDocument();
    expect(screen.getByText("1 made outside vnprox")).toBeInTheDocument();
  });

  it("shows field-level before and after, not merely 'modified'", async () => {
    fetchTopologyDiff.mockResolvedValue(
      baseDiff({
        modified: [
          {
            ref: "bridge:pve1:vmbr0",
            kind: "bridge",
            node: "pve1",
            name: "vmbr0",
            change: "modified",
            fields: [
              { field: "MTUDeclared", before: "1500", after: "9000" },
              { field: "DeclaredPortNames", before: "eno1", after: "eno1 eno2" },
            ],
            attribution: { attributed: false },
          },
        ],
        unattributedCount: 1,
      }),
    );
    renderPanel();

    await screen.findByText("MTUDeclared");
    expect(screen.getByText("1500")).toBeInTheDocument();
    expect(screen.getByText("9000")).toBeInTheDocument();
    expect(screen.getByText("eno1")).toBeInTheDocument();
    expect(screen.getByText("eno1 eno2")).toBeInTheDocument();
  });

  it("surfaces the server's message for an uncovered range rather than an empty diff", async () => {
    // A 422 no_snapshot_in_range names the nearest snapshots that DO exist —
    // the one thing that tells an operator which range to ask for instead. It
    // must reach the screen verbatim, and must NOT be rendered as "no
    // differences", which would be a false statement about the cluster.
    fetchTopologyDiff.mockRejectedValue(
      new ApiError(
        422,
        "no_snapshot_in_range",
        'change: no snapshot covers the from point "1600000000"; nearest available: snap-7 (scheduled, 2023-11-14T22:13:20Z)',
      ),
    );
    renderPanel();

    expect(await screen.findByText(/nearest available: snap-7/)).toBeInTheDocument();
    expect(screen.queryByText("No differences")).not.toBeInTheDocument();
  });

  it("names nodes captured at only one end instead of claiming their interfaces were deleted", async () => {
    fetchTopologyDiff.mockResolvedValue(
      baseDiff({
        coverage: {
          nodes: ["pve1"],
          paths: ["/etc/network/interfaces"],
          unmatchedNodes: [{ node: "pve3", presentIn: "from" }],
        },
      }),
    );
    renderPanel();

    expect(await screen.findByText(/pve3 \(only in the earlier point\)/)).toBeInTheDocument();
    expect(screen.getByText(/nothing is claimed about them/)).toBeInTheDocument();
  });

  it("asks for nothing until both points are chosen", () => {
    renderPanel("", "");
    expect(fetchTopologyDiff).not.toHaveBeenCalled();
    expect(screen.getByText("Pick two points to compare")).toBeInTheDocument();
  });

  it("does fetch once both points are chosen", async () => {
    // Control leg for the assertion above: the spy counts when it should.
    fetchTopologyDiff.mockResolvedValue(baseDiff());
    renderPanel("snap-1", "now");
    await screen.findByText("No differences");
    expect(fetchTopologyDiff).toHaveBeenCalledWith("snap-1", "now");
  });

  it("links the range through to the map overlay", async () => {
    fetchTopologyDiff.mockResolvedValue(baseDiff());
    renderPanel("snap-1", "now");
    const link = await screen.findByRole("link", { name: "Show on map" });
    expect(link).toHaveAttribute("href", "/topology?diffFrom=snap-1&diffTo=now");
  });
});
