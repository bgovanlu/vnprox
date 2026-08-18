// T-3402: which of Sidebar.tsx's collapsible groups are expanded, persisted
// across reloads via zustand's `persist` middleware — mirrors
// src/store/theme.ts's identical shape/convention (a small, named
// localStorage key; see theme.test.ts for the `vi.resetModules()` +
// re-`import()` pattern this store's own test uses to prove a real
// round-trip through localStorage, not just in-memory state surviving a
// component remount).
//
// A group with no entry yet (first load, or a group added after a user's
// browser already has this key) defaults to expanded — every route was
// unconditionally visible before this task, and a freshly-added group
// silently starting collapsed would hide its routes from a returning user
// with no signal that anything moved.
import { create } from "zustand";
import { persist } from "zustand/middleware";

/** The three collapsible groups Sidebar.tsx renders (Home/Topology/Guests/
 * Management are flat, above any group; Settings is pinned below all of
 * them) — see Sidebar.tsx's own NAV_GROUPS for the routes each id owns. */
export const SIDEBAR_GROUP_IDS = ["network", "operate", "automate"] as const;
export type SidebarGroupId = (typeof SIDEBAR_GROUP_IDS)[number];

interface SidebarGroupsState {
  expanded: Partial<Record<SidebarGroupId, boolean>>;
  setExpanded: (id: SidebarGroupId, value: boolean) => void;
}

export const useSidebarGroupsStore = create<SidebarGroupsState>()(
  persist(
    (set) => ({
      expanded: {},
      setExpanded: (id, value) => {
        set((state) => ({ expanded: { ...state.expanded, [id]: value } }));
      },
    }),
    { name: "vnprox.sidebarGroups" },
  ),
);

/** Absent means "never toggled" -> defaults to expanded (see file header). */
export function isSidebarGroupExpanded(expanded: Partial<Record<SidebarGroupId, boolean>>, id: SidebarGroupId): boolean {
  return expanded[id] ?? true;
}
