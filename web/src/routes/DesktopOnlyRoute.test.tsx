// T-909 acceptance criterion 3: "Navigating to a desktop-only route ... at
// phone width shows the explicit 'desktop only' affordance with actionable
// copy, not a broken layout — Vitest test on the route-guard component".
import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { NARROW_VIEWPORT_QUERY } from "../lib/useNarrowViewport";
import { DesktopOnlyRoute } from "./DesktopOnlyRoute";

function fakeMatchMedia(matches: boolean) {
  const mql: Partial<MediaQueryList> & { matches: boolean } = {
    matches,
    media: NARROW_VIEWPORT_QUERY,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
  };
  return vi.fn().mockReturnValue(mql);
}

function renderGuard(matches: boolean) {
  vi.stubGlobal("matchMedia", fakeMatchMedia(matches));
  return render(
    <MemoryRouter>
      <DesktopOnlyRoute pageLabel="Topology">
        <div>Real Topology content</div>
      </DesktopOnlyRoute>
    </MemoryRouter>,
  );
}

describe("DesktopOnlyRoute", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("renders the wrapped page at desktop width", () => {
    renderGuard(false);
    expect(screen.getByText("Real Topology content")).toBeInTheDocument();
    expect(screen.queryByText(/needs a larger screen/i)).not.toBeInTheDocument();
  });

  it("renders an explicit, actionable desktop-only notice instead of the page at narrow width", () => {
    renderGuard(true);
    expect(screen.queryByText("Real Topology content")).not.toBeInTheDocument();
    expect(screen.getByText(/Topology needs a larger screen/i)).toBeInTheDocument();
    // Actionable copy: names what the visitor CAN do from this device, not
    // just "unavailable" — links back to the narrow-reachable pages plus a
    // mention that changeset confirm/rollback still works.
    expect(screen.getByRole("link", { name: /Go to Dashboard/i })).toHaveAttribute("href", "/");
    expect(screen.getByRole("link", { name: /Go to Findings/i })).toHaveAttribute("href", "/tools");
    expect(screen.getByText(/confirm\/roll back controls still work/i)).toBeInTheDocument();
  });

  it("uses a page-specific detail string when provided (e.g. naming a wizard)", () => {
    vi.stubGlobal("matchMedia", fakeMatchMedia(true));
    render(
      <MemoryRouter>
        <DesktopOnlyRoute pageLabel="SDN" detail="The SDN zone/vnet wizards need a desktop-sized screen.">
          <div>Real SDN content</div>
        </DesktopOnlyRoute>
      </MemoryRouter>,
    );
    expect(screen.getByText("The SDN zone/vnet wizards need a desktop-sized screen.")).toBeInTheDocument();
  });
});
