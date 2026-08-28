// SPDX-License-Identifier: Apache-2.0

import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, useLocation } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ToastProvider } from "../components/Toast";
import { useTopologyStore } from "../topology/store";
import { useTopologyShortcutTargetStore } from "./topologyShortcutTarget";
import { useKeyboardShortcuts } from "./useKeyboardShortcuts";

function LocationProbe() {
  const loc = useLocation();
  return <div data-testid="location">{loc.pathname}</div>;
}

function Harness({
  onOpenHelp = vi.fn(),
  onOpenPalette = vi.fn(),
  onOpenPageHelp = vi.fn(),
}: {
  onOpenHelp?: () => void;
  onOpenPalette?: () => void;
  onOpenPageHelp?: () => void;
}) {
  useKeyboardShortcuts({ onOpenHelp, onOpenPalette, onOpenPageHelp });
  return <div>harness</div>;
}

function renderHarness() {
  return render(
    <MemoryRouter initialEntries={["/settings"]}>
      <ToastProvider>
        <Harness />
      </ToastProvider>
    </MemoryRouter>,
  );
}

describe("useKeyboardShortcuts — topology bindings", () => {
  afterEach(() => {
    useTopologyShortcutTargetStore.getState().setTarget(null);
    useTopologyStore.setState({ spotlightOpen: false });
  });

  it("shows a fallback toast for a topology shortcut when no target is registered", async () => {
    const user = userEvent.setup();
    renderHarness();

    await user.keyboard("1");

    expect(await screen.findByText(/only works on the Topology view/)).toBeInTheDocument();
  });

  // T-3403: "/" is the top bar's global search entry point (its rounded
  // search field's own kbd hint reads "/"), so unlike the other topology
  // shortcuts it must work from any page — opening the spotlight and
  // routing to Topology, exactly like clicking the search field does
  // (TopBar.tsx's openSearch) — instead of the "only works on Topology"
  // toast the other topology-only bindings fall back to.
  it("'/' opens the spotlight search and navigates to Topology when no target is registered", async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter initialEntries={["/settings"]}>
        <ToastProvider>
          <Harness />
          <LocationProbe />
        </ToastProvider>
      </MemoryRouter>,
    );

    await user.keyboard("/");

    expect(useTopologyStore.getState().spotlightOpen).toBe(true);
    expect(screen.getByTestId("location")).toHaveTextContent("/topology");
  });

  it("dispatches layer toggles (1-4) to the registered topology target", async () => {
    const toggleLayer = vi.fn();
    useTopologyShortcutTargetStore.getState().setTarget({
      toggleLayer,
      openVlanFilter: vi.fn(),
      openSearch: vi.fn(),
    });
    const user = userEvent.setup();
    renderHarness();

    await user.keyboard("1");
    await user.keyboard("2");
    await user.keyboard("3");
    await user.keyboard("4");

    expect(toggleLayer.mock.calls.map((c: unknown[]) => c[0])).toEqual(["phys", "l2", "sdn", "guest"]);
  });

  it("dispatches 'f' to openVlanFilter and '/' to openSearch on the registered target", async () => {
    const openVlanFilter = vi.fn();
    const openSearch = vi.fn();
    useTopologyShortcutTargetStore.getState().setTarget({
      toggleLayer: vi.fn(),
      openVlanFilter,
      openSearch,
    });
    const user = userEvent.setup();
    renderHarness();

    await user.keyboard("f");
    await user.keyboard("/");

    expect(openVlanFilter).toHaveBeenCalledTimes(1);
    expect(openSearch).toHaveBeenCalledTimes(1);
  });

  it("does not dispatch topology shortcuts while focus is in a text input", async () => {
    const toggleLayer = vi.fn();
    useTopologyShortcutTargetStore.getState().setTarget({
      toggleLayer,
      openVlanFilter: vi.fn(),
      openSearch: vi.fn(),
    });
    render(
      <MemoryRouter>
        <ToastProvider>
          <input aria-label="some field" />
          <Harness />
        </ToastProvider>
      </MemoryRouter>,
    );
    const user = userEvent.setup();
    const input = screen.getByLabelText("some field");
    await user.click(input);
    await user.keyboard("1");

    expect(toggleLayer).not.toHaveBeenCalled();
  });

  it("still opens the help dialog on '?' regardless of topology target registration", async () => {
    const onOpenHelp = vi.fn();
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <ToastProvider>
          <Harness onOpenHelp={onOpenHelp} />
        </ToastProvider>
      </MemoryRouter>,
    );

    await act(async () => {
      await user.keyboard("?");
    });

    expect(onOpenHelp).toHaveBeenCalledTimes(1);
  });

  it("opens the command palette on Ctrl+K", async () => {
    const onOpenPalette = vi.fn();
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <ToastProvider>
          <Harness onOpenPalette={onOpenPalette} />
        </ToastProvider>
      </MemoryRouter>,
    );

    await user.keyboard("{Control>}k{/Control}");

    expect(onOpenPalette).toHaveBeenCalledTimes(1);
  });

  it("opens the command palette on Cmd+K (metaKey)", async () => {
    const onOpenPalette = vi.fn();
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <ToastProvider>
          <Harness onOpenPalette={onOpenPalette} />
        </ToastProvider>
      </MemoryRouter>,
    );

    await user.keyboard("{Meta>}k{/Meta}");

    expect(onOpenPalette).toHaveBeenCalledTimes(1);
  });

  it("opens the command palette on Ctrl+K even while focus is in a text input", async () => {
    const onOpenPalette = vi.fn();
    render(
      <MemoryRouter>
        <ToastProvider>
          <input aria-label="some field" />
          <Harness onOpenPalette={onOpenPalette} />
        </ToastProvider>
      </MemoryRouter>,
    );
    const user = userEvent.setup();
    await user.click(screen.getByLabelText("some field"));

    await user.keyboard("{Control>}k{/Control}");

    expect(onOpenPalette).toHaveBeenCalledTimes(1);
  });

  it("does not treat plain 'k' (no modifier) as the palette binding", async () => {
    const onOpenPalette = vi.fn();
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <ToastProvider>
          <Harness onOpenPalette={onOpenPalette} />
        </ToastProvider>
      </MemoryRouter>,
    );

    await user.keyboard("k");

    expect(onOpenPalette).not.toHaveBeenCalled();
  });
});

// T-2204: F1 is the online-help binding, deliberately separate from `?`
// (the keyboard-shortcut list) so neither displaces the other.
describe("useKeyboardShortcuts — F1 contextual help", () => {
  it("opens page help on F1, and does not open the shortcut dialog", async () => {
    const onOpenPageHelp = vi.fn();
    const onOpenHelp = vi.fn();
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <ToastProvider>
          <Harness onOpenPageHelp={onOpenPageHelp} onOpenHelp={onOpenHelp} />
        </ToastProvider>
      </MemoryRouter>,
    );

    await user.keyboard("{F1}");

    expect(onOpenPageHelp).toHaveBeenCalledTimes(1);
    expect(onOpenHelp).not.toHaveBeenCalled();
  });

  it("opens page help on F1 even while focus is in a text input", async () => {
    const onOpenPageHelp = vi.fn();
    render(
      <MemoryRouter>
        <ToastProvider>
          <input aria-label="some field" />
          <Harness onOpenPageHelp={onOpenPageHelp} />
        </ToastProvider>
      </MemoryRouter>,
    );
    const user = userEvent.setup();
    await user.click(screen.getByLabelText("some field"));

    await user.keyboard("{F1}");

    expect(onOpenPageHelp).toHaveBeenCalledTimes(1);
  });

  it("leaves `?` bound to the shortcut dialog, not to page help", async () => {
    const onOpenPageHelp = vi.fn();
    const onOpenHelp = vi.fn();
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <ToastProvider>
          <Harness onOpenPageHelp={onOpenPageHelp} onOpenHelp={onOpenHelp} />
        </ToastProvider>
      </MemoryRouter>,
    );

    await act(async () => {
      await user.keyboard("?");
    });

    expect(onOpenHelp).toHaveBeenCalledTimes(1);
    expect(onOpenPageHelp).not.toHaveBeenCalled();
  });
});
