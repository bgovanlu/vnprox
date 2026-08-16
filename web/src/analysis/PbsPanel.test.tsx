// The PBS panel's own honesty invariants: an unresolved carrier and an
// unknown link speed each render as unknown, and "hosts exist but no path
// resolved" is a distinct state from "no PBS at all".
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import type { PbsOverlay } from "../api/types";
import { PbsPanel } from "./PbsPanel";

let pbsResult: { data: PbsOverlay | undefined; isLoading: boolean; error: Error | null } = {
  data: { hosts: [], paths: [] },
  isLoading: false,
  error: null,
};

vi.mock("./analysisQueries", () => ({
  usePbsOverlayQuery: () => pbsResult,
}));

function renderPanel(data: PbsOverlay) {
  pbsResult = { data, isLoading: false, error: null };
  return render(
    <MemoryRouter>
      <PbsPanel />
    </MemoryRouter>,
  );
}

describe("PbsPanel", () => {
  it("renders an unknown link speed as unknown, never as a number", () => {
    // linkSpeedKnown is a separate field from linkMbps precisely so a
    // missing speed cannot be shown as 0 Mbit/s.
    renderPanel({
      hosts: [{ ref: "pbs-host::pbs1", address: "10.0.0.9" }],
      paths: [
        {
          node: "pve1",
          host: "pbs-host::pbs1",
          carrier: "physnic:pve1:eno1",
          sizingHint: "Backups fit comfortably.",
          linkMbps: 0,
          linkSpeedKnown: false,
        },
      ],
    });

    expect(screen.getByText("unknown")).toBeInTheDocument();
    expect(screen.queryByText("0 Mbit/s")).not.toBeInTheDocument();
  });

  it("renders an unresolved carrier as unresolved", () => {
    renderPanel({
      hosts: [{ ref: "pbs-host::pbs1", address: "10.0.0.9" }],
      paths: [{ node: "pve1", host: "pbs-host::pbs1", sizingHint: "hint", linkSpeedKnown: false }],
    });

    expect(screen.getByText("unresolved")).toBeInTheDocument();
  });

  it("distinguishes 'no PBS configured' from 'PBS known, path unresolved'", () => {
    renderPanel({ hosts: [], paths: [] });
    expect(screen.getByText("No PBS hosts configured")).toBeInTheDocument();
    expect(screen.queryByText("No backup path resolved")).not.toBeInTheDocument();

    renderPanel({ hosts: [{ ref: "pbs-host::pbs1", address: "10.0.0.9" }], paths: [] });
    expect(screen.getByText("No backup path resolved")).toBeInTheDocument();
  });

  it("names a failed read as a failed read", () => {
    pbsResult = { data: undefined, isLoading: false, error: new Error("boom") };
    render(
      <MemoryRouter>
        <PbsPanel />
      </MemoryRouter>,
    );
    expect(screen.getByText("Could not read PBS status")).toBeInTheDocument();
    expect(screen.queryByText("No PBS hosts configured")).not.toBeInTheDocument();
  });
});
