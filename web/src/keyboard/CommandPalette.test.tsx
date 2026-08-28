// SPDX-License-Identifier: Apache-2.0

// T-903 AC1: typing a bridge name surfaces both the spotlight entity
// result (GET /inventory/search) and its registered palette action
// ("Edit vmbr0") in the same merged list, against a three-node-vlan-style
// search result for vmbr0.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { SearchResponse } from "../api/types";
import { CommandPalette } from "./CommandPalette";
import { usePaletteActionsStore, type PaletteAction } from "./actions";

const searchInventoryMock = vi.fn<(q: string) => Promise<SearchResponse>>();

vi.mock("../api/topology", () => ({
  searchInventory: (q: string) => searchInventoryMock(q),
}));

afterEach(() => {
  usePaletteActionsStore.setState({ actionsByOwner: new Map(), allActions: [] });
  // Each test configures its own resolved value; without a reset, a later
  // test with no explicit mock would otherwise still see an earlier test's
  // canned entity results for whatever query it types.
  searchInventoryMock.mockReset();
});

function registerAction(action: PaletteAction): void {
  usePaletteActionsStore.getState().setOwnerActions("test-owner", [action]);
}

function renderPalette(onOpenChange: (open: boolean) => void = vi.fn()) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <CommandPalette open onOpenChange={onOpenChange} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("CommandPalette", () => {
  it("merges the spotlight entity result and the registered action for a matching query", async () => {
    searchInventoryMock.mockResolvedValue({
      results: [{ ref: "bridge:pve1:vmbr0", kind: "bridge", label: "vmbr0", node: "pve1", matchedField: "name", score: 10 }],
    });
    const perform = vi.fn();
    registerAction({ id: "edit-vmbr0", label: "Edit vmbr0", hint: "Topology", perform });

    renderPalette();
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Command palette input"), "vmbr0");

    await waitFor(() => {
      expect(screen.getByText("vmbr0")).toBeInTheDocument();
    });
    expect(screen.getByText("Edit vmbr0")).toBeInTheDocument();
  });

  it("running a registered action calls perform() and closes the palette", async () => {
    searchInventoryMock.mockResolvedValue({ results: [] });
    const perform = vi.fn();
    registerAction({ id: "edit-vmbr0", label: "Edit vmbr0", perform });
    const onOpenChange = vi.fn();

    renderPalette(onOpenChange);
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Command palette input"), "Edit vmbr0");
    await waitFor(() => {
      expect(screen.getByText("Edit vmbr0")).toBeInTheDocument();
    });
    await user.click(screen.getByText("Edit vmbr0"));

    expect(perform).toHaveBeenCalledTimes(1);
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("filters out actions that don't match the typed query", async () => {
    searchInventoryMock.mockResolvedValue({ results: [] });
    registerAction({ id: "new-vlan-zone", label: "New VLAN zone", perform: vi.fn() });
    renderPalette();
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Command palette input"), "totally-unrelated-query");

    await waitFor(() => {
      expect(screen.getByText("No matches.")).toBeInTheDocument();
    });
    expect(screen.queryByText("New VLAN zone")).not.toBeInTheDocument();
  });
});
