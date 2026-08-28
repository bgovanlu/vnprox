// SPDX-License-Identifier: Apache-2.0

// T-903 AC2: usePaletteActions registry — two simultaneously-mounted
// "pages" merge their actions without collision, and unmounting one only
// removes its own.
import { renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { usePaletteActions, usePaletteActionsStore, useAllPaletteActions, type PaletteAction } from "./actions";

afterEach(() => {
  usePaletteActionsStore.setState({ actionsByOwner: new Map(), allActions: [] });
});

const topologyActions: PaletteAction[] = [{ id: "edit-vmbr0", label: "Edit vmbr0", perform: () => undefined }];
const sdnActions: PaletteAction[] = [{ id: "new-vlan-zone", label: "New VLAN zone", perform: () => undefined }];

describe("usePaletteActions / useAllPaletteActions", () => {
  it("merges actions from two simultaneously-mounted owners without collision", () => {
    const topology = renderHook(() => {
      usePaletteActions("topology", topologyActions);
    });
    const sdn = renderHook(() => {
      usePaletteActions("sdn", sdnActions);
    });
    const all = renderHook(() => useAllPaletteActions());

    expect(all.result.current.map((a) => a.id).sort()).toEqual(["edit-vmbr0", "new-vlan-zone"]);

    topology.unmount();
    sdn.unmount();
  });

  it("unmounting one owner removes only its own actions", () => {
    const topology = renderHook(() => {
      usePaletteActions("topology", topologyActions);
    });
    const sdn = renderHook(() => {
      usePaletteActions("sdn", sdnActions);
    });
    const all = renderHook(() => useAllPaletteActions());

    topology.unmount();

    expect(all.result.current.map((a) => a.id)).toEqual(["new-vlan-zone"]);

    sdn.unmount();
    expect(all.result.current).toEqual([]);
  });

  it("re-registering under the same owner id replaces (not appends to) its previous actions", () => {
    const owner = renderHook(({ actions }: { actions: PaletteAction[] }) => {
      usePaletteActions("simulator", actions);
    }, { initialProps: { actions: topologyActions } });
    const all = renderHook(() => useAllPaletteActions());
    expect(all.result.current).toHaveLength(1);

    owner.rerender({ actions: sdnActions });
    expect(all.result.current.map((a) => a.id)).toEqual(["new-vlan-zone"]);

    owner.unmount();
    expect(all.result.current).toEqual([]);
  });
});
