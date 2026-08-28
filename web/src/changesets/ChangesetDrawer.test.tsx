// SPDX-License-Identifier: Apache-2.0

// Component-level tests for the persistent changeset drawer: op-list
// rendering (summaries + validation badges), reorder, remove, and the
// review/discard affordances gating on the drawer state machine. The
// backend is mocked at the api/changesets.ts boundary (this component's
// only network dependency) so these run entirely against fake data, no
// server needed.
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ToastProvider } from "../components/Toast";
import type { Changeset } from "../api/types";
import { NARROW_VIEWPORT_QUERY } from "../lib/useNarrowViewport";
import { ChangesetDrawer } from "./ChangesetDrawer";
import { changesetKey } from "./queries";
import { useChangesetDrawerStore } from "./store";

/** T-909: stubs matchMedia so useNarrowViewport() reports a phone-width
 * viewport — mirrors lib/useReducedMotion.test.ts's fake. */
function stubNarrowViewport(matches: boolean): void {
  vi.stubGlobal(
    "matchMedia",
    vi.fn().mockReturnValue({
      matches,
      media: NARROW_VIEWPORT_QUERY,
      addEventListener: () => undefined,
      removeEventListener: () => undefined,
    }),
  );
}

vi.mock("../api/changesets", () => ({
  listChangesets: vi.fn(() => Promise.resolve([])),
  getChangeset: vi.fn(),
  createChangeset: vi.fn(),
  updateChangeset: vi.fn(),
  discardChangeset: vi.fn(),
  validateChangeset: vi.fn(),
  diffChangeset: vi.fn(() => Promise.resolve({ files: [], ops: [] })),
  applyChangeset: vi.fn(),
  confirmChangeset: vi.fn(),
  rollbackChangeset: vi.fn(),
}));

// The WS bridge opens a real browser WebSocket by default; stub it out so
// this component test doesn't try to reach a server.
vi.mock("../api/ws", () => ({
  createWsClient: () => ({ subscribe: () => () => undefined, status: () => "closed", close: () => undefined }),
  defaultWsUrl: () => "ws://unused",
}));

import * as changesetsApi from "../api/changesets";

function baseChangeset(overrides: Partial<Changeset> = {}): Changeset {
  return {
    id: "cs1",
    title: "My draft",
    author: "root@pam",
    status: "draft",
    ops: [
      { op: "bridge.create", target: "bridge:pve1:vmbr1", params: { ports: ["bond1"] } },
      { op: "vlan.create", target: "vlan:pve1:vmbr0.30", params: { parent: "vmbr0", vid: 30 } },
    ],
    findings: [{ severity: "error", code: "referential.exists", message: "bond1 does not exist", ref: "bridge:pve1:vmbr1" }],
    createdAt: 0,
    updatedAt: 0,
    ...overrides,
  };
}

function renderDrawer(): { queryClient: QueryClient } {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <ChangesetDrawer />
      </ToastProvider>
    </QueryClientProvider>,
  ) as unknown as ReactNode;
  return { queryClient };
}

describe("ChangesetDrawer", () => {
  beforeEach(() => {
    useChangesetDrawerStore.setState({
      activeId: undefined,
      drawerOpen: false,
      reviewRequested: false,
      warningsAcknowledged: false,
    });
    vi.mocked(changesetsApi.listChangesets).mockResolvedValue([]);
  });

  afterEach(() => {
    vi.clearAllMocks();
  });

  it("renders nothing when there's no active draft and nothing to resume", async () => {
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <ToastProvider>
          <ChangesetDrawer />
        </ToastProvider>
      </QueryClientProvider>,
    );
    // ToastProvider always renders its (empty) notification region — assert
    // the drawer's own region is absent, not that the whole DOM is empty.
    await waitFor(() => {
      expect(screen.queryByRole("region", { name: "Change drawer" })).not.toBeInTheDocument();
    });
  });

  it("shows the op count badge and expands to list op summaries with their validation badges", async () => {
    const cs = baseChangeset();
    const { queryClient } = renderDrawer();
    act(() => {
      queryClient.setQueryData(changesetKey(cs.id), cs);
      useChangesetDrawerStore.getState().setActiveId(cs.id);
    });

    expect(await screen.findByText("2")).toBeInTheDocument(); // op count badge
    expect(screen.getByText(/Create bridge vmbr1 with ports bond1/)).toBeInTheDocument();
    expect(screen.getByText(/Create VLAN vmbr0\.30/)).toBeInTheDocument();
    expect(screen.getByText("bond1 does not exist")).toBeInTheDocument();
    expect(screen.getByText("error")).toBeInTheDocument();
  });

  it("Review & apply is disabled with zero ops, enabled with at least one", async () => {
    const empty = baseChangeset({ ops: [], findings: [] });
    const { queryClient } = renderDrawer();
    act(() => {
      queryClient.setQueryData(changesetKey(empty.id), empty);
      useChangesetDrawerStore.getState().setActiveId(empty.id);
    });

    expect(await screen.findByText("Review & apply")).toBeDisabled();

    act(() => {
      queryClient.setQueryData(changesetKey(empty.id), baseChangeset());
    });
    await waitFor(() => {
      expect(screen.getByText("Review & apply")).not.toBeDisabled();
    });
  });

  /** getByText(...).closest("li") is typed nullable; assert-and-return so
   * tests don't need non-null assertions (banned by lint config). */
  function rowOf(text: RegExp): HTMLElement {
    const row = screen.getByText(text).closest("li");
    if (!row) throw new Error(`no <li> row found around text ${String(text)}`);
    return row;
  }

  it("removing an op calls updateChangeset with that op filtered out", async () => {
    const cs = baseChangeset();
    const [, secondOp] = cs.ops;
    if (!secondOp) throw new Error("fixture must have two ops");
    vi.mocked(changesetsApi.updateChangeset).mockResolvedValue({ ...cs, ops: [secondOp] });
    const { queryClient } = renderDrawer();
    act(() => {
      queryClient.setQueryData(changesetKey(cs.id), cs);
      useChangesetDrawerStore.getState().setActiveId(cs.id);
    });
    await screen.findByText(/Create bridge vmbr1/);

    const user = userEvent.setup();
    await user.click(within(rowOf(/Create bridge vmbr1/)).getByRole("button", { name: "Remove" }));

    await waitFor(() => {
      expect(changesetsApi.updateChangeset).toHaveBeenCalledWith("cs1", { title: undefined, ops: [cs.ops[1]] });
    });
  });

  it("reordering the first op down swaps it with the second, preserving both ops", async () => {
    const cs = baseChangeset();
    vi.mocked(changesetsApi.updateChangeset).mockResolvedValue(cs);
    const { queryClient } = renderDrawer();
    act(() => {
      queryClient.setQueryData(changesetKey(cs.id), cs);
      useChangesetDrawerStore.getState().setActiveId(cs.id);
    });
    await screen.findByText(/Create bridge vmbr1/);

    const user = userEvent.setup();
    await user.click(within(rowOf(/Create bridge vmbr1/)).getByRole("button", { name: "Move down" }));

    await waitFor(() => {
      expect(changesetsApi.updateChangeset).toHaveBeenCalledWith("cs1", { title: undefined, ops: [cs.ops[1], cs.ops[0]] });
    });
  });

  it("discarding an editable draft calls discardChangeset and clears the active id", async () => {
    const cs = baseChangeset({ status: "draft" });
    vi.mocked(changesetsApi.discardChangeset).mockResolvedValue(undefined);
    const { queryClient } = renderDrawer();
    act(() => {
      queryClient.setQueryData(changesetKey(cs.id), cs);
      useChangesetDrawerStore.getState().setActiveId(cs.id);
    });
    await screen.findByText(/Create bridge vmbr1/);

    const user = userEvent.setup();
    await user.click(screen.getByText("Discard"));

    await waitFor(() => {
      expect(changesetsApi.discardChangeset).toHaveBeenCalledWith("cs1");
      expect(useChangesetDrawerStore.getState().activeId).toBeUndefined();
    });
  });

  it("reorder/remove controls disappear and Discard becomes disabled once the changeset is no longer editable", async () => {
    const cs = baseChangeset({ status: "committed" });
    const { queryClient } = renderDrawer();
    act(() => {
      queryClient.setQueryData(changesetKey(cs.id), cs);
      useChangesetDrawerStore.getState().setActiveId(cs.id);
    });
    await screen.findByText(/Create bridge vmbr1/);

    expect(screen.queryByRole("button", { name: "Remove" })).not.toBeInTheDocument();
    expect(screen.getByText("Discard")).toBeDisabled();
  });

  describe("narrow viewport (T-909)", () => {
    afterEach(() => {
      vi.unstubAllGlobals();
    });

    it("hides reorder/remove but keeps Review & apply usable for an editable draft at phone width", async () => {
      stubNarrowViewport(true);
      const cs = baseChangeset();
      const { queryClient } = renderDrawer();
      act(() => {
        queryClient.setQueryData(changesetKey(cs.id), cs);
        useChangesetDrawerStore.getState().setActiveId(cs.id);
      });
      await screen.findByText(/Create bridge vmbr1/);

      // "Editing entities" (staging new ops) stays desktop-only...
      expect(screen.queryByRole("button", { name: "Move up" })).not.toBeInTheDocument();
      expect(screen.queryByRole("button", { name: "Move down" })).not.toBeInTheDocument();
      expect(screen.queryByRole("button", { name: "Remove" })).not.toBeInTheDocument();
      expect(screen.getByText("Discard")).toBeDisabled();

      // ...but taking an already-drafted changeset through review/apply (the
      // one write path this task must not degrade) stays reachable.
      expect(screen.getByText("Review & apply")).not.toBeDisabled();
    });

    it("re-enables the drafting controls once the viewport is no longer narrow", async () => {
      stubNarrowViewport(false);
      const cs = baseChangeset();
      const { queryClient } = renderDrawer();
      act(() => {
        queryClient.setQueryData(changesetKey(cs.id), cs);
        useChangesetDrawerStore.getState().setActiveId(cs.id);
      });
      await screen.findByText(/Create bridge vmbr1/);

      expect(screen.queryAllByRole("button", { name: "Remove" }).length).toBeGreaterThan(0);
      expect(screen.getByText("Discard")).not.toBeDisabled();
    });
  });
});
