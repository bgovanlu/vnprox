// SPDX-License-Identifier: Apache-2.0

// T-903 AC3: the help dialog renders the ⌘K/Ctrl+K palette binding plus at
// least the four named verbs' shortcuts where bound (each currently-
// registered palette action, reachable via that same binding).
import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { ShortcutHelpDialog } from "./ShortcutHelpDialog";
import { usePaletteActionsStore } from "./actions";

afterEach(() => {
  usePaletteActionsStore.setState({ actionsByOwner: new Map(), allActions: [] });
});

describe("ShortcutHelpDialog", () => {
  it("always lists the ⌘K/Ctrl+K command-palette binding", () => {
    render(<ShortcutHelpDialog open onOpenChange={() => undefined} />);
    expect(screen.getByText("⌘K / Ctrl+K")).toBeInTheDocument();
    expect(screen.getByText("Open command palette")).toBeInTheDocument();
  });

  it("lists every currently-registered palette verb from the four named pages", () => {
    usePaletteActionsStore.getState().setOwnerActions("topology", [
      { id: "edit-vmbr0", label: "Edit vmbr0", perform: () => undefined },
    ]);
    usePaletteActionsStore.getState().setOwnerActions("sdn", [
      { id: "new-vlan-zone", label: "New VLAN zone", perform: () => undefined },
    ]);
    usePaletteActionsStore.getState().setOwnerActions("changesets", [
      { id: "open-drafts", label: "Open drafts", perform: () => undefined },
    ]);
    usePaletteActionsStore.getState().setOwnerActions("simulator", [
      { id: "simulate-from-app01", label: "Simulate path from app01/net0", perform: () => undefined },
    ]);

    render(<ShortcutHelpDialog open onOpenChange={() => undefined} />);

    expect(screen.getByText("Edit vmbr0")).toBeInTheDocument();
    expect(screen.getByText("New VLAN zone")).toBeInTheDocument();
    expect(screen.getByText("Open drafts")).toBeInTheDocument();
    expect(screen.getByText("Simulate path from app01/net0")).toBeInTheDocument();
  });

  it("shows no 'available now' section when nothing is registered", () => {
    render(<ShortcutHelpDialog open onOpenChange={() => undefined} />);
    expect(screen.queryByText("Available now, via the command palette")).not.toBeInTheDocument();
  });
});
