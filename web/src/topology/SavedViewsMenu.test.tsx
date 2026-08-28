// SPDX-License-Identifier: Apache-2.0

// T-907 AC1/AC2 (Vitest half of the UI wiring — savedViews.test.ts covers
// the pure encode/decode/round-trip logic this component builds on).
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { ToastProvider } from "../components/Toast";
import type { SavedViewState } from "./savedViews";
import { SavedViewsMenu } from "./SavedViewsMenu";

const saveMutate = vi.fn();
const deleteMutate = vi.fn();
const clipboardWriteText = vi.fn((_text: string) => Promise.resolve());
let savedViewsList: { name: string; updatedAt: number }[] = [];

vi.mock("./savedViewsQueries", () => ({
  useSavedViewsQuery: () => ({ data: savedViewsList }),
  useSaveViewMutation: () => ({ mutate: saveMutate }),
  useDeleteViewMutation: () => ({ mutate: deleteMutate }),
  loadSavedView: vi.fn(),
}));

import { loadSavedView } from "./savedViewsQueries";

const CURRENT_STATE: SavedViewState = {
  layers: ["phys", "sdn"],
  vlanFilter: 100,
  zoom: 1.4,
  viewport: { x: -120, y: 40 },
  selection: "bridge:pve1:vmbr0",
  view: "graph",
};

function renderMenu(onLoad: (state: SavedViewState) => void = () => undefined) {
  return render(
    <ToastProvider>
      <SavedViewsMenu getCurrentState={() => CURRENT_STATE} onLoad={onLoad} />
    </ToastProvider>,
  );
}

beforeEach(() => {
  saveMutate.mockReset();
  deleteMutate.mockReset();
  vi.mocked(loadSavedView).mockReset();
  savedViewsList = [];
  vi.stubGlobal("prompt", vi.fn());
  clipboardWriteText.mockClear();
});

describe("SavedViewsMenu", () => {
  it("shows 'No saved views yet.' when the user has none", async () => {
    const user = userEvent.setup();
    renderMenu();
    await user.click(screen.getByRole("button", { name: "Views ▾" }));
    await screen.findByText("No saved views yet.");
  });

  it("prompts for a name and saves the current state (T-907 AC1)", async () => {
    vi.mocked(window.prompt).mockReturnValue("my great filter");
    const user = userEvent.setup();
    renderMenu();
    await user.click(screen.getByRole("button", { name: "Views ▾" }));
    await user.click(await screen.findByText("Save current view…"));

    expect(window.prompt).toHaveBeenCalled();
    expect(saveMutate).toHaveBeenCalledWith(
      { name: "my great filter", state: CURRENT_STATE },
      expect.anything(),
    );
  });

  it("does not save when the name prompt is cancelled", async () => {
    vi.mocked(window.prompt).mockReturnValue(null);
    const user = userEvent.setup();
    renderMenu();
    await user.click(screen.getByRole("button", { name: "Views ▾" }));
    await user.click(await screen.findByText("Save current view…"));
    expect(saveMutate).not.toHaveBeenCalled();
  });

  it("loads a saved view and applies it via onLoad", async () => {
    savedViewsList = [{ name: "my view", updatedAt: 100 }];
    const loadedState: SavedViewState = { ...CURRENT_STATE, zoom: 2 };
    vi.mocked(loadSavedView).mockResolvedValue(loadedState);
    const onLoad = vi.fn();
    const user = userEvent.setup();
    renderMenu(onLoad);

    await user.click(screen.getByRole("button", { name: "Views ▾" }));
    await user.click(await screen.findByText("my view"));

    expect(loadSavedView).toHaveBeenCalledWith("my view");
    await vi.waitFor(() => {
      expect(onLoad).toHaveBeenCalledWith(loadedState);
    });
  });

  it("deletes a saved view without triggering a load", async () => {
    savedViewsList = [{ name: "my view", updatedAt: 100 }];
    const onLoad = vi.fn();
    const user = userEvent.setup();
    renderMenu(onLoad);

    await user.click(screen.getByRole("button", { name: "Views ▾" }));
    await user.click(await screen.findByRole("button", { name: "Delete saved view: my view" }));

    expect(deleteMutate).toHaveBeenCalledWith("my view", expect.anything());
    expect(loadSavedView).not.toHaveBeenCalled();
  });

  it("copies a shareable link that carries the current state (T-907 AC2)", async () => {
    const user = userEvent.setup();
    // Stubbed here, after userEvent.setup() (not in beforeEach): user-event's
    // own setup() installs its own navigator.clipboard, which would clobber
    // an earlier stub — this ordering is the one that actually sticks.
    Object.defineProperty(navigator, "clipboard", {
      value: { writeText: clipboardWriteText },
      configurable: true,
    });
    renderMenu();
    await user.click(screen.getByRole("button", { name: "Views ▾" }));
    await user.click(await screen.findByText("Copy share link"));

    await vi.waitFor(() => {
      expect(clipboardWriteText).toHaveBeenCalledTimes(1);
    });
    const [call] = clipboardWriteText.mock.calls;
    if (!call) throw new Error("expected clipboard.writeText to have been called");
    const [copied] = call;
    expect(copied).toContain("svLayers=phys%2Csdn");
    expect(copied).toContain("svZoom=1.4");
    expect(copied).toContain("svSel=bridge%3Apve1%3Avmbr0");
  });
});
