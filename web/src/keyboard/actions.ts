// SPDX-License-Identifier: Apache-2.0

// T-903: the command-palette action registry. Pages register their own
// verbs ("Edit vmbr0", "New VLAN zone", "Open drafts", "Simulate path from
// <entity>") on mount and unregister on unmount — the same lifecycle
// src/keyboard/topologyShortcutTarget.ts already uses for the Topology
// page's shortcut handlers, just keyed by an arbitrary owner id instead of
// a single well-known slot, since any number of pages/panels can each
// contribute actions at once (docs/features/topology.md §2's spotlight
// search plus every page's own verbs, all merged in CommandPalette.tsx).
import { useEffect } from "react";
import { create } from "zustand";

export interface PaletteAction {
  /** Unique among this owner's own actions; only needs to be globally
   * unique in practice (callers derive it from a stable ref/kind), since
   * CommandPalette keys its rendered list on `${ownerId}:${id}`. */
  readonly id: string;
  readonly label: string;
  /** Optional secondary text (e.g. the page/feature the verb belongs to),
   * shown de-emphasized next to the label. */
  readonly hint?: string;
  /** Extra terms the palette's fuzzy-free substring filter also matches
   * against, beyond `label` itself (e.g. an entity's kind or node). */
  readonly keywords?: readonly string[];
  readonly perform: () => void;
}

interface PaletteActionsState {
  actionsByOwner: Map<string, readonly PaletteAction[]>;
  /** The flattened action list, recomputed only when `actionsByOwner`
   * itself changes (setOwnerActions/clearOwnerActions below) rather than on
   * every read. useAllPaletteActions selects this field directly — a
   * selector that instead ran `Array.from(...).flat()` inline would return
   * a brand-new array on every single render regardless of whether
   * anything actually changed, which is not just wasteful but actively
   * breaks React 18's `useSyncExternalStore` (the hook zustand v5 selectors
   * are built on): a snapshot that's never reference-equal to its own
   * previous value looks like a perpetual store change, so every
   * subscribed component would re-render in an infinite loop. */
  allActions: readonly PaletteAction[];
  setOwnerActions: (ownerId: string, actions: readonly PaletteAction[]) => void;
  clearOwnerActions: (ownerId: string) => void;
}

function flatten(actionsByOwner: Map<string, readonly PaletteAction[]>): PaletteAction[] {
  return Array.from(actionsByOwner.values()).flat();
}

export const usePaletteActionsStore = create<PaletteActionsState>((set) => ({
  actionsByOwner: new Map(),
  allActions: [],
  setOwnerActions: (ownerId, actions) => {
    set((state) => {
      const next = new Map(state.actionsByOwner);
      next.set(ownerId, actions);
      return { actionsByOwner: next, allActions: flatten(next) };
    });
  },
  clearOwnerActions: (ownerId) => {
    set((state) => {
      if (!state.actionsByOwner.has(ownerId)) return state;
      const next = new Map(state.actionsByOwner);
      next.delete(ownerId);
      return { actionsByOwner: next, allActions: flatten(next) };
    });
  },
}));

/**
 * Registers `actions` under `ownerId` for the lifetime of the calling
 * component, replacing any previous registration under that same id and
 * removing them again on unmount — "pages register verbs on mount and
 * unregister on unmount" (T-903 deliverable). Two owners' actions never
 * collide even if their `id`s happen to match, since they're stored in
 * separate map slots; unmounting one owner only ever clears its own slot.
 *
 * Callers should pass a referentially-stable `actions` array (e.g. from
 * `useMemo`) — like every other effect here, an array literal passed
 * inline would re-register (and re-render CommandPalette's consumers) on
 * every render, which is wasteful but not incorrect since the effect
 * cleanup always runs first.
 */
export function usePaletteActions(ownerId: string, actions: readonly PaletteAction[]): void {
  const setOwnerActions = usePaletteActionsStore((s) => s.setOwnerActions);
  const clearOwnerActions = usePaletteActionsStore((s) => s.clearOwnerActions);
  useEffect(() => {
    setOwnerActions(ownerId, actions);
    return () => {
      clearOwnerActions(ownerId);
    };
  }, [ownerId, actions, setOwnerActions, clearOwnerActions]);
}

/** Flattened, currently-registered action list across every mounted owner
 * — what CommandPalette (and ShortcutHelpDialog, for its "available now"
 * section) reads. */
export function useAllPaletteActions(): readonly PaletteAction[] {
  return usePaletteActionsStore((s) => s.allActions);
}
