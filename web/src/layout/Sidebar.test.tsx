// SPDX-License-Identifier: Apache-2.0

// T-3402: replaces NavRail.test.tsx (retired alongside NavRail.tsx). Keeps
// AC1's "Home entry highlights when active" case (NavRail.test.tsx's own
// origin, T-904) and adds this task's own acceptance criteria: every
// routed nav entry (23 as of T-3906's "Guest view" addition) renders
// exactly once, active-route auto-expand, collapse state
// round-tripping through localStorage (the store's own persistence is
// covered in isolation by sidebarGroupsStore.test.ts; this file covers the
// store <-> rendered-UI wiring), the findings badge, and the narrow-
// viewport reachable-set filter (T-909).
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Sidebar } from "./Sidebar";
import { useSidebarGroupsStore } from "./sidebarGroupsStore";
import { useDemoStore } from "../demo/useDemoMode";
import { NARROW_VIEWPORT_QUERY } from "../lib/useNarrowViewport";
import { fetchFindings } from "../api/findings";
import type { StreamFinding } from "../api/types";

vi.mock("../api/findings", () => ({
  fetchFindings: vi.fn(() => Promise.resolve([])),
  fixFinding: vi.fn(),
}));

/** Every route this task's card names, by group — the source of truth this
 * file checks the rendered sidebar against, independent of Sidebar.tsx's
 * own internal shape. */
const FLAT_LABELS = ["Home", "Topology", "Guests", "Guest view", "Management"];
const NETWORK_LABELS = [
  "SDN",
  "Firewall",
  "IPAM",
  "Ports",
  "Cabling plan",
  "Edge",
  "Route explorer",
  "Flows",
  "Conntrack",
  // T-4015: the general WireGuard tunnel management surface.
  "WireGuard",
];
const OPERATE_LABELS = ["History", "Incidents", "Audit", "Analysis", "Tools"];
const AUTOMATE_LABELS = ["Config as code", "Governance", "Blueprints", "Hub"];
const ALL_LABELS = [...FLAT_LABELS, ...NETWORK_LABELS, ...OPERATE_LABELS, ...AUTOMATE_LABELS, "Settings"];

function fakeMatchMedia(matches: boolean) {
  const mql: Partial<MediaQueryList> & { matches: boolean } = {
    matches,
    media: NARROW_VIEWPORT_QUERY,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
  };
  return () => mql as MediaQueryList;
}

function renderAt(path: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="*" element={<Sidebar />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  localStorage.clear();
  // Every group starts at its "never toggled" default (expanded) —
  // otherwise a prior test's collapse would leak into the next one, since
  // the store is a module-level singleton shared across this whole file.
  useSidebarGroupsStore.setState({ expanded: {} });
  useDemoStore.setState({ demo: undefined });
  vi.stubGlobal("matchMedia", fakeMatchMedia(false));
});

afterEach(() => {
  localStorage.clear();
  vi.unstubAllGlobals();
});

describe("Sidebar — route inventory", () => {
  it("renders every routed nav entry exactly once, at full (non-narrow) width", () => {
    renderAt("/");
    for (const label of ALL_LABELS) {
      expect(screen.getAllByRole("link", { name: label })).toHaveLength(1);
    }
    expect(screen.getAllByRole("link")).toHaveLength(ALL_LABELS.length);
  });

  it("groups the routes exactly as the card specifies", () => {
    renderAt("/");
    // Every grouped label sits inside its own disclosure section, alongside
    // its group siblings and nothing from another group. The section wraps
    // both the toggle button and its panel, so scoping the lookup to it
    // (rather than to the panel alone) also proves the button and its
    // items are DOM-adjacent, not merely both present somewhere on the
    // page.
    const networkPanel = screen.getByRole("button", { name: "Network" }).closest("div");
    expect(networkPanel).not.toBeNull();
    if (networkPanel) {
      for (const label of NETWORK_LABELS) {
        expect(within(networkPanel).getByRole("link", { name: label })).toBeInTheDocument();
      }
      for (const label of [...OPERATE_LABELS, ...AUTOMATE_LABELS]) {
        expect(within(networkPanel).queryByRole("link", { name: label })).not.toBeInTheDocument();
      }
    }
  });
});

/** T-4214 replaced the copy-pasted `bg-accent-600/10 dark:bg-accent-500/15`
 * recipe — verbatim in nine files — with the `--color-accent-soft` role, which
 * re-points per theme so the `dark:` half is gone. Named once here rather than
 * repeated at four call sites: these assertions are about "the active item is
 * washed", not about which hex does the washing. */
const ACTIVE_WASH = "bg-accent-soft";

describe("Sidebar — Home entry (ported from NavRail.test.tsx, T-904 AC1)", () => {
  it("highlights Home at the index route", () => {
    renderAt("/");
    const home = screen.getByRole("link", { name: "Home" });
    expect(home.className).toContain(ACTIVE_WASH);
  });

  it("does not highlight Home (or leave every other flat item active) on another route", () => {
    renderAt("/topology");
    const home = screen.getByRole("link", { name: "Home" });
    expect(home.className).not.toContain(ACTIVE_WASH);
    const topology = screen.getByRole("link", { name: "Topology" });
    expect(topology.className).toContain(ACTIVE_WASH);
  });
});

describe("Sidebar — collapsible groups", () => {
  it("collapses a group on click: items leave the DOM, aria-expanded flips, and the choice is written to localStorage", async () => {
    renderAt("/");
    const networkToggle = screen.getByRole("button", { name: "Network" });
    expect(networkToggle).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByRole("link", { name: "SDN" })).toBeInTheDocument();

    await userEvent.click(networkToggle);

    expect(networkToggle).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("link", { name: "SDN" })).not.toBeInTheDocument();

    const stored = localStorage.getItem("vnprox.sidebarGroups");
    expect(stored).toBeTruthy();
    expect(JSON.parse(stored ?? "{}")).toMatchObject({ state: { expanded: { network: false } } });
  });

  it("a group containing the active route auto-expands on navigation, even if it was collapsed", () => {
    // Collapse Network before the active route ever lands inside it — the
    // same shape as a user landing on a deep link with a group they'd
    // previously collapsed.
    useSidebarGroupsStore.getState().setExpanded("network", false);

    renderAt("/sdn");

    const networkToggle = screen.getByRole("button", { name: "Network" });
    expect(networkToggle).toHaveAttribute("aria-expanded", "true");
    const sdn = screen.getByRole("link", { name: "SDN" });
    expect(sdn).toBeInTheDocument();
    expect(sdn.className).toContain(ACTIVE_WASH);
  });

  it("leaves an inactive collapsed group collapsed", () => {
    useSidebarGroupsStore.getState().setExpanded("automate", false);
    renderAt("/sdn");

    expect(screen.getByRole("button", { name: "Automate" })).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("link", { name: "Hub" })).not.toBeInTheDocument();
  });
});

describe("Sidebar — findings badge (T-602, T-2004 contrast fix)", () => {
  it("renders no badge when there are no findings", async () => {
    renderAt("/");
    expect(await screen.findByRole("link", { name: "Tools" })).toBeInTheDocument();
    expect(screen.queryByLabelText(/finding/)).not.toBeInTheDocument();
  });

  it("renders the count, with dark text on solid amber (no opacity)", async () => {
    const finding = (id: string): StreamFinding => ({
      id,
      source: "drift",
      check: "test-check",
      severity: "warning",
      detail: "test finding",
      nodes: ["pve1"],
      fixable: false,
    });
    vi.mocked(fetchFindings).mockResolvedValueOnce([finding("f1"), finding("f2"), finding("f3")]);
    renderAt("/");

    const badge = await screen.findByLabelText("3 findings");
    expect(badge).toHaveTextContent("3");
    expect(badge.className).toContain("bg-amber-500");
    expect(badge.className).toContain("text-slate-900");
    expect(badge.className).not.toContain("bg-amber-500/90");
    expect(badge.className).not.toContain("opacity");
  });
});

describe("Sidebar — narrow viewport (T-909)", () => {
  it("filters to only Home and Tools, and drops Settings entirely", () => {
    vi.stubGlobal("matchMedia", fakeMatchMedia(true));
    renderAt("/");

    expect(screen.getByRole("link", { name: "Home" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Tools" })).toBeInTheDocument();
    expect(screen.getAllByRole("link")).toHaveLength(2);
    expect(screen.queryByRole("link", { name: "Topology" })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Settings" })).not.toBeInTheDocument();
    // No group disclosure chrome at narrow width — grouping is a
    // >=768px-only affordance.
    expect(screen.queryByRole("button", { name: "Network" })).not.toBeInTheDocument();
  });

  it("still gives each narrow-width link an explicit accessible name", () => {
    vi.stubGlobal("matchMedia", fakeMatchMedia(true));
    renderAt("/");
    for (const link of screen.getAllByRole("link")) {
      expect(link).toHaveAccessibleName();
    }
  });
});

describe("Sidebar — identity chip", () => {
  it("names the product, with no demo label when this is not a demo instance", () => {
    renderAt("/");
    expect(screen.getByText("vnprox")).toBeInTheDocument();
    expect(screen.queryByText("Demo")).not.toBeInTheDocument();
  });

  it("labels demo mode distinctly (T-2801's demo flag from /health, via useIsDemo)", () => {
    useDemoStore.setState({ demo: true });
    renderAt("/");
    expect(screen.getByText("vnprox")).toBeInTheDocument();
    expect(screen.getByText("Demo")).toBeInTheDocument();
  });
});

describe("Sidebar — load-bearing chrome invariants", () => {
  it("keeps aria-label=\"Primary\" and z-50 on the nav element (T-906 print CSS, T-909 CountdownBanner overlap)", () => {
    renderAt("/");
    const nav = screen.getByRole("navigation", { name: "Primary" });
    expect(nav.className).toContain("relative");
    expect(nav.className).toContain("z-50");
  });
});
