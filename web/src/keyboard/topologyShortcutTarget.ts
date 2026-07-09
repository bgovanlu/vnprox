// A tiny zustand store bridging the app-wide keyboard shortcut framework
// (mounted once, in AppShell) to whichever page currently owns the
// topology-specific shortcuts (`1`-`4`, `f`, `/` — docs/user-guide.md §6).
// TopologyPage registers/unregisters a target on mount/unmount; when no
// target is registered (any other route), useKeyboardShortcuts falls back
// to an "open the Topology view" toast instead of the old "not yet
// implemented" one, since the feature is real now, just contextually
// unavailable outside the map.
import { create } from "zustand";
import type { Layer } from "../api/types";

export interface TopologyShortcutTarget {
  toggleLayer: (layer: Layer) => void;
  openVlanFilter: () => void;
  openSearch: () => void;
}

interface TopologyShortcutTargetState {
  target: TopologyShortcutTarget | null;
  setTarget: (target: TopologyShortcutTarget | null) => void;
}

export const useTopologyShortcutTargetStore = create<TopologyShortcutTargetState>((set) => ({
  target: null,
  setTarget: (target) => {
    set({ target });
  },
}));
