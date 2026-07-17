import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ToastProvider } from "../components/Toast";
import { useTopologyShortcutTargetStore } from "./topologyShortcutTarget";
import { useKeyboardShortcuts } from "./useKeyboardShortcuts";

function Harness({
  onOpenHelp = vi.fn(),
  onOpenPalette = vi.fn(),
}: {
  onOpenHelp?: () => void;
  onOpenPalette?: () => void;
}) {
  useKeyboardShortcuts({ onOpenHelp, onOpenPalette });
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
  });

  it("shows a fallback toast for a topology shortcut when no target is registered", async () => {
    const user = userEvent.setup();
    renderHarness();

    await user.keyboard("1");

    expect(await screen.findByText(/only works on the Topology view/)).toBeInTheDocument();
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
