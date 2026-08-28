// SPDX-License-Identifier: Apache-2.0

// T-3911: component-level tests for the composable dashboard grid —
// default layout, plugin tiles rendering through DashboardTile.tsx's
// shared shell (AC2), the explicit "tile unavailable" placeholder for a
// plugin tile whose provider is absent/disabled/erroring (this card's
// explicit graceful-degradation requirement), add/remove, and
// keyboard-only reorder (no drag — every control is a real `<button>`,
// operated here with `.focus()` + a real keyboard Enter, mirroring
// web/e2e/a11y.spec.ts's "no mouse input" convention).
//
// The built-in tile catalog (tileRegistry.ts) is mocked to two trivial
// dummy tiles so this file exercises the grid MECHANISM (composition,
// persistence, reorder, degrade-gracefully) in isolation from every real
// built-in tile's own data dependencies (findings/changesets/audit/etc,
// already covered by DashboardPage.test.tsx and each tile's own test).
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../api/client";
import type { DashboardLayoutPayload, DashboardTile as DashboardTileData } from "../api/types";
import { ToastProvider } from "../components/Toast";
import { DashboardGrid } from "./DashboardGrid";

vi.mock("./tileRegistry", () => {
  function Alpha() {
    return (
      <section aria-label="Alpha tile">
        <p>alpha body</p>
      </section>
    );
  }
  function Beta() {
    return (
      <section aria-label="Beta tile">
        <p>beta body</p>
      </section>
    );
  }
  const BUILTIN_TILES = [
    { id: "builtin:alpha", label: "Alpha tile", Component: Alpha },
    { id: "builtin:beta", label: "Beta tile", Component: Beta },
  ];
  return {
    BUILTIN_TILES,
    findBuiltinTile: (id: string) => BUILTIN_TILES.find((t) => t.id === id),
  };
});

const apiFetch = vi.hoisted(() => vi.fn());
vi.mock("../api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/client")>();
  return { ...actual, apiFetch };
});

let mockLayout: DashboardLayoutPayload | undefined;
let mockPluginTiles: DashboardTileData[];
let putCalls: DashboardLayoutPayload[];

function apiFetchImpl(path: string, options?: { method?: string; json?: unknown }): Promise<unknown> {
  if (path === "/dashboard/tiles") {
    return Promise.resolve({ items: mockPluginTiles });
  }
  if (path === "/layouts/dashboard") {
    if (options?.method === "PUT") {
      const body = options.json as { layout: DashboardLayoutPayload };
      putCalls.push(body.layout);
      mockLayout = body.layout;
      return Promise.resolve({ name: "dashboard", layout: body.layout, updatedAt: Date.now() });
    }
    return mockLayout
      ? Promise.resolve({ name: "dashboard", layout: mockLayout, updatedAt: 1 })
      : Promise.reject(new ApiError(404, "not_found", "no saved layout with that name"));
  }
  return Promise.reject(new Error(`DashboardGrid.test.tsx: unexpected apiFetch call: ${path}`));
}

function renderGrid() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <ToastProvider>
        <MemoryRouter>
          <DashboardGrid />
        </MemoryRouter>
      </ToastProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  mockLayout = undefined;
  mockPluginTiles = [];
  putCalls = [];
  apiFetch.mockReset();
  apiFetch.mockImplementation(apiFetchImpl);
});

describe("DashboardGrid: default layout", () => {
  it("renders every built-in tile, in order, for a user with no saved layout", async () => {
    renderGrid();
    await waitFor(() => {
      expect(screen.getByRole("region", { name: "Alpha tile" })).toBeInTheDocument();
    });
    expect(screen.getByRole("region", { name: "Beta tile" })).toBeInTheDocument();
    const regions = screen.getAllByRole("region").map((r) => r.getAttribute("aria-label"));
    expect(regions.indexOf("Alpha tile")).toBeLessThan(regions.indexOf("Beta tile"));
  });
});

describe("DashboardGrid: plugin tiles compose through the same shell (AC2)", () => {
  beforeEach(() => {
    mockLayout = {
      kind: "dashboard-tiles",
      tiles: [
        { id: "builtin:alpha", kind: "builtin" },
        { id: "plugin:sample-tile", kind: "plugin" },
      ],
    };
    mockPluginTiles = [{ id: "sample-tile", title: "Sample Tile", value: "42", detail: "from a plugin", link: "/topology" }];
  });

  it("renders the plugin tile's title/value/detail through DashboardTile.tsx's shared region+deep-link contract", async () => {
    renderGrid();
    const region = await screen.findByRole("region", { name: "Sample Tile" });
    expect(within(region).getByText("42")).toBeInTheDocument();
    expect(within(region).getByText("from a plugin")).toBeInTheDocument();
    // Same deep-link button contract every built-in tile uses.
    expect(within(region).getByRole("button", { name: "Open" })).toBeInTheDocument();
    // The built-in tile alongside it still renders normally.
    expect(screen.getByRole("region", { name: "Alpha tile" })).toBeInTheDocument();
  });
});

describe("DashboardGrid: a broken/absent/disabled plugin tile degrades gracefully", () => {
  beforeEach(() => {
    mockLayout = {
      kind: "dashboard-tiles",
      tiles: [
        { id: "builtin:alpha", kind: "builtin" },
        { id: "plugin:gone-tile", kind: "plugin" },
        { id: "builtin:beta", kind: "builtin" },
      ],
    };
    // The plugin behind "gone-tile" is disabled/uninstalled/erroring:
    // GET /dashboard/tiles simply no longer lists it (already dropped
    // server-side, per plugin.Registry.DashboardTiles' degrade-one-provider
    // contract) — this is the state the frontend actually observes.
    mockPluginTiles = [];
  });

  it("renders an explicit 'tile unavailable' placeholder instead of crashing or dropping the dashboard", async () => {
    renderGrid();
    const placeholder = await screen.findByRole("region", { name: "Tile unavailable" });
    expect(within(placeholder).getByText("Not available right now")).toBeInTheDocument();
    // Every other tile — before AND after the broken one in saved order —
    // still renders: one broken third-party tile does not break the rest.
    expect(screen.getByRole("region", { name: "Alpha tile" })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Beta tile" })).toBeInTheDocument();
    // A remove control is still offered so the operator can clear the slot.
    expect(screen.getByRole("button", { name: "Remove Unavailable tile from dashboard" })).toBeInTheDocument();
  });
});

describe("DashboardGrid: add / remove", () => {
  beforeEach(() => {
    mockLayout = { kind: "dashboard-tiles", tiles: [{ id: "builtin:alpha", kind: "builtin" }] };
  });

  it("adds a tile via the 'Add tile' menu and persists the new layout", async () => {
    const user = userEvent.setup();
    renderGrid();
    await screen.findByRole("region", { name: "Alpha tile" });
    // The grid renders the built-in default optimistically while the saved
    // layout is still loading (DashboardGrid.tsx's doc comment), so Beta
    // may flash briefly before the real (Beta-less) saved layout settles —
    // wait for that settle rather than asserting synchronously.
    await waitFor(() => {
      expect(screen.queryByRole("region", { name: "Beta tile" })).not.toBeInTheDocument();
    });

    await user.click(screen.getByRole("button", { name: "Add tile ▾" }));
    await user.click(await screen.findByRole("menuitem", { name: "Beta tile" }));

    await waitFor(() => {
      expect(screen.getByRole("region", { name: "Beta tile" })).toBeInTheDocument();
    });
    await waitFor(() => {
      expect(putCalls.at(-1)?.tiles).toEqual([
        { id: "builtin:alpha", kind: "builtin" },
        { id: "builtin:beta", kind: "builtin" },
      ]);
    });
  });

  it("removes a tile via its slot's Remove button and persists the new layout", async () => {
    mockLayout = {
      kind: "dashboard-tiles",
      tiles: [
        { id: "builtin:alpha", kind: "builtin" },
        { id: "builtin:beta", kind: "builtin" },
      ],
    };
    const user = userEvent.setup();
    renderGrid();
    await screen.findByRole("region", { name: "Beta tile" });

    await user.click(screen.getByRole("button", { name: "Remove Alpha tile from dashboard" }));

    await waitFor(() => {
      expect(screen.queryByRole("region", { name: "Alpha tile" })).not.toBeInTheDocument();
    });
    expect(screen.getByRole("region", { name: "Beta tile" })).toBeInTheDocument();
    await waitFor(() => {
      expect(putCalls.at(-1)?.tiles).toEqual([{ id: "builtin:beta", kind: "builtin" }]);
    });
  });
});

describe("DashboardGrid: reorder is keyboard-operable, not drag-only", () => {
  beforeEach(() => {
    mockLayout = {
      kind: "dashboard-tiles",
      tiles: [
        { id: "builtin:alpha", kind: "builtin" },
        { id: "builtin:beta", kind: "builtin" },
      ],
    };
  });

  it("moves a tile later using only .focus() + a real keyboard Enter (no pointer click, no drag)", async () => {
    const user = userEvent.setup();
    renderGrid();
    await screen.findByRole("region", { name: "Beta tile" });

    const moveLater = screen.getByRole("button", { name: "Move Alpha tile later" });
    moveLater.focus();
    expect(moveLater).toHaveFocus();
    await user.keyboard("{Enter}");

    await waitFor(() => {
      const regions = screen.getAllByRole("region").map((r) => r.getAttribute("aria-label"));
      expect(regions.indexOf("Beta tile")).toBeLessThan(regions.indexOf("Alpha tile"));
    });
    await waitFor(() => {
      expect(putCalls.at(-1)?.tiles).toEqual([
        { id: "builtin:beta", kind: "builtin" },
        { id: "builtin:alpha", kind: "builtin" },
      ]);
    });
  });

  it("disables 'move earlier' on the first tile and 'move later' on the last tile", async () => {
    renderGrid();
    await screen.findByRole("region", { name: "Beta tile" });
    expect(screen.getByRole("button", { name: "Move Alpha tile earlier" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Move Beta tile later" })).toBeDisabled();
  });
});
