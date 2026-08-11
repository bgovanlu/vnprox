// T-2804's page. The assertions are about the claims the UI makes: one
// interleaved timeline, an honest account of a source that contributed
// nothing, and a diff refusal shown as a refusal rather than as "nothing
// changed".
import { render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { IncidentListResponse, IncidentTimeline } from "../api/incidents";
import type { MeResponse } from "../api/types";
import { ToastProvider } from "../components/Toast";
import { IncidentsPage } from "./IncidentsPage";

const fetchIncidents = vi.fn<() => Promise<IncidentListResponse>>();
const fetchIncidentTimeline = vi.fn<(id: string) => Promise<IncidentTimeline>>();

vi.mock("../api/incidents", async () => {
  const actual = await vi.importActual<typeof import("../api/incidents")>("../api/incidents");
  return {
    ...actual,
    fetchIncidents: () => fetchIncidents(),
    fetchIncidentTimeline: (id: string) => fetchIncidentTimeline(id),
  };
});

const session: MeResponse = {
  user: { username: "brian@pam", realm: "pam" },
  caps: {
    "": {
      netRead: true,
      netWrite: false,
      sdnRead: false,
      sdnWrite: false,
      fwRead: false,
      fwWrite: false,
      guestNet: false,
      audit: true,
      capture: false,
    },
  },
};

vi.mock("../api/useSession", () => ({
  useSession: () => ({ data: session }),
}));

function baseTimeline(partial: Partial<IncidentTimeline> = {}): IncidentTimeline {
  return {
    incident: {
      id: "inc-1",
      title: "vmbr0 down on pve2",
      status: "open",
      openedBy: "brian@pam",
      openedAt: 1_700_000_000,
      startedAt: 1_700_000_000,
      retroactive: false,
      annotations: [],
    },
    window: { from: 1_700_000_000, to: 1_700_000_100, live: false },
    events: [
      { id: "flow:1", at: 1_700_000_040, source: "flow", kind: "netflow9", summary: "flow 10.0.0.5 -> 10.0.0.9" },
      { id: "finding:1", at: 1_700_000_000, source: "finding", kind: "new", summary: "finding carrier_down new" },
      { id: "capture:start:c1", at: 1_700_000_030, source: "capture", kind: "started", summary: "capture started on vmbr0" },
      { id: "changeset:1", at: 1_700_000_010, source: "changeset", kind: "changeset.create", summary: "changeset.create cs-1" },
      { id: "annotation:a1", at: 1_700_000_045, source: "annotation", kind: "note", summary: "pulled the cable" },
      { id: "diagnosis:1", at: 1_700_000_020, source: "diagnosis", kind: "diagnose.run", summary: "diagnosis ladder run" },
    ],
    sources: [
      { source: "finding", status: "ok", count: 1 },
      { source: "changeset", status: "ok", count: 1 },
      { source: "diagnosis", status: "ok", count: 1 },
      { source: "capture", status: "ok", count: 1 },
      { source: "flow", status: "ok", count: 1 },
      { source: "annotation", status: "ok", count: 1 },
    ],
    caveats: ["the point-in-time diff compared /etc/network/interfaces only"],
    diff: {
      from: { requested: "1700000000", snapshotId: "snap-1", at: 1_700_000_000 },
      to: { requested: "1700000100", snapshotId: "snap-2", at: 1_700_000_100 },
      added: [],
      removed: [],
      modified: [],
      coverage: { nodes: ["pve1"], paths: ["/etc/network/interfaces"] },
      unattributedCount: 0,
    },
    ...partial,
  };
}

function renderPage(): void {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <MemoryRouter>
          <IncidentsPage />
        </MemoryRouter>
      </ToastProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  fetchIncidents.mockReset();
  fetchIncidentTimeline.mockReset();
  fetchIncidents.mockResolvedValue({
    items: [
      {
        id: "inc-1",
        title: "vmbr0 down on pve2",
        status: "open",
        openedBy: "brian@pam",
        openedAt: 1_700_000_000,
        startedAt: 1_700_000_000,
        retroactive: false,
        annotations: [],
      },
    ],
  });
});

describe("IncidentsPage", () => {
  it("renders all five sources plus notes on one interleaved timeline", async () => {
    fetchIncidentTimeline.mockResolvedValue(baseTimeline());
    renderPage();

    const incidentButton = await screen.findByRole("button", { name: /vmbr0 down on pve2/ });
    incidentButton.click();

    const list = await screen.findByTestId("incident-events");
    await waitFor(() => {
      expect(within(list).getAllByRole("listitem")).toHaveLength(6);
    });
    const sources = within(list)
      .getAllByRole("listitem")
      .map((li) => li.getAttribute("data-source"));
    expect(sources).toEqual(["finding", "changeset", "diagnosis", "capture", "flow", "annotation"]);
  });

  it("says why a source contributed nothing instead of showing an unexplained gap", async () => {
    fetchIncidentTimeline.mockResolvedValue(
      baseTimeline({
        sources: [
          { source: "finding", status: "ok", count: 1 },
          {
            source: "flow",
            status: "unavailable",
            count: 0,
            detail: "no flow samples are collected on this node",
          },
        ],
      }),
    );
    renderPage();
    (await screen.findByRole("button", { name: /vmbr0 down on pve2/ })).click();

    const gaps = await screen.findByTestId("incident-source-gaps");
    expect(gaps.textContent).toContain("Flows");
    expect(gaps.textContent).toContain("no flow samples are collected on this node");
  });

  it("shows a diff refusal as a refusal, never as 'nothing changed'", async () => {
    fetchIncidentTimeline.mockResolvedValue(
      baseTimeline({
        diff: undefined,
        diffErrorCode: "no_snapshot_in_range",
        diffError: 'change: no snapshot covers the from point "1700000000"; nearest available: snap-9 (scheduled)',
      }),
    );
    renderPage();
    (await screen.findByRole("button", { name: /vmbr0 down on pve2/ })).click();

    const strip = await screen.findByTestId("incident-diff");
    expect(strip.textContent).toContain("No point-in-time diff for this window");
    expect(strip.textContent).toContain("snap-9");
    expect(strip.textContent).not.toContain("nothing changed");
  });

  it("offers an export link for the selected incident", async () => {
    fetchIncidentTimeline.mockResolvedValue(baseTimeline());
    renderPage();
    (await screen.findByRole("button", { name: /vmbr0 down on pve2/ })).click();

    const link = await screen.findByRole("link", { name: "Export" });
    expect(link.getAttribute("href")).toBe("/api/v1/incidents/inc-1/export");
  });
});
